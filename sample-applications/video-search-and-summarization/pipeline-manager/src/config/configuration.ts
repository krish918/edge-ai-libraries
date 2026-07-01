// Copyright (C) 2025 Intel Corporation
// SPDX-License-Identifier: Apache-2.0
import { CONFIG_STATE } from 'src/features/features.model';

const parseBoolean = (
  envName: string,
  value: string | undefined,
  defaultValue: boolean,
): boolean => {
  if (value === undefined || value.trim() === '') {
    return defaultValue;
  }

  const normalized = value.trim().toLowerCase();
  if (['true', '1', 'yes', 'y', 'on'].includes(normalized)) {
    return true;
  }

  if (['false', '0', 'no', 'n', 'off'].includes(normalized)) {
    return false;
  }

  console.warn(
    `Invalid boolean value for ${envName}: "${value}". Using default ${defaultValue}.`,
  );
  return defaultValue;
};

const parseNumber = (
  envName: string,
  value: string | undefined,
  defaultValue: number,
  options: { integer?: boolean; min?: number; max?: number } = {},
): number => {
  if (value === undefined || value.trim() === '') {
    return defaultValue;
  }

  const parsed = Number(value);
  const belowMin = options.min !== undefined && parsed < options.min;
  const aboveMax = options.max !== undefined && parsed > options.max;

  if (!Number.isFinite(parsed) || belowMin || aboveMax) {
    console.warn(
      `Invalid numeric value for ${envName}: "${value}". Using default ${defaultValue}.`,
    );
    return defaultValue;
  }

  return options.integer ? Math.floor(parsed) : parsed;
};

const parseAllowedLabels = (value: string | undefined): string[] => {
  if (!value) {
    return [];
  }

  return value
    .split(',')
    .map((label) => label.trim().toLowerCase())
    .filter((label) => label.length > 0);
};

