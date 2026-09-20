import {
  DEFAULT_PROVIDER_PRICE_TIMEZONE,
  type ObservedProviderModel,
  type ProviderModelPrice,
  type ProviderPriceWindow,
  type RepriceState,
} from '@/services/api/providerPriceService';

export const MINUTES_PER_DAY = 1440;

export type ProviderPriceWindowDraft = {
  id?: number;
  start: string;
  end: string;
  multiplier: string;
  label: string;
};

export type ProviderPriceDraft = {
  id?: number;
  provider: string;
  model: string;
  prompt: string;
  completion: string;
  cacheRead: string;
  cacheCreation: string;
  timezone: string;
  note: string;
  windows: ProviderPriceWindowDraft[];
};

export type ProviderPriceDraftError =
  | 'provider_required'
  | 'model_required'
  | 'rate_invalid'
  | 'timezone_required'
  | 'window_time_invalid'
  | 'window_multiplier_invalid';

export type ProviderPriceGroup = {
  provider: string;
  items: ProviderModelPrice[];
};

const padTwo = (value: number) => String(value).padStart(2, '0');

const normalizeKeyPart = (value: string) => value.trim().toLowerCase();

export const providerPriceKey = (provider: string, model: string) =>
  `${normalizeKeyPart(provider)}::${normalizeKeyPart(model)}`;

/** Minutes since local midnight -> `HH:MM`; 1440 (end of day) renders as `00:00`. */
export const formatWindowMinute = (minute: number): string => {
  const rounded = Number.isFinite(minute) ? Math.round(minute) : 0;
  const normalized = ((rounded % MINUTES_PER_DAY) + MINUTES_PER_DAY) % MINUTES_PER_DAY;
  return `${padTwo(Math.floor(normalized / 60))}:${padTwo(normalized % 60)}`;
};

/** `HH:MM` -> minutes since midnight in 0..1440 (`24:00` = 1440), or null when malformed. */
export const parseWindowMinute = (value: string): number | null => {
  const match = /^(\d{1,2}):(\d{2})$/.exec(value.trim());
  if (!match) return null;
  const hours = Number(match[1]);
  const minutes = Number(match[2]);
  if (hours > 24 || minutes > 59 || (hours === 24 && minutes !== 0)) return null;
  return hours * 60 + minutes;
};

/** Window start: 0..1439 (`24:00` wraps to `00:00`). */
export const parseWindowStartMinute = (value: string): number | null => {
  const minute = parseWindowMinute(value);
  if (minute === null) return null;
  return minute % MINUTES_PER_DAY;
};

/** Window end: 1..1440; `00:00` (and `24:00`) mean midnight at the end of the day. */
export const parseWindowEndMinute = (value: string): number | null => {
  const minute = parseWindowMinute(value);
  if (minute === null) return null;
  return minute === 0 ? MINUTES_PER_DAY : minute;
};

export const formatWindowMultiplier = (multiplier: number): string => {
  const value = Number(multiplier);
  if (!Number.isFinite(value)) return '1';
  return String(Number(value.toFixed(4)));
};

/** e.g. `00:30–08:30 ×0.5`; the label, when present, is appended after a space. */
export const formatWindowBadge = (window: ProviderPriceWindow): string => {
  const range = `${formatWindowMinute(window.startMinute)}–${formatWindowMinute(window.endMinute)} ×${formatWindowMultiplier(window.multiplier)}`;
  const label = window.label?.trim();
  return label ? `${range} ${label}` : range;
};

/** CNY per 1M tokens, e.g. `¥2.0000/1M`. */
export const formatCnyRate = (value: number | undefined | null): string => {
  const rate = Number(value);
  return `¥${(Number.isFinite(rate) ? rate : 0).toFixed(4)}/1M`;
};

const rateToDraft = (value: number | undefined, configured = true): string =>
  configured && Number.isFinite(Number(value)) ? String(Number(value)) : '';

