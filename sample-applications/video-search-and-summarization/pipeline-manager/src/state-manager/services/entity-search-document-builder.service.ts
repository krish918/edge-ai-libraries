// SPDX-FileCopyrightText: (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

import { Injectable } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { DataPrepSummaryDTO } from 'src/data-prep/models/data-prep.models';
import { DetectedObject } from 'src/evam/models/message-broker.model';
import { State } from '../models/state.model';

export interface EntitySearchDocumentInput {
  state: State;
  frameIds: string[];
  caption: string;
  bucketName: string;
  chunkStartTime: number;
  chunkEndTime: number;
}

interface EntitySearchConfig {
  indexingEnabled: boolean;
  docsPerChunk: number;
  maxObjectsPerFrame: number;
  minConfidence: number;
  allowedLabels: Set<string>;
  dedupByLabel: boolean;
  includeInTags: boolean;
  contextChars: number;
}

interface NormalizedDetection {
  label: string;
  confidence: number;
  frameId: string;
  object: DetectedObject;
  timestampSeconds?: number;
  ordinal: number;
}

interface EntityDocumentGroup {
  label: string;
  detections: NormalizedDetection[];
}

@Injectable()
export class EntitySearchDocumentBuilderService {
  constructor(private $config: ConfigService) {}

  buildDocuments(input: EntitySearchDocumentInput): DataPrepSummaryDTO[] {
    const config = this.getConfig();
    const baseTags = Array.isArray(input.state.video?.tags)
      ? [...input.state.video.tags]
      : [];

    const documents: DataPrepSummaryDTO[] = [
      {
        bucket_name: input.bucketName,
        video_id: input.state.video.videoId,
        video_summary: input.caption,
        video_start_time: input.chunkStartTime,
        video_end_time: input.chunkEndTime,
        tags:
          config.indexingEnabled && config.includeInTags
            ? [...baseTags, 'doc:chunk-summary']
            : baseTags,
      },
    ];

    if (!config.indexingEnabled || config.docsPerChunk <= 0) {
      return documents;
    }

    const detections = this.collectDetections(input, config);
    if (detections.length === 0) {
      return documents;
    }

    const entityDocuments = this.groupDetections(detections, config)
      .sort((a, b) => this.compareGroups(a, b))
      .slice(0, config.docsPerChunk)
      .map((group) =>
        this.createEntityDocument(input, group, config, baseTags),
      );

    return [...documents, ...entityDocuments];
  }

  private getConfig(): EntitySearchConfig {
    const minConfidence = this.getNumberConfig(
      'minConfidence',
      0.35,
      0,
      1,
    );

    return {
      indexingEnabled: this.getBooleanConfig('indexingEnabled', false),
      docsPerChunk: this.getIntegerConfig('docsPerChunk', 8, 0),
      maxObjectsPerFrame: this.getIntegerConfig('maxObjectsPerFrame', 10, 0),
      minConfidence,
      allowedLabels: this.getAllowedLabelsConfig(),
      dedupByLabel: this.getBooleanConfig('dedupByLabel', true),
      includeInTags: this.getBooleanConfig('includeInTags', true),
      contextChars: this.getIntegerConfig('contextChars', 700, 0),
    };
  }

  private collectDetections(
    input: EntitySearchDocumentInput,
    config: EntitySearchConfig,
  ): NormalizedDetection[] {
    const detections: NormalizedDetection[] = [];
    const frameIds = this.sortFrameIds(input.frameIds);

    for (const frameId of frameIds) {
      const frame = input.state.frames?.[frameId];
      const frameObjects = frame?.metadata?.objects;
      const objects = Array.isArray(frameObjects) ? frameObjects : [];
      const perFrame: NormalizedDetection[] = [];

      objects.forEach((object, index) => {
        const label = this.normalizeLabel(
          object?.detection?.label || object?.roi_type,
        );
        const confidence = this.parseFiniteNumber(
          object?.detection?.confidence,
          0,
        );

        if (!label || confidence < config.minConfidence) {
          return;
        }

        if (
          config.allowedLabels.size > 0 &&
          !config.allowedLabels.has(label)
        ) {
          return;
        }

        perFrame.push({
          label,
          confidence,
          frameId,
          object,
          timestampSeconds: this.normalizeTimestampSeconds(
            frame?.metadata?.timestamp ?? frame?.metadata?.frame_timestamp,
          ),
          ordinal: detections.length + index,
        });
      });

      detections.push(
        ...perFrame
          .sort((a, b) => this.compareDetections(a, b))
          .slice(0, config.maxObjectsPerFrame),
      );
    }

    return detections;
  }

