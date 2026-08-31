import { describe, expect, it } from 'vitest';
import { normalizeErrorInsightResponse, ERROR_CLASSES } from './errorInsight';

describe('normalizeErrorInsightResponse', () => {
  it('passes through a well-formed payload', () => {
    const raw = {
      classes: [{ class: 'auth', count: 3 }],
      timeline: [{ bucket_ms: 7200000, class: 'auth', count: 3 }],
      recent: [{ class: 'auth', timestamp_ms: 7300000, status_code: 401 }],
    };
    const view = normalizeErrorInsightResponse(raw);
    expect(view.classes).toEqual([{ class: 'auth', count: 3 }]);
    expect(view.timeline).toHaveLength(1);
    expect(view.recent[0]?.status_code).toBe(401);
  });

  it('defaults missing arrays and drops malformed rows', () => {
    const view = normalizeErrorInsightResponse({
      classes: [{ class: 'auth' }, { class: 'network', count: 2 }, null],
    });
    expect(view.classes).toEqual([{ class: 'network', count: 2 }]);
    expect(view.timeline).toEqual([]);
    expect(view.recent).toEqual([]);
  });

  it('returns empty view for a non-object payload', () => {
    expect(normalizeErrorInsightResponse('nope')).toEqual({
      classes: [],
      timeline: [],
      recent: [],
    });
  });
});

describe('ERROR_CLASSES', () => {
  it('lists the ten protocol classes plus other', () => {
    expect(ERROR_CLASSES).toEqual([
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
    ]);
  });
});