export const createEmptyProviderPriceDraft = (
  overrides: Partial<Pick<ProviderPriceDraft, 'provider' | 'model'>> = {}
): ProviderPriceDraft => ({
  provider: overrides.provider ?? '',
  model: overrides.model ?? '',
  prompt: '',
  completion: '',
  cacheRead: '',
  cacheCreation: '',
  timezone: DEFAULT_PROVIDER_PRICE_TIMEZONE,
  note: '',
  windows: [],
});

export const createEmptyWindowDraft = (): ProviderPriceWindowDraft => ({
  start: '',
  end: '',
  multiplier: '1',
  label: '',
});

export const createProviderPriceDraft = (price: ProviderModelPrice): ProviderPriceDraft => ({
  id: price.id,
  provider: price.provider,
  model: price.model,
  prompt: rateToDraft(price.prompt),
  completion: rateToDraft(price.completion),
  cacheRead: rateToDraft(price.cacheRead, price.cacheReadConfigured === true),
  cacheCreation: rateToDraft(price.cacheCreation, price.cacheCreationConfigured === true),
  timezone: price.timezone || DEFAULT_PROVIDER_PRICE_TIMEZONE,
  note: price.note ?? '',
  windows: (price.windows ?? []).map((window) => ({
    id: window.id,
    start: formatWindowMinute(window.startMinute),
    end: formatWindowMinute(window.endMinute),
    multiplier: formatWindowMultiplier(window.multiplier),
    label: window.label ?? '',
  })),
});

const parseRate = (value: string): number | null => {
  const trimmed = value.trim();
  if (trimmed === '') return 0;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : null;
};

export type ProviderPriceDraftResult =
  | { price: ProviderModelPrice; error?: undefined }
  | { price?: undefined; error: ProviderPriceDraftError };

export const buildProviderPriceFromDraft = (
  draft: ProviderPriceDraft
): ProviderPriceDraftResult => {
  const provider = draft.provider.trim();
  const model = draft.model.trim();
  if (!provider) return { error: 'provider_required' };
  if (!model) return { error: 'model_required' };

  const prompt = parseRate(draft.prompt);
  const completion = parseRate(draft.completion);
  if (prompt === null || completion === null) return { error: 'rate_invalid' };

  const cacheReadConfigured = draft.cacheRead.trim() !== '';
  const cacheCreationConfigured = draft.cacheCreation.trim() !== '';
  const cacheRead = cacheReadConfigured ? parseRate(draft.cacheRead) : 0;
  const cacheCreation = cacheCreationConfigured ? parseRate(draft.cacheCreation) : 0;
  if (cacheRead === null || cacheCreation === null) return { error: 'rate_invalid' };

  const timezone = draft.timezone.trim();
  if (!timezone) return { error: 'timezone_required' };

  const windows: ProviderPriceWindow[] = [];
  for (const window of draft.windows) {
    const startMinute = parseWindowStartMinute(window.start);
    const endMinute = parseWindowEndMinute(window.end);
    if (startMinute === null || endMinute === null) return { error: 'window_time_invalid' };
    const multiplier = Number(window.multiplier.trim());
    if (!Number.isFinite(multiplier) || multiplier <= 0) {
      return { error: 'window_multiplier_invalid' };
    }
    const label = window.label.trim();
    windows.push({
      ...(window.id !== undefined ? { id: window.id } : {}),
      startMinute,
      endMinute,
      multiplier,
      ...(label ? { label } : {}),
    });
  }

  const note = draft.note.trim();
  return {
    price: {
      ...(draft.id !== undefined ? { id: draft.id } : {}),
      provider,
      model,
      prompt,
      completion,
      cacheRead,
      cacheCreation,
      cacheReadConfigured,
      cacheCreationConfigured,
      timezone,
      ...(note ? { note } : {}),
      windows,
    },
  };
};

/**
 * Returns the full rule list with `price` replacing the entry it edits (`replaceKey`, the
 * key of the rule the editor was opened with) or any rule with the same provider+model;
 * otherwise appends it. The list is what gets PUT to the backend.
 */
