import { describe, expect, it } from 'vitest';
import { ERROR_INSIGHT_MAX_WINDOW_MS } from '@/services/api/errorInsight';
import {
  buildBreakdownView,
  buildErrorInsightView,
  foldClass,
  ERROR_INSIGHT_WINDOW_PRESETS,
} from './errorInsightModel';

describe('foldClass', () => {
  it('keeps known classes and folds unknown into other', () => {
    expect(foldClass('auth')).toBe('auth');
    expect(foldClass('brand_new_backend_class')).toBe('other');
  });
});

describe('buildErrorInsightView', () => {
  it('computes totals, shares and aligned timeline series', () => {
    const view = buildErrorInsightView(
      {
        classes: [
          { class: 'rate_limited', count: 6 },
          { class: 'auth', count: 2 },
          { class: 'mystery', count: 2 },
        ],
        timeline: [
          { bucket_ms: 0, class: 'rate_limited', count: 4 },
          { bucket_ms: 3600000, class: 'rate_limited', count: 2 },
          { bucket_ms: 3600000, class: 'auth', count: 2 },
          { bucket_ms: 3600000, class: 'mystery', count: 2 },
        ],
        recent: [],
        by_provider: [],
        by_model: [],
      },
      { fromMs: 0, toMs: 3600000 }
    );
    expect(view.totalFailures).toBe(10);
    expect(view.shares[0]).toEqual({ class: 'rate_limited', count: 6, share: 0.6 });
    // mystery 折叠进 other
    expect(view.shares.find((s) => s.class === 'other')?.count).toBe(2);
    expect(view.timelineBuckets).toEqual([0, 3600000]);
    const rateSeries = view.timelineSeries.find((s) => s.class === 'rate_limited');
    expect(rateSeries?.data).toEqual([4, 2]);
    const otherSeries = view.timelineSeries.find((s) => s.class === 'other');
    expect(otherSeries?.data).toEqual([0, 2]);
    // donutData reuses the shares data
    expect(view.donutData).toEqual(view.shares);
  });

  it('handles an empty window', () => {
    const view = buildErrorInsightView(
      { classes: [], timeline: [], recent: [], by_provider: [], by_model: [] },
      { fromMs: 0, toMs: 0 }
    );
    expect(view.totalFailures).toBe(0);
    expect(view.shares).toEqual([]);
    expect(view.timelineSeries).toEqual([]);
  });

  it('zero-fills the hourly bucket sequence from the window start through the window end', () => {
    // 2 hour window aligned to the hour -> 3 hourly buckets (inclusive both ends).
    const view = buildErrorInsightView(
      {
        classes: [{ class: 'timeout', count: 1 }],
        timeline: [{ bucket_ms: 0, class: 'timeout', count: 1 }],
        recent: [],
        by_provider: [],
        by_model: [],
      },
      { fromMs: 0, toMs: 2 * 3600000 }
    );
    expect(view.timelineBuckets).toEqual([0, 3600000, 2 * 3600000]);
    const timeoutSeries = view.timelineSeries.find((s) => s.class === 'timeout');
    // the middle and last buckets have no data and must be zero-filled, not omitted.
    expect(timeoutSeries?.data).toEqual([1, 0, 0]);
  });

  it('aligns the bucket start down to the hour and truncates the tail beyond the 337 bucket cap', () => {
    const halfHourIntoTheHour = 30 * 60 * 1000;
    // 400 hourly intervals well past the 14d preset - a genuine contract
    // violation that must actually truncate.
    const view = buildErrorInsightView(
      { classes: [], timeline: [], recent: [], by_provider: [], by_model: [] },
      { fromMs: halfHourIntoTheHour, toMs: halfHourIntoTheHour + 400 * 3600000 }
    );
    expect(view.timelineBuckets[0]).toBe(0);
    expect(view.timelineBuckets.length).toBe(337);
  });

  it('produces exactly 337 buckets for the real 14d preset boundary, with no truncation', () => {
    // windowMs is an exact multiple of the hour (14d) but toMs itself is NOT
    // hour-aligned - this is the real shape of a live "now" request, and the
    // regression this guards against: 336 hourly intervals is a fencepost of
    // 337 bucket starts, not 336, so this must NOT get truncated.
    const toMs = Date.UTC(2026, 0, 15, 9, 23, 41, 500); // arbitrary, not on the hour
    const fromMs = toMs - ERROR_INSIGHT_MAX_WINDOW_MS;
    const view = buildErrorInsightView(
      { classes: [], timeline: [], recent: [], by_provider: [], by_model: [] },
      { fromMs, toMs }
    );
    expect(view.timelineBuckets.length).toBe(337);
    const hourMs = 3600000;
    const expectedLastBucket = Math.floor(toMs / hourMs) * hourMs;
    expect(view.timelineBuckets[view.timelineBuckets.length - 1]).toBe(expectedLastBucket);
    const expectedFirstBucket = Math.floor(fromMs / hourMs) * hourMs;
    expect(view.timelineBuckets[0]).toBe(expectedFirstBucket);
  });
});

