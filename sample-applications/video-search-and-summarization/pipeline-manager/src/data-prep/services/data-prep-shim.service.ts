// Copyright (C) 2025 Intel Corporation
// SPDX-License-Identifier: Apache-2.0
import { HttpService } from '@nestjs/axios';
import { Injectable } from '@nestjs/common';
import { ConfigService } from '@nestjs/config';
import { EventEmitter2 } from '@nestjs/event-emitter';
import { from, mergeMap, of, tap, toArray } from 'rxjs';
import { SearchEvents } from 'src/events/Pipeline.events';
import {
  DataPrepMinioDTO,
  DataPrepMinioRO,
  DataPrepSummaryBatchOptions,
  DataPrepSummaryDTO,
} from '../models/data-prep.models';

@Injectable()
export class DataPrepShimService {
  constructor(
    private $config: ConfigService,
    private $http: HttpService,
    private $emitter: EventEmitter2,
  ) {}

  createEmbeddings(data: DataPrepMinioDTO) {
    const dataPrepEndpoint: string =
      this.$config.get<string>('search.dataPrep')!;
    const api = [dataPrepEndpoint, 'videos', 'minio'].join('/');
    const timeout =
      this.$config.get<number>('search.dataPrepTimeoutMs') ?? 30000;
    return this.$http.post<DataPrepMinioRO>(api, data, { timeout }).pipe(
      tap(() => {
        this.$emitter.emit(SearchEvents.EMBEDDINGS_UPDATE);
      }),
    );
  }

  createEmbeddingsFromSummary(data: DataPrepSummaryDTO) {
    return this.postSummary(data).pipe(
      tap(() => {
        this.$emitter.emit(SearchEvents.EMBEDDINGS_UPDATE);
      }),
    );
  }

  createEmbeddingsFromSummaries(
    data: DataPrepSummaryDTO[],
    options?: DataPrepSummaryBatchOptions,
  ) {
    const summaries = Array.isArray(data) ? data : [];

    if (summaries.length === 0) {
      return of([]);
    }

    const concurrency = this.resolveBatchConcurrency(options?.concurrency);
    const emitUpdate =
      options?.emitUpdate ??
      this.$config.get<boolean>('search.entityAware.emitBatchUpdateOnce') ??
      true;

    return from(summaries).pipe(
      mergeMap((summary) => this.postSummary(summary), concurrency),
      toArray(),
      tap(() => {
        if (emitUpdate) {
          this.$emitter.emit(SearchEvents.EMBEDDINGS_UPDATE);
        }
      }),
    );
  }

  private postSummary(data: DataPrepSummaryDTO) {
    const dataPrepEndpoint: string =
      this.$config.get<string>('search.dataPrep')!;
    const api = [dataPrepEndpoint, 'summary'].join('/');
    const timeout =
      this.$config.get<number>('search.dataPrepTimeoutMs') ?? 30000;

    return this.$http.post<DataPrepMinioRO>(api, data, { timeout });
  }

  private resolveBatchConcurrency(optionValue?: number): number {
    const configuredValue =
      optionValue ??
      this.$config.get<number>(
        'search.entityAware.dataPrepSummaryBatchConcurrency',
      );
    const parsed = Number(configuredValue);

    if (!Number.isFinite(parsed) || parsed < 1) {
      return 3;
    }

    return Math.floor(parsed);
  }
}