  private groupDetections(
    detections: NormalizedDetection[],
    config: EntitySearchConfig,
  ): EntityDocumentGroup[] {
    if (!config.dedupByLabel) {
      return detections.map((detection) => ({
        label: detection.label,
        detections: [detection],
      }));
    }

    const groups = new Map<string, NormalizedDetection[]>();
    for (const detection of detections) {
      const existing = groups.get(detection.label) ?? [];
      existing.push(detection);
      groups.set(detection.label, existing);
    }

    return [...groups.entries()].map(([label, groupDetections]) => ({
      label,
      detections: groupDetections,
    }));
  }

  private createEntityDocument(
    input: EntitySearchDocumentInput,
    group: EntityDocumentGroup,
    config: EntitySearchConfig,
    baseTags: string[],
  ): DataPrepSummaryDTO {
    const supportingFrames = this.sortFrameIds(
      group.detections.map((detection) => detection.frameId),
    );
    const maxConfidence = Math.max(
      ...group.detections.map((detection) => detection.confidence),
    );
    const averageConfidence =
      group.detections.reduce(
        (sum, detection) => sum + detection.confidence,
        0,
      ) / group.detections.length;
    const timeWindow = this.getEntityTimeWindow(input, group);
    const spatialEvidence = this.formatSpatialEvidence(group.detections);
    const context = this.truncateContext(input.caption, config.contextChars);

    const summaryParts = [
      'Entity-focused video search document.',
      `Entity: ${group.label}.`,
      `Observed label: ${group.label}.`,
      `Occurrences: ${group.detections.length} ${this.pluralize(
        'detection',
        group.detections.length,
      )} across frames ${supportingFrames.join(', ')}.`,
      `Confidence: max ${maxConfidence.toFixed(2)}, average ${averageConfidence.toFixed(2)}.`,
      `Temporal window: ${timeWindow.start}s to ${timeWindow.end}s.`,
      spatialEvidence,
      `Scene/chunk context: ${context}`,
      `User-visible summary: The chunk contains ${group.label}.`,
    ].filter((part): part is string => Boolean(part));

    return {
      bucket_name: input.bucketName,
      video_id: input.state.video.videoId,
      video_summary: summaryParts.join('\n'),
      video_start_time: timeWindow.start,
      video_end_time: timeWindow.end,
      tags: config.includeInTags
        ? [
            ...baseTags,
            'doc:entity-summary',
            `entity:${group.label}`,
            'entity-aware:v1',
          ]
        : baseTags,
    };
  }

  private getEntityTimeWindow(
    input: EntitySearchDocumentInput,
    group: EntityDocumentGroup,
  ): { start: number; end: number } {
    const timestamps = group.detections
      .map((detection) => detection.timestampSeconds)
      .filter(
        (timestamp): timestamp is number =>
          timestamp !== undefined &&
          timestamp >= input.chunkStartTime &&
          timestamp <= input.chunkEndTime,
      )
      .sort((a, b) => a - b);

    if (timestamps.length === 0) {
      return { start: input.chunkStartTime, end: input.chunkEndTime };
    }

    const start = Math.max(input.chunkStartTime, timestamps[0]);
    const end = Math.min(input.chunkEndTime, timestamps[timestamps.length - 1]);

    if (end <= start) {
      return { start: input.chunkStartTime, end: input.chunkEndTime };
    }

    return { start, end };
  }

  private formatSpatialEvidence(
    detections: NormalizedDetection[],
  ): string | undefined {
    const evidence = detections
      .slice(0, 3)
      .map((detection) => {
        const pixelBox = this.formatPixelBox(detection.object);
        const normalizedBox = this.formatNormalizedBox(
          detection.object?.detection?.bounding_box,
        );

        if (!pixelBox && !normalizedBox) {
          return undefined;
        }

        return [`frame ${detection.frameId}`, pixelBox ?? normalizedBox].join(
          ' ',
        );
      })
      .filter((item): item is string => Boolean(item));

    return evidence.length > 0
      ? `Spatial evidence: ${evidence.join('; ')}.`
      : undefined;
  }

  private formatPixelBox(object: DetectedObject): string | undefined {
    const x = this.parseOptionalFiniteNumber(object?.x);
    const y = this.parseOptionalFiniteNumber(object?.y);
    const w = this.parseOptionalFiniteNumber(object?.w);
    const h = this.parseOptionalFiniteNumber(object?.h);

    if (
      x === undefined ||
      y === undefined ||
      w === undefined ||
      h === undefined
    ) {
      return undefined;
    }

    return `bbox x=${Math.round(x)}, y=${Math.round(y)}, w=${Math.round(w)}, h=${Math.round(h)}`;
  }

  private formatNormalizedBox(
    box: DetectedObject['detection']['bounding_box'] | undefined,
  ): string | undefined {
    const xMin = this.parseOptionalFiniteNumber(box?.x_min);
    const yMin = this.parseOptionalFiniteNumber(box?.y_min);
    const xMax = this.parseOptionalFiniteNumber(box?.x_max);
    const yMax = this.parseOptionalFiniteNumber(box?.y_max);

    if (
      xMin === undefined ||
      yMin === undefined ||
      xMax === undefined ||
      yMax === undefined
    ) {
      return undefined;
    }

    return `normalized bbox x_min=${this.round3(xMin)}, y_min=${this.round3(
      yMin,
    )}, x_max=${this.round3(xMax)}, y_max=${this.round3(yMax)}`;
  }

