// SPDX-FileCopyrightText: (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

import { ConfigService } from '@nestjs/config';
import { EntitySearchDocumentBuilderService } from './entity-search-document-builder.service';
import { State } from '../models/state.model';

describe('EntitySearchDocumentBuilderService', () => {
  const createService = (overrides: Record<string, unknown> = {}) => {
    const config: Record<string, unknown> = {
      'search.entityAware.indexingEnabled': true,
      'search.entityAware.docsPerChunk': 8,
      'search.entityAware.maxObjectsPerFrame': 10,
      'search.entityAware.minConfidence': 0.35,
      'search.entityAware.allowedLabels': [],
      'search.entityAware.dedupByLabel': true,
      'search.entityAware.includeInTags': true,
      'search.entityAware.contextChars': 700,
      ...overrides,
    };

    return new EntitySearchDocumentBuilderService({
      get: jest.fn((key: string) => config[key]),
    } as unknown as ConfigService);
  };

  const detectedObject = (
    label: string,
    confidence: number,
    box: Partial<{
      x: number;
      y: number;
      w: number;
      h: number;
    }> = {},
  ) => ({
    detection: {
      bounding_box: {
        x_min: 0.1,
        y_min: 0.2,
        x_max: 0.4,
        y_max: 0.8,
      },
      confidence,
      label,
      label_id: 1,
    },
    h: box.h ?? 20,
    region_id: 1,
    roi_type: label,
    w: box.w ?? 10,
    x: box.x ?? 5,
    y: box.y ?? 7,
  });

  const baseState = (frames: State['frames']): State =>
    ({
      stateId: 'state-1',
      video: {
        videoId: 'video-1',
        name: 'video.mp4',
        url: 'http://example.com/video.mp4',
        tags: ['user-tag'],
        createdAt: '2026-07-01T00:00:00Z',
        updatedAt: '2026-07-01T00:00:00Z',
      },
      frames,
      frameSummaries: {},
      chunks: {},
      userInputs: {},
      systemConfig: {},
      status: {},
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-01T00:00:00Z',
      title: 'test',
    }) as unknown as State;

  const build = (
    service: EntitySearchDocumentBuilderService,
    state: State,
    caption = 'A person walks near a car in the parking lot.',
  ) =>
    service.buildDocuments({
      state,
      frameIds: ['2', '1', '2'],
      caption,
      bucketName: 'bucket-1',
      chunkStartTime: 10,
      chunkEndTime: 20,
    });

  it('returns the original chunk summary first and unchanged when disabled', () => {
    const service = createService({
      'search.entityAware.indexingEnabled': false,
    });
    const state = baseState({
      '1': {
        frameId: '1',
        chunkId: '0',
        createdAt: '',
        frameUri: '',
        metadata: { objects: [detectedObject('person', 0.9)] } as any,
      },
    });

    const documents = build(service, state, 'Original chunk caption');

    expect(documents).toEqual([
      {
        bucket_name: 'bucket-1',
        video_id: 'video-1',
        video_summary: 'Original chunk caption',
        video_start_time: 10,
        video_end_time: 20,
        tags: ['user-tag'],
      },
    ]);
  });

  it('returns only the chunk summary when there are no objects', () => {
    const service = createService();
    const state = baseState({
      '1': {
        frameId: '1',
        chunkId: '0',
        createdAt: '',
        frameUri: '',
        metadata: { objects: [] } as any,
      },
    });

    const documents = build(service, state);

    expect(documents).toHaveLength(1);
    expect(documents[0].tags).toEqual(['user-tag', 'doc:chunk-summary']);
  });

  it('filters low-confidence and non-allowlisted objects', () => {
    const service = createService({
      'search.entityAware.allowedLabels': ['person'],
      'search.entityAware.minConfidence': 0.5,
    });
    const state = baseState({
      '1': {
        frameId: '1',
        chunkId: '0',
        createdAt: '',
        frameUri: '',
        metadata: {
          objects: [
            detectedObject('person', 0.4),
            detectedObject('car', 0.95),
            detectedObject('person', 0.8),
          ],
        } as any,
      },
    });

    const documents = build(service, state);

    expect(documents).toHaveLength(2);
    expect(documents[1].tags).toContain('entity:person');
    expect(documents[1].video_summary).toContain('Entity: person.');
    expect(documents[1].video_summary).not.toContain('Entity: car.');
  });

  it('groups duplicate labels and adds internal tags', () => {
    const service = createService();
    const state = baseState({
      '1': {
        frameId: '1',
        chunkId: '0',
        createdAt: '',
        frameUri: '',
        metadata: { objects: [detectedObject('Person', 0.9)] } as any,
      },
      '2': {
        frameId: '2',
        chunkId: '0',
        createdAt: '',
        frameUri: '',
        metadata: { objects: [detectedObject('person', 0.7)] } as any,
      },
    });

    const documents = build(service, state);

    expect(documents).toHaveLength(2);
    expect(documents[1].tags).toEqual([
      'user-tag',
      'doc:entity-summary',
      'entity:person',
      'entity-aware:v1',
    ]);
    expect(documents[1].video_summary).toContain(
      'Occurrences: 2 detections across frames 1, 2.',
    );
  });

  it('caps entity documents per chunk', () => {
    const service = createService({
      'search.entityAware.docsPerChunk': 2,
    });
    const state = baseState({
      '1': {
        frameId: '1',
        chunkId: '0',
        createdAt: '',
        frameUri: '',
        metadata: {
          objects: [
            detectedObject('person', 0.9),
            detectedObject('car', 0.8),
            detectedObject('bicycle', 0.7),
          ],
        } as any,
      },
      '2': {
        frameId: '2',
        chunkId: '0',
        createdAt: '',
        frameUri: '',
        metadata: { objects: [detectedObject('dog', 0.6)] } as any,
      },
    });

    const documents = build(service, state);

    expect(documents).toHaveLength(3);
    expect(documents.slice(1).map((document) => document.tags[2])).toEqual([
      'entity:person',
      'entity:car',
    ]);
  });

  it('truncates context in entity documents', () => {
    const service = createService({
      'search.entityAware.contextChars': 12,
    });
    const state = baseState({
      '1': {
        frameId: '1',
        chunkId: '0',
        createdAt: '',
        frameUri: '',
        metadata: { objects: [detectedObject('person', 0.9)] } as any,
      },
    });

    const documents = build(service, state, '01234567890123456789');

    expect(documents[1].video_summary).toContain('Scene/chunk context: 012345678901...');
    expect(documents[1].video_summary).not.toContain('01234567890123456789');
  });

  it('does not throw when metadata is malformed', () => {
    const service = createService();
    const state = baseState({
      '1': {
        frameId: '1',
        chunkId: '0',
        createdAt: '',
        frameUri: '',
        metadata: { objects: 'not-an-array' } as any,
      },
      '2': {
        frameId: '2',
        chunkId: '0',
        createdAt: '',
        frameUri: '',
        metadata: {
          objects: [
            {
              detection: {
                confidence: '0.9',
                label: 'person',
                bounding_box: { x_min: 'bad' },
              },
              roi_type: 'person',
            },
          ],
        } as any,
      },
    });

    expect(() => build(service, state, '')).not.toThrow();
    const documents = build(service, state, '');
    expect(documents).toHaveLength(2);
    expect(documents[1].video_summary).toContain(
      'Scene/chunk context: No chunk caption available.',
    );
  });
});