export default () => ({
  features: {
    summary: process.env.SUMMARY_FEATURE,
    search: process.env.SEARCH_FEATURE,
  },
  datastore: {
    baseUrl: 'http://datastore:9009',
    host: process.env.MINIO_HOST,
    protocol: process.env.MINIO_PROTOCOL,
    port: process.env.MINIO_PORT,
    accessKey: process.env.MINIO_ROOT_USER,
    secretKey: process.env.MINIO_ROOT_PASSWORD,
    bucketName: process.env.MINIO_BUCKET,
    maxFileSize: process.env.MAX_FILE_SIZE ?? 1_000_000_000,
  },
  search: {
    endpoint: process.env.SEARCH_ENDPOINT,
    dataPrep: process.env.SEARCH_DATAPREP_ENDPOINT,
    dataPrepTimeoutMs: process.env.SEARCH_DATAPREP_TIMEOUT_MS
      ? Number(process.env.SEARCH_DATAPREP_TIMEOUT_MS)
      : 30000,
    entityAware: {
      indexingEnabled: parseBoolean(
        'SEARCH_ENTITY_AWARE_INDEXING_ENABLED',
        process.env.SEARCH_ENTITY_AWARE_INDEXING_ENABLED,
        false,
      ),
      docsPerChunk: parseNumber(
        'SEARCH_ENTITY_DOCS_PER_CHUNK',
        process.env.SEARCH_ENTITY_DOCS_PER_CHUNK,
        8,
        { integer: true, min: 0 },
      ),
      maxObjectsPerFrame: parseNumber(
        'SEARCH_ENTITY_MAX_OBJECTS_PER_FRAME',
        process.env.SEARCH_ENTITY_MAX_OBJECTS_PER_FRAME,
        10,
        { integer: true, min: 0 },
      ),
      minConfidence: parseNumber(
        'SEARCH_ENTITY_MIN_CONFIDENCE',
        process.env.SEARCH_ENTITY_MIN_CONFIDENCE,
        0.35,
        { min: 0, max: 1 },
      ),
      allowedLabels: parseAllowedLabels(
        process.env.SEARCH_ENTITY_ALLOWED_LABELS,
      ),
      dedupByLabel: parseBoolean(
        'SEARCH_ENTITY_DEDUP_BY_LABEL',
        process.env.SEARCH_ENTITY_DEDUP_BY_LABEL,
        true,
      ),
      includeInTags: parseBoolean(
        'SEARCH_ENTITY_INCLUDE_IN_TAGS',
        process.env.SEARCH_ENTITY_INCLUDE_IN_TAGS,
        true,
      ),
      contextChars: parseNumber(
        'SEARCH_ENTITY_CONTEXT_CHARS',
        process.env.SEARCH_ENTITY_CONTEXT_CHARS,
        700,
        { integer: true, min: 0 },
      ),
      dataPrepSummaryBatchConcurrency: parseNumber(
        'SEARCH_DATAPREP_SUMMARY_BATCH_CONCURRENCY',
        process.env.SEARCH_DATAPREP_SUMMARY_BATCH_CONCURRENCY,
        3,
        { integer: true, min: 1 },
      ),
      emitBatchUpdateOnce: parseBoolean(
        'SEARCH_DATAPREP_EMIT_BATCH_UPDATE_ONCE',
        process.env.SEARCH_DATAPREP_EMIT_BATCH_UPDATE_ONCE,
        true,
      ),
    },
  },
  database: {
    host: process.env.DB_HOST,
    port: process.env.DB_PORT,
    username: process.env.DB_USER,
    password: process.env.DB_PASSWORD,
    database: process.env.DB_NAME,
  },
  proxy: {
    noProxy: process.env.no_proxy,
    url: process.env.proxy ?? 'http://proxy.example.com:8080',
  },
  evam: {
    host: process.env.EVAM_HOST,
    pipelinePort: process.env.EVAM_PIPELINE_PORT,
    publishPort: process.env.EVAM_PUBLISH_PORT,
    videoTopic: 'topic/video_stream',
    datetimeFormat: 'yyyy_MM_dd-HH_mm_ss.S',
    model: 'yolov8l-worldv2',
    modelPath:
      '/home/pipeline-server/models/yoloworld/v2/FP32/yolov8l-worldv2.xml',
    device: process.env.EVAM_DEVICE ?? 'CPU',
    rmq: {
      queue: 'my_mqtt_queue',
      exchange: 'amq.topic',
    },
  },

  audio: {
    host: process.env.AUDIO_HOST,
    device: process.env.AUDIO_DEVICE,
    useFullTranscriptSummary:
      process.env.AUDIO_USE_FULL_TRANSCRIPT_SUMMARY ?? 'true',
    version: 'api/v1',
    apiHealth: 'health',
    apiTranscription: 'transcriptions',
    apiModels: 'models',
  },

  summary: {
    produceFinalSummary:
      process.env.PRODUCE_FINAL_SUMMARY ?? 'true',
  },

  rmq: {
    host: process.env.RABBITMQ_HOST,
    amqpPort: process.env.RABBITMQ_AMQP_PORT,
    username: process.env.RABBITMQ_USER,
    password: process.env.RABBITMQ_PASSWORD,
  },

  // OpenAI-compatible API configuration for VLM captioning and LLM summarization.
  // USE_VLLM controls backend selection: CONFIG_ON = vLLM, CONFIG_OFF = OVMS.
  openai: {
    usecase: 'default',
    llmSummarization: {
      apiKey: process.env.LLM_SUMMARIZATION_KEY ?? '',
      apiBase: process.env.LLM_SUMMARIZATION_API,
      useVLLM: process.env.USE_VLLM ?? CONFIG_STATE.OFF,
      maxContextLength: process.env.MAX_CONTEXT_LENGTH ?? 90_000,
      device: process.env.LLM_SUMMARIZATION_DEVICE,
      concurrent: process.env.LLM_CONCURRENT,
      modelName: process.env.LLM_MODEL_NAME,
      modelsAPI: process.env.LLM_MODEL_API ?? 'v1/config',
      defaults: {
        temperature: null,
        topP: null,
        presencePenalty: null,
        maxCompletionTokens: process.env.SUMMARIZATION_MAX_COMPLETION_TOKENS,
        frequencyPenalty: null,
        doSample: false,
        seed: 42,
      },
    },
  tick: {
    interval: parseInt(process.env.TICK_INTERVAL_MS ?? '5000', 10),
    fastInterval: parseInt(process.env.FAST_TICK_INTERVAL_MS ?? '2000', 10),
  },

    vlmCaptioning: {
      apiKey: process.env.VLM_CAPTIONING_KEY ?? '',
      apiBase: process.env.VLM_CAPTIONING_API,
      useVLLM: process.env.USE_VLLM ?? CONFIG_STATE.OFF,
      device: process.env.VLM_CAPTIONING_DEVICE,
      concurrent: process.env.VLM_CONCURRENT,
      modelName: process.env.VLM_MODEL_NAME,
      modelsAPI: process.env.VLM_MODEL_API ?? 'v1/config',
      multiFrame: process.env.MULTI_FRAME_COUNT ?? 12, // process.env.MULTI_FRAME_COUNT ?? 5,
      frameOverlap: 0, // process.env.FRAME_OVERFLAP ?? 0,
      defaults: {
        temperature: null,
        topP: null,
        presencePenalty: null,
        maxCompletionTokens: process.env.CAPTIONING_MAX_COMPLETION_TOKENS,
        frequencyPenalty: null,
        doSample: false,
        seed: 42,
      },
    },
  },
});
