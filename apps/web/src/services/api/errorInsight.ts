import axios from 'axios';
import { normalizeUsageServiceBase } from './usageService';

// Display order: capacity problems first (they dominate production volume),
// then transport, then caller-side classes.
export const ERROR_CLASSES = [
  'upstream_overloaded',
  'rate_limited',
  'quota_exhausted',
  'client_canceled',
  'network',
  'stream_aborted',
  'timeout',
  'upstream_error',
  'invalid_request',
  'auth',
  'other',
] as const;

export type ErrorClass = (typeof ERROR_CLASSES)[number];

export const ERROR_INSIGHT_MAX_WINDOW_MS = 14 * 24 * 60 * 60 * 1000;

const ERROR_INSIGHT_TIMEOUT_MS = 30 * 1000;

export interface ErrorInsightClassItem {
  class: string;
  count: number;
}

export interface ErrorInsightTimelineItem {
  bucket_ms: number;
  class: string;
  count: number;
}

export interface ErrorInsightRecentItem {
  class: string;
  timestamp_ms: number;
  status_code?: number;
  model?: string;
  account?: string;
  provider?: string;
  summary?: string;
  latency_ms?: number;
}

export interface ErrorInsightResponse {
  classes: ErrorInsightClassItem[];
  timeline: ErrorInsightTimelineItem[];
  recent: ErrorInsightRecentItem[];
}

export interface ErrorInsightRequest {
  from_ms: number;
  to_ms: number;
}

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object';

const readClassItem = (value: unknown): ErrorInsightClassItem | null => {
  if (!isRecord(value)) return null;
  if (typeof value.class !== 'string' || typeof value.count !== 'number') return null;
  return { class: value.class, count: value.count };
};

const readTimelineItem = (value: unknown): ErrorInsightTimelineItem | null => {
  if (!isRecord(value)) return null;
  if (
    typeof value.bucket_ms !== 'number' ||
    typeof value.class !== 'string' ||
    typeof value.count !== 'number'
  ) {
    return null;
  }
  return { bucket_ms: value.bucket_ms, class: value.class, count: value.count };
};

const readRecentItem = (value: unknown): ErrorInsightRecentItem | null => {
  if (!isRecord(value)) return null;
  if (typeof value.class !== 'string' || typeof value.timestamp_ms !== 'number') return null;
  const item: ErrorInsightRecentItem = {
    class: value.class,
    timestamp_ms: value.timestamp_ms,
  };
  if (typeof value.status_code === 'number') item.status_code = value.status_code;
  if (typeof value.model === 'string') item.model = value.model;
  if (typeof value.account === 'string') item.account = value.account;
  if (typeof value.provider === 'string') item.provider = value.provider;
  if (typeof value.summary === 'string') item.summary = value.summary;
  if (typeof value.latency_ms === 'number') item.latency_ms = value.latency_ms;
  return item;
};

const readArray = <T>(value: unknown, read: (entry: unknown) => T | null): T[] => {
  if (!Array.isArray(value)) return [];
  const out: T[] = [];
  for (const entry of value) {
    const item = read(entry);
    if (item !== null) out.push(item);
  }
  return out;
};

export function normalizeErrorInsightResponse(raw: unknown): ErrorInsightResponse {
  if (!isRecord(raw)) return { classes: [], timeline: [], recent: [] };
  return {
    classes: readArray(raw.classes, readClassItem),
    timeline: readArray(raw.timeline, readTimelineItem),
    recent: readArray(raw.recent, readRecentItem),
  };
}

export const errorInsightApi = {
  fetch: async (
    base: string,
    managementKey: string | undefined,
    request: ErrorInsightRequest,
    signal?: AbortSignal
  ): Promise<ErrorInsightResponse> => {
    const normalized = normalizeUsageServiceBase(base).replace(/\/+$/, '');
    const response = await axios.post<unknown>(
      `${normalized}/v0/management/error-insight`,
      request,
      {
        timeout: ERROR_INSIGHT_TIMEOUT_MS,
        headers: managementKey ? { Authorization: `Bearer ${managementKey}` } : undefined,
        signal,
      }
    );
    return normalizeErrorInsightResponse(response.data);
  },
};
