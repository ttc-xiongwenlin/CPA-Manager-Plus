import { describe, expect, it } from 'vitest';
import type {
  ObservedProviderModel,
  ProviderModelPrice,
  RepriceState,
} from '@/services/api/providerPriceService';
import {
  buildObservedModels,
  buildObservedProviders,
  buildProviderPriceFromDraft,
  buildUnpricedObservedModels,
  calculateRepriceProgress,
  createEmptyProviderPriceDraft,
  createProviderPriceDraft,
  formatCnyRate,
  formatRepriceProgressPercent,
  formatWindowBadge,
  formatWindowMinute,
  groupProviderPrices,
  parseRepriceFromDate,
  parseWindowEndMinute,
  parseWindowStartMinute,
  removeProviderPrice,
  upsertProviderPrice,
} from './providerPricesPageModel';

const deepseekChat: ProviderModelPrice = {
  id: 1,
  provider: 'openai-compatible-deepseek',
  model: 'deepseek-chat',
  prompt: 2,
  completion: 8,
  cacheRead: 0.2,
  cacheCreation: 0,
  cacheReadConfigured: true,
  cacheCreationConfigured: false,
  timezone: 'Asia/Shanghai',
  note: '官网价',
  windows: [{ id: 7, startMinute: 30, endMinute: 510, multiplier: 0.5, label: '夜间' }],
};

const baiduErnie: ProviderModelPrice = {
  provider: 'openai-compatible-baidu',
  model: 'ernie-4.5',
  prompt: 4,
  completion: 16,
  timezone: 'Asia/Shanghai',
};

const observed: ObservedProviderModel[] = [
  { provider: 'openai-compatible-deepseek', model: 'deepseek-chat', calls: 120, lastSeenMs: 30 },
  { provider: 'openai-compatible-deepseek', model: 'deepseek-reasoner', calls: 40, lastSeenMs: 20 },
  { provider: 'codex', model: 'gpt-5.5', calls: 300, lastSeenMs: 10 },
  { provider: 'antigravity', model: 'gemini-3', calls: 40, lastSeenMs: 50 },
];

describe('window minute conversion', () => {
  it('formats minutes as HH:MM and treats 1440 as midnight', () => {
    expect(formatWindowMinute(30)).toBe('00:30');
    expect(formatWindowMinute(510)).toBe('08:30');
    expect(formatWindowMinute(1440)).toBe('00:00');
    expect(formatWindowMinute(0)).toBe('00:00');
  });

  it('parses window starts into 0..1439', () => {
    expect(parseWindowStartMinute('00:30')).toBe(30);
    expect(parseWindowStartMinute('8:30')).toBe(510);
    expect(parseWindowStartMinute('24:00')).toBe(0);
    expect(parseWindowStartMinute('25:00')).toBeNull();
    expect(parseWindowStartMinute('08:60')).toBeNull();
    expect(parseWindowStartMinute('abc')).toBeNull();
  });

  it('parses window ends into 1..1440 where 00:00 means end of day', () => {
    expect(parseWindowEndMinute('08:30')).toBe(510);
    expect(parseWindowEndMinute('00:00')).toBe(1440);
    expect(parseWindowEndMinute('24:00')).toBe(1440);
    expect(parseWindowEndMinute('')).toBeNull();
  });

  it('formats window badges', () => {
    expect(formatWindowBadge({ startMinute: 30, endMinute: 510, multiplier: 0.5 })).toBe(
      '00:30–08:30 ×0.5'
    );
    expect(
      formatWindowBadge({ startMinute: 1080, endMinute: 1440, multiplier: 2, label: '峰时' })
    ).toBe('18:00–00:00 ×2 峰时');
  });
});

describe('rate formatting', () => {
  it('formats CNY per 1M tokens', () => {
    expect(formatCnyRate(2)).toBe('¥2.0000/1M');
    expect(formatCnyRate(0.25)).toBe('¥0.2500/1M');
    expect(formatCnyRate(undefined)).toBe('¥0.0000/1M');
  });
});

