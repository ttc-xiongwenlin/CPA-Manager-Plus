import {
  ERROR_CLASSES,
  ERROR_INSIGHT_MAX_WINDOW_MS,
  type ErrorClass,
  type ErrorInsightBreakdownItem,
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

export interface ErrorInsightKpis {
  totalFailures: number;
  topClass: ErrorClass | null;
  topShare: number;
  upstreamShare: number;
  canceledShare: number;
}

export interface ErrorInsightWindowBounds {
  fromMs: number;
  toMs: number;
}

export interface ErrorInsightBreakdownView {
  keys: string[];
  series: { class: ErrorClass; data: number[] }[];
}

export interface ErrorInsightView {
  totalFailures: number;
  shares: ErrorClassShare[];
  donutData: ErrorClassShare[];
  timelineBuckets: number[];
  timelineSeries: { class: ErrorClass; data: number[] }[];
  byProvider: ErrorInsightBreakdownView;
  byModel: ErrorInsightBreakdownView;
  kpis: ErrorInsightKpis;
  recent: ErrorInsightResponse['recent'];
}

const TIMELINE_BUCKET_MS = 60 * 60 * 1000;
// The 14d preset spans 336 hourly *intervals*, but buckets are inclusive of
// both the (hour-floored) window start and the window end, so the fencepost
// count of bucket starts is 336 + 1 = 337 (e.g. a 2h window yields buckets at
// +0h, +1h, +2h - 3 starts for 2 intervals). The cap must fit the worst
// legitimate case exactly, so it's 14*24 + 1, not 14*24.
const MAX_TIMELINE_BUCKETS = 14 * 24 + 1; // 337

// Classes that indicate the failure originated upstream (provider outage or
// transport failure) rather than on the client side.
const UPSTREAM_CLASSES = new Set<ErrorClass>([
  'upstream_overloaded',
  'upstream_error',
  'stream_aborted',
  'timeout',
]);

function buildZeroFilledBuckets(fromMs: number, toMs: number): number[] {
  const start = Math.floor(fromMs / TIMELINE_BUCKET_MS) * TIMELINE_BUCKET_MS;
  if (toMs < start) return [];
  const rawCount = Math.floor((toMs - start) / TIMELINE_BUCKET_MS) + 1;
  console.assert(
    rawCount <= MAX_TIMELINE_BUCKETS,
    `error-insight: timeline bucket count ${rawCount} exceeds cap ${MAX_TIMELINE_BUCKETS}; truncating tail`
  );
  const count = Math.min(rawCount, MAX_TIMELINE_BUCKETS);
  return Array.from({ length: count }, (_, index) => start + index * TIMELINE_BUCKET_MS);
}

function computeKpis(shares: ErrorClassShare[], total: number): ErrorInsightKpis {
  let upstreamCount = 0;
  let canceledCount = 0;
  for (const share of shares) {
    if (UPSTREAM_CLASSES.has(share.class)) upstreamCount += share.count;
    if (share.class === 'client_canceled') canceledCount += share.count;
  }
  const top = total > 0 ? shares[0] : undefined;
  return {
    totalFailures: total,
    topClass: top ? top.class : null,
    topShare: top ? top.share : 0,
    upstreamShare: total > 0 ? upstreamCount / total : 0,
    canceledShare: total > 0 ? canceledCount / total : 0,
  };
}

export function buildErrorInsightView(
  response: ErrorInsightResponse,
  windowBounds: ErrorInsightWindowBounds
): ErrorInsightView {
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

  const buckets = buildZeroFilledBuckets(windowBounds.fromMs, windowBounds.toMs);
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
    donutData: shares,
    timelineBuckets: buckets,
    timelineSeries,
    // by_provider/by_model breakdowns both go through buildBreakdownView
    // below; the page renders them straight off view.byProvider/view.byModel
    // (ErrorInsightPage.tsx's buildBreakdownOption), it doesn't call the
    // helper itself.
    byProvider: buildBreakdownView(response.by_provider),
    byModel: buildBreakdownView(response.by_model),
    kpis: computeKpis(shares, total),
    recent: response.recent,
  };
}

export function buildBreakdownView(items: ErrorInsightBreakdownItem[]): ErrorInsightBreakdownView {
  const totalsByKey = new Map<string, number>();
  const keyOrder: string[] = [];
  for (const item of items) {
    if (!totalsByKey.has(item.key)) {
      totalsByKey.set(item.key, 0);
      keyOrder.push(item.key);
    }
    totalsByKey.set(item.key, (totalsByKey.get(item.key) ?? 0) + item.count);
  }
  const keys = [...keyOrder].sort(
    (a, b) => (totalsByKey.get(b) ?? 0) - (totalsByKey.get(a) ?? 0)
  );
  const keyIndex = new Map(keys.map((key, index) => [key, index]));

  const seriesMap = new Map<ErrorClass, number[]>();
  for (const item of items) {
    const cls = foldClass(item.class);
    let data = seriesMap.get(cls);
    if (!data) {
      data = new Array<number>(keys.length).fill(0);
      seriesMap.set(cls, data);
    }
    const index = keyIndex.get(item.key);
    if (index !== undefined) data[index] += item.count;
  }
  const series = ERROR_CLASSES.filter((cls) => seriesMap.has(cls)).map((cls) => ({
    class: cls,
    data: seriesMap.get(cls) as number[],
  }));

  return { keys, series };
}
