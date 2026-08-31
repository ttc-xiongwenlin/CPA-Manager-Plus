import { describe, expect, it } from 'vitest';
import {
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
    const view = buildErrorInsightView({
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
    });
    expect(view.totalFailures).toBe(10);
    expect(view.shares[0]).toEqual({ class: 'rate_limited', count: 6, share: 0.6 });
    // mystery 折叠进 other
    expect(view.shares.find((s) => s.class === 'other')?.count).toBe(2);
    expect(view.timelineBuckets).toEqual([0, 3600000]);
    const rateSeries = view.timelineSeries.find((s) => s.class === 'rate_limited');
    expect(rateSeries?.data).toEqual([4, 2]);
    const otherSeries = view.timelineSeries.find((s) => s.class === 'other');
    expect(otherSeries?.data).toEqual([0, 2]);
  });

  it('handles an empty window', () => {
    const view = buildErrorInsightView({ classes: [], timeline: [], recent: [] });
    expect(view.totalFailures).toBe(0);
    expect(view.shares).toEqual([]);
    expect(view.timelineSeries).toEqual([]);
  });
});

describe('ERROR_INSIGHT_WINDOW_PRESETS', () => {
  it('caps at 14 days', () => {
    const max = Math.max(...ERROR_INSIGHT_WINDOW_PRESETS.map((p) => p.ms));
    expect(max).toBe(14 * 24 * 60 * 60 * 1000);
  });
});