describe('provider price drafts', () => {
  it('round-trips a price through the draft, keeping unconfigured cache rates empty', () => {
    const draft = createProviderPriceDraft(deepseekChat);
    expect(draft).toEqual({
      id: 1,
      provider: 'openai-compatible-deepseek',
      model: 'deepseek-chat',
      prompt: '2',
      completion: '8',
      cacheRead: '0.2',
      cacheCreation: '',
      timezone: 'Asia/Shanghai',
      note: '官网价',
      windows: [{ id: 7, start: '00:30', end: '08:30', multiplier: '0.5', label: '夜间' }],
    });

    expect(buildProviderPriceFromDraft(draft)).toEqual({ price: deepseekChat });
  });

  it('sends configured=false for empty cache rates and defaults the timezone', () => {
    const result = buildProviderPriceFromDraft({
      ...createEmptyProviderPriceDraft({ provider: ' codex ', model: ' gpt-5.5 ' }),
      prompt: '1.5',
      completion: '',
      windows: [{ start: '18:00', end: '00:00', multiplier: '2', label: ' 峰时 ' }],
    });

    expect(result.error).toBeUndefined();
    expect(result.price).toEqual({
      provider: 'codex',
      model: 'gpt-5.5',
      prompt: 1.5,
      completion: 0,
      cacheRead: 0,
      cacheCreation: 0,
      cacheReadConfigured: false,
      cacheCreationConfigured: false,
      timezone: 'Asia/Shanghai',
      windows: [{ startMinute: 1080, endMinute: 1440, multiplier: 2, label: '峰时' }],
    });
  });

  it('reports validation errors in priority order', () => {
    const base = createEmptyProviderPriceDraft();
    expect(buildProviderPriceFromDraft(base).error).toBe('provider_required');
    expect(buildProviderPriceFromDraft({ ...base, provider: 'codex' }).error).toBe(
      'model_required'
    );
    expect(
      buildProviderPriceFromDraft({ ...base, provider: 'codex', model: 'm', prompt: '-1' }).error
    ).toBe('rate_invalid');
    expect(
      buildProviderPriceFromDraft({ ...base, provider: 'codex', model: 'm', cacheRead: 'x' }).error
    ).toBe('rate_invalid');
    expect(
      buildProviderPriceFromDraft({ ...base, provider: 'codex', model: 'm', timezone: ' ' }).error
    ).toBe('timezone_required');
    expect(
      buildProviderPriceFromDraft({
        ...base,
        provider: 'codex',
        model: 'm',
        windows: [{ start: '9', end: '10:00', multiplier: '1', label: '' }],
      }).error
    ).toBe('window_time_invalid');
    expect(
      buildProviderPriceFromDraft({
        ...base,
        provider: 'codex',
        model: 'm',
        windows: [{ start: '09:00', end: '10:00', multiplier: '0', label: '' }],
      }).error
    ).toBe('window_multiplier_invalid');
  });
});

describe('rule list editing', () => {
  it('replaces the edited rule in place and appends new ones', () => {
    const edited = { ...deepseekChat, model: 'deepseek-chat-v2', prompt: 3 };
    const replaced = upsertProviderPrice(
      [deepseekChat, baiduErnie],
      edited,
      'openai-compatible-deepseek::deepseek-chat'
    );
    expect(replaced).toEqual([edited, baiduErnie]);

    const appended = upsertProviderPrice([deepseekChat], baiduErnie);
    expect(appended).toEqual([deepseekChat, baiduErnie]);

    const sameKey = upsertProviderPrice([deepseekChat, baiduErnie], {
      ...baiduErnie,
      provider: 'OPENAI-COMPATIBLE-BAIDU',
      prompt: 5,
    });
    expect(sameKey).toHaveLength(2);
    expect(sameKey[1].prompt).toBe(5);
  });

  it('removes a rule by provider and model', () => {
    expect(removeProviderPrice([deepseekChat, baiduErnie], baiduErnie)).toEqual([deepseekChat]);
  });

  it('groups rules by provider with sorted models', () => {
    const reasoner = { ...deepseekChat, model: 'deepseek-reasoner' };
    expect(groupProviderPrices([reasoner, baiduErnie, deepseekChat])).toEqual([
      { provider: 'openai-compatible-baidu', items: [baiduErnie] },
      { provider: 'openai-compatible-deepseek', items: [deepseekChat, reasoner] },
    ]);
  });
});

describe('observed provider models', () => {
  it('lists observed providers and models by call volume', () => {
    expect(buildObservedProviders(observed)).toEqual([
      'codex',
      'openai-compatible-deepseek',
      'antigravity',
    ]);
    expect(buildObservedModels(observed, 'openai-compatible-deepseek')).toEqual([
      'deepseek-chat',
      'deepseek-reasoner',
    ]);
    expect(buildObservedModels(observed, '')).toEqual([
      'gpt-5.5',
      'deepseek-chat',
      'deepseek-reasoner',
      'gemini-3',
    ]);
  });

  it('lists observed pairs that have no rule yet', () => {
    expect(buildUnpricedObservedModels(observed, [deepseekChat]).map((item) => item.model)).toEqual(
      ['gpt-5.5', 'gemini-3', 'deepseek-reasoner']
    );
  });
});

describe('reprice state', () => {
  const state: RepriceState = {
    status: 'repricing',
    pricedThroughEventId: 900,
    latestEventId: 1000,
    repricing: true,
    repriceEpoch: 2,
    repriceFromMs: 0,
    repriceTargetEventId: 1000,
    repricedThroughEventId: 250,
    processedEvents: 250,
    updatedAtMs: 1,
  };

  it('derives progress only while repricing', () => {
    expect(calculateRepriceProgress(state)).toBe(0.25);
    expect(calculateRepriceProgress({ ...state, repricedThroughEventId: 5000 })).toBe(1);
    expect(calculateRepriceProgress({ ...state, repricing: false, status: 'ready' })).toBeNull();
    expect(calculateRepriceProgress({ ...state, repriceTargetEventId: 0 })).toBeNull();
    expect(calculateRepriceProgress(null)).toBeNull();
    expect(formatRepriceProgressPercent(0.256)).toBe('26%');
  });

  it('parses the reprice start date as local midnight, empty meaning all history', () => {
    expect(parseRepriceFromDate('')).toBe(0);
    expect(parseRepriceFromDate('not-a-date')).toBe(0);
    expect(parseRepriceFromDate('2026-09-01')).toBe(new Date(2026, 8, 1).getTime());
  });
});