describe('buildBreakdownView', () => {
  it('sorts keys by summed count desc and aligns zero-filled series in ERROR_CLASSES order', () => {
    const view = buildBreakdownView([
      { key: 'gpt-4o', class: 'rate_limited', count: 3 },
      { key: 'gpt-4o', class: 'timeout', count: 1 },
      { key: 'claude-3', class: 'rate_limited', count: 10 },
      { key: 'gemini', class: 'auth', count: 1 },
    ]);
    // claude-3 (10) > gpt-4o (4) > gemini (1)
    expect(view.keys).toEqual(['claude-3', 'gpt-4o', 'gemini']);
    // ERROR_CLASSES order: upstream_overloaded, rate_limited, quota_exhausted, client_canceled,
    // network, stream_aborted, timeout, upstream_error, invalid_request, auth, other.
    expect(view.series.map((s) => s.class)).toEqual(['rate_limited', 'timeout', 'auth']);
    const rateLimited = view.series.find((s) => s.class === 'rate_limited');
    expect(rateLimited?.data).toEqual([10, 3, 0]);
    const timeout = view.series.find((s) => s.class === 'timeout');
    expect(timeout?.data).toEqual([0, 1, 0]);
    const auth = view.series.find((s) => s.class === 'auth');
    expect(auth?.data).toEqual([0, 0, 1]);
  });

  it('handles no items', () => {
    const view = buildBreakdownView([]);
    expect(view.keys).toEqual([]);
    expect(view.series).toEqual([]);
  });
});

describe('kpis', () => {
  it('computes topClass, topShare, upstreamShare and canceledShare', () => {
    const view = buildErrorInsightView(
      {
        classes: [
          { class: 'upstream_overloaded', count: 4 },
          { class: 'upstream_error', count: 2 },
          { class: 'stream_aborted', count: 1 },
          { class: 'timeout', count: 1 },
          { class: 'client_canceled', count: 1 },
          { class: 'auth', count: 1 },
        ],
        timeline: [],
        recent: [],
        by_provider: [],
        by_model: [],
      },
      { fromMs: 0, toMs: 0 }
    );
    expect(view.kpis.totalFailures).toBe(10);
    expect(view.kpis.topClass).toBe('upstream_overloaded');
    expect(view.kpis.topShare).toBe(0.4);
    // upstream = (4+2+1+1)/10 = 0.8
    expect(view.kpis.upstreamShare).toBe(0.8);
    // canceled = 1/10 = 0.1
    expect(view.kpis.canceledShare).toBe(0.1);
  });

  it('is all-zero with topClass null when total is 0', () => {
    const view = buildErrorInsightView(
      { classes: [], timeline: [], recent: [], by_provider: [], by_model: [] },
      { fromMs: 0, toMs: 0 }
    );
    expect(view.kpis).toEqual({
      totalFailures: 0,
      topClass: null,
      topShare: 0,
      upstreamShare: 0,
      canceledShare: 0,
    });
  });
});

describe('ERROR_INSIGHT_WINDOW_PRESETS', () => {
  it('caps at 14 days', () => {
    const max = Math.max(...ERROR_INSIGHT_WINDOW_PRESETS.map((p) => p.ms));
    expect(max).toBe(14 * 24 * 60 * 60 * 1000);
  });
});