  private truncateContext(caption: string, contextChars: number): string {
    const trimmed = caption?.trim() || 'No chunk caption available.';

    if (contextChars === 0) {
      return '';
    }

    if (trimmed.length <= contextChars) {
      return trimmed;
    }

    return `${trimmed.slice(0, contextChars).trimEnd()}...`;
  }

  private sortFrameIds(frameIds: string[]): string[] {
    return [...new Set(frameIds)].sort((a, b) => {
      const aNumber = Number(a);
      const bNumber = Number(b);

      if (Number.isFinite(aNumber) && Number.isFinite(bNumber)) {
        return aNumber - bNumber;
      }

      return a.localeCompare(b);
    });
  }

  private compareGroups(a: EntityDocumentGroup, b: EntityDocumentGroup): number {
    const maxDiff =
      Math.max(...b.detections.map((detection) => detection.confidence)) -
      Math.max(...a.detections.map((detection) => detection.confidence));

    if (maxDiff !== 0) {
      return maxDiff;
    }

    const supportDiff =
      this.getSupportingFrameCount(b) - this.getSupportingFrameCount(a);

    if (supportDiff !== 0) {
      return supportDiff;
    }

    return a.label.localeCompare(b.label);
  }

  private compareDetections(
    a: NormalizedDetection,
    b: NormalizedDetection,
  ): number {
    const confidenceDiff = b.confidence - a.confidence;
    if (confidenceDiff !== 0) {
      return confidenceDiff;
    }

    const labelDiff = a.label.localeCompare(b.label);
    if (labelDiff !== 0) {
      return labelDiff;
    }

    return a.ordinal - b.ordinal;
  }

  private getSupportingFrameCount(group: EntityDocumentGroup): number {
    return new Set(
      group.detections.map((detection) => detection.frameId),
    ).size;
  }

  private getAllowedLabelsConfig(): Set<string> {
    const rawValue = this.$config.get<unknown>(
      'search.entityAware.allowedLabels',
    );

    if (Array.isArray(rawValue)) {
      return new Set(
        rawValue.map((label) => this.normalizeLabel(label)).filter(Boolean),
      );
    }

    if (typeof rawValue === 'string') {
      return new Set(
        rawValue
          .split(',')
          .map((label) => this.normalizeLabel(label))
          .filter(Boolean),
      );
    }

    return new Set<string>();
  }

  private getBooleanConfig(key: string, defaultValue: boolean): boolean {
    const value = this.$config.get<unknown>(`search.entityAware.${key}`);

    if (typeof value === 'boolean') {
      return value;
    }

    if (typeof value === 'string') {
      const normalized = value.trim().toLowerCase();
      if (['true', '1', 'yes', 'y', 'on'].includes(normalized)) {
        return true;
      }
      if (['false', '0', 'no', 'n', 'off'].includes(normalized)) {
        return false;
      }
    }

    return defaultValue;
  }

  private getIntegerConfig(
    key: string,
    defaultValue: number,
    min: number,
  ): number {
    return Math.floor(this.getNumberConfig(key, defaultValue, min));
  }

  private getNumberConfig(
    key: string,
    defaultValue: number,
    min: number,
    max?: number,
  ): number {
    const value = this.$config.get<unknown>(`search.entityAware.${key}`);
    if (value === undefined || value === null || value === '') {
      return defaultValue;
    }

    const parsed = Number(value);

    if (
      !Number.isFinite(parsed) ||
      parsed < min ||
      (max !== undefined && parsed > max)
    ) {
      return defaultValue;
    }

    return parsed;
  }

  private normalizeLabel(value: unknown): string {
    return typeof value === 'string'
      ? value.trim().toLowerCase().replace(/\s+/g, ' ')
      : '';
  }

  private normalizeTimestampSeconds(value: unknown): number | undefined {
    const timestamp = this.parseOptionalFiniteNumber(value);

    if (timestamp === undefined) {
      return undefined;
    }

    if (timestamp > 1_000_000_000) {
      return timestamp / 1_000_000_000;
    }

    if (timestamp > 1_000_000) {
      return timestamp / 1_000;
    }

    return timestamp;
  }

  private parseFiniteNumber(value: unknown, fallback: number): number {
    return this.parseOptionalFiniteNumber(value) ?? fallback;
  }

  private parseOptionalFiniteNumber(value: unknown): number | undefined {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : undefined;
  }

  private pluralize(noun: string, count: number): string {
    return count === 1 ? noun : `${noun}s`;
  }

  private round3(value: number): string {
    return value.toFixed(3);
  }
}
