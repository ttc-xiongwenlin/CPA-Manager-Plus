import {
  ERROR_CLASSES,
  ERROR_INSIGHT_MAX_WINDOW_MS,
  type ErrorClass,
  type ErrorInsightResponse,
} from '@/services/api/errorInsight';

export const ERROR_INSIGHT_WINDOW_PRESETS = [
  { key: '1h', ms: 60 * 60 * 1000 },
  { key: '6h', ms: 6 * 60 * 60 * 1000 },
  { key: '24h', ms: 24 * 60 * 60 * 1000 },
  { key: '3d', ms: 3 * 24 * 60 * 60 * 1000 },
  { key: '7d', ms: 7 * 24 * 60 * 60 * 1000 },
  { key: '14d', ms: ERROR_INSIGHT_MAX_WINDOW_MS },
] as const;

// Stable per-class colors so the same class keeps its color across the
// distribution list and the timeline chart.
export const ERROR_CLASS_COLORS: Record<ErrorClass, string> = {
  upstream_overloaded: '#e6772e',
  rate_limited: '#d4a72c',
  quota_exhausted: '#b8544f',
  client_canceled: '#8a8f98',
  network: '#4f77b8',
  stream_aborted: '#7a5fb0',
  timeout: '#3e9c9c',
  upstream_error: '#c04949',
  invalid_request: '#5a9a5a',
  auth: '#b05f8a',
  other: '#6b7280',
};

const KNOWN_CLASSES = new Set<string>(ERROR_CLASSES);

export function foldClass(name: string): ErrorClass {
  return KNOWN_CLASSES.has(name) ? (name as ErrorClass) : 'other';
}

export interface ErrorClassShare {
  class: ErrorClass;
  count: number;
  share: number;
}

export interface ErrorInsightView {
  totalFailures: number;
  shares: ErrorClassShare[];
  timelineBuckets: number[];
  timelineSeries: { class: ErrorClass; data: number[] }[];
  recent: ErrorInsightResponse['recent'];
}

export function buildErrorInsightView(response: ErrorInsightResponse): ErrorInsightView {
  const counts = new Map<ErrorClass, number>();
  let total = 0;
  for (const item of response.classes) {
    const cls = foldClass(item.class);
    counts.set(cls, (counts.get(cls) ?? 0) + item.count);
    total += item.count;
  }
  const shares: ErrorClassShare[] = [...counts.entries()]
    .map(([cls, count]) => ({
      class: cls,
      count,
      share: total > 0 ? count / total : 0,
    }))
    .sort((a, b) => b.count - a.count);

  const buckets = [...new Set(response.timeline.map((p) => p.bucket_ms))].sort((a, b) => a - b);
  const bucketIndex = new Map(buckets.map((bucket, index) => [bucket, index]));
  const seriesMap = new Map<ErrorClass, number[]>();
  for (const point of response.timeline) {
    const cls = foldClass(point.class);
    let data = seriesMap.get(cls);
    if (!data) {
      data = new Array<number>(buckets.length).fill(0);
      seriesMap.set(cls, data);
    }
    const index = bucketIndex.get(point.bucket_ms);
    if (index !== undefined) data[index] += point.count;
  }
  const timelineSeries = ERROR_CLASSES.filter((cls) => seriesMap.has(cls)).map((cls) => ({
    class: cls,
    data: seriesMap.get(cls) as number[],
  }));

  return {
    totalFailures: total,
    shares,
    timelineBuckets: buckets,
    timelineSeries,
    recent: response.recent,
  };
}
