// Copyright (C) 2025 Intel Corporation
// SPDX-License-Identifier: Apache-2.0
import { Test, TestingModule } from '@nestjs/testing';
import { DataPrepShimService } from './data-prep-shim.service';
import { ConfigService } from '@nestjs/config';
import { HttpService } from '@nestjs/axios';
import { EventEmitter2 } from '@nestjs/event-emitter';
import { lastValueFrom, of, throwError } from 'rxjs';
import { SearchEvents } from 'src/events/Pipeline.events';
import { DataPrepSummaryDTO } from '../models/data-prep.models';

describe('DataPrepShimService', () => {
  let service: DataPrepShimService;
  let httpService: jest.Mocked<HttpService>;
  let eventEmitter: jest.Mocked<EventEmitter2>;

  const summary: DataPrepSummaryDTO = {
    bucket_name: 'bucket',
    video_id: 'video-1',
    video_summary: 'summary',
    video_start_time: 0,
    video_end_time: 10,
    tags: ['tag'],
  };

  beforeEach(async () => {
    const module: TestingModule = await Test.createTestingModule({
      providers: [
        DataPrepShimService,
        {
          provide: ConfigService,
          useValue: {
            get: jest.fn((key: string) => {
              const config: Record<string, unknown> = {
                'search.dataPrep': 'http://localhost:8080/dataprep',
                'search.dataPrepTimeoutMs': 1000,
                'search.entityAware.dataPrepSummaryBatchConcurrency': 2,
                'search.entityAware.emitBatchUpdateOnce': true,
              };
              return config[key];
            }),
          },
        },
        {
          provide: HttpService,
          useValue: {
            post: jest.fn(),
          },
        },
        {
          provide: EventEmitter2,
          useValue: {
            emit: jest.fn(),
          },
        },
      ],
    }).compile();

    service = module.get<DataPrepShimService>(DataPrepShimService);
    httpService = module.get(HttpService) as jest.Mocked<HttpService>;
    eventEmitter = module.get(EventEmitter2) as jest.Mocked<EventEmitter2>;
  });

  it('should be defined', () => {
    expect(service).toBeDefined();
  });

  it('preserves single summary embedding update behavior', async () => {
    httpService.post.mockReturnValue(of({ data: { status: 'ok' } }) as any);

    await lastValueFrom(service.createEmbeddingsFromSummary(summary));

    expect(httpService.post).toHaveBeenCalledWith(
      'http://localhost:8080/dataprep/summary',
      summary,
      { timeout: 1000 },
    );
    expect(eventEmitter.emit).toHaveBeenCalledTimes(1);
    expect(eventEmitter.emit).toHaveBeenCalledWith(
      SearchEvents.EMBEDDINGS_UPDATE,
    );
  });

  it('posts summary batches and emits one update after all posts succeed', async () => {
    httpService.post.mockReturnValue(of({ data: { status: 'ok' } }) as any);

    await lastValueFrom(
      service.createEmbeddingsFromSummaries([
        summary,
        { ...summary, video_summary: 'summary 2' },
        { ...summary, video_summary: 'summary 3' },
      ]),
    );

    expect(httpService.post).toHaveBeenCalledTimes(3);
    expect(eventEmitter.emit).toHaveBeenCalledTimes(1);
    expect(eventEmitter.emit).toHaveBeenCalledWith(
      SearchEvents.EMBEDDINGS_UPDATE,
    );
  });

  it('does not emit an update when any batch post fails', async () => {
    httpService.post
      .mockReturnValueOnce(of({ data: { status: 'ok' } }) as any)
      .mockReturnValueOnce(throwError(() => new Error('dataprep failed')));

    await expect(
      lastValueFrom(
        service.createEmbeddingsFromSummaries([
          summary,
          { ...summary, video_summary: 'summary 2' },
        ]),
      ),
    ).rejects.toThrow('dataprep failed');

    expect(eventEmitter.emit).not.toHaveBeenCalled();
  });

  it('skips HTTP calls and events for an empty batch', async () => {
    await expect(
      lastValueFrom(service.createEmbeddingsFromSummaries([])),
    ).resolves.toEqual([]);

    expect(httpService.post).not.toHaveBeenCalled();
    expect(eventEmitter.emit).not.toHaveBeenCalled();
  });
});