export const upsertProviderPrice = (
  prices: ProviderModelPrice[],
  price: ProviderModelPrice,
  replaceKey?: string | null
): ProviderModelPrice[] => {
  const nextKey = providerPriceKey(price.provider, price.model);
  const targetKeys = new Set([nextKey, ...(replaceKey ? [replaceKey] : [])]);
  let replaced = false;
  const next = prices.flatMap((item) => {
    if (!targetKeys.has(providerPriceKey(item.provider, item.model))) return [item];
    if (replaced) return [];
    replaced = true;
    return [price];
  });
  return replaced ? next : [...next, price];
};

export const removeProviderPrice = (
  prices: ProviderModelPrice[],
  target: Pick<ProviderModelPrice, 'provider' | 'model'>
): ProviderModelPrice[] => {
  const targetKey = providerPriceKey(target.provider, target.model);
  return prices.filter((item) => providerPriceKey(item.provider, item.model) !== targetKey);
};

export const groupProviderPrices = (prices: ProviderModelPrice[]): ProviderPriceGroup[] => {
  const groups = new Map<string, ProviderPriceGroup>();
  prices.forEach((price) => {
    const key = normalizeKeyPart(price.provider);
    const group = groups.get(key);
    if (group) {
      group.items.push(price);
      return;
    }
    groups.set(key, { provider: price.provider, items: [price] });
  });
  return Array.from(groups.values())
    .map((group) => ({
      ...group,
      items: [...group.items].sort((left, right) => left.model.localeCompare(right.model)),
    }))
    .sort((left, right) => left.provider.localeCompare(right.provider));
};

export const buildObservedProviders = (observed: ObservedProviderModel[]): string[] => {
  const calls = new Map<string, { provider: string; calls: number }>();
  observed.forEach((item) => {
    const provider = item.provider.trim();
    if (!provider) return;
    const key = normalizeKeyPart(provider);
    const existing = calls.get(key) ?? { provider, calls: 0 };
    existing.calls += Math.max(Number(item.calls) || 0, 0);
    calls.set(key, existing);
  });
  return Array.from(calls.values())
    .sort((left, right) => right.calls - left.calls || left.provider.localeCompare(right.provider))
    .map((item) => item.provider);
};

export const buildObservedModels = (
  observed: ObservedProviderModel[],
  provider: string
): string[] => {
  const providerKey = normalizeKeyPart(provider);
  return observed
    .filter((item) => !providerKey || normalizeKeyPart(item.provider) === providerKey)
    .filter((item) => item.model.trim())
    .sort((left, right) => right.calls - left.calls || left.model.localeCompare(right.model))
    .map((item) => item.model)
    .filter((model, index, list) => list.indexOf(model) === index);
};

export const buildUnpricedObservedModels = (
  observed: ObservedProviderModel[],
  prices: ProviderModelPrice[]
): ObservedProviderModel[] => {
  const priced = new Set(prices.map((price) => providerPriceKey(price.provider, price.model)));
  return observed
    .filter((item) => item.provider.trim() && item.model.trim())
    .filter((item) => !priced.has(providerPriceKey(item.provider, item.model)))
    .sort(
      (left, right) =>
        right.calls - left.calls ||
        right.lastSeenMs - left.lastSeenMs ||
        left.provider.localeCompare(right.provider) ||
        left.model.localeCompare(right.model)
    );
};

/** 0..1 while a reprice is running, otherwise null. */
export const calculateRepriceProgress = (state: RepriceState | null | undefined): number | null => {
  if (!state?.repricing || !(state.repriceTargetEventId > 0)) return null;
  const ratio = state.repricedThroughEventId / state.repriceTargetEventId;
  if (!Number.isFinite(ratio)) return 0;
  return Math.min(1, Math.max(0, ratio));
};

export const formatRepriceProgressPercent = (progress: number): string =>
  `${Math.round(progress * 100)}%`;

/** `YYYY-MM-DD` (local midnight) -> epoch ms; empty or invalid input means all history (0). */
export const parseRepriceFromDate = (value: string): number => {
  const trimmed = value.trim();
  if (!trimmed) return 0;
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(trimmed);
  if (!match) return 0;
  const timestamp = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3])).getTime();
  return Number.isFinite(timestamp) && timestamp > 0 ? timestamp : 0;
};
