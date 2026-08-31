import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  ERROR_INSIGHT_UI_STATE_STORAGE_KEY,
  buildErrorInsightSearchParams,
  buildErrorInsightUiStateFromSearchParams,
  getDefaultErrorInsightFilters,
  readErrorInsightUiState,
  writeErrorInsightUiState,
} from './errorInsightUiState';

type StorageLike = {
  getItem: (key: string) => string | null;
  setItem: (key: string, value: string) => void;
  removeItem: (key: string) => void;
  clear: () => void;
};

const createMemoryStorage = (): StorageLike => {
  const store = new Map<string, string>();
  return {
    getItem: (key) => (store.has(key) ? (store.get(key) as string) : null),
    setItem: (key, value) => {
      store.set(key, value);
    },
    removeItem: (key) => {
      store.delete(key);
    },
    clear: () => {
      store.clear();
    },
  };
};

const originalWindow = (globalThis as { window?: unknown }).window;

describe('errorInsightUiState', () => {
  let storage: StorageLike;

  beforeEach(() => {
    storage = createMemoryStorage();
    (globalThis as { window?: unknown }).window = { localStorage: storage };
  });

  afterEach(() => {
    if (originalWindow === undefined) {
      delete (globalThis as { window?: unknown }).window;
    } else {
      (globalThis as { window?: unknown }).window = originalWindow;
    }
  });

  it('uses the documented defaults when storage is empty', () => {
    expect(getDefaultErrorInsightFilters()).toEqual({
      windowKey: '24h',
      model: 'all',
      provider: 'all',
      apiKeyHash: 'all',
      bucket: 'all',
      authFile: 'all',
      searchQuery: '',
      selectedClass: '',
    });
    expect(readErrorInsightUiState()).toEqual(getDefaultErrorInsightFilters());
  });

  it('persists and reads filters via localStorage', () => {
    writeErrorInsightUiState({
      windowKey: '7d',
      model: 'gpt-4o',
      provider: 'openai',
      apiKeyHash: 'abc123',
      bucket: 'bucket-1',
      authFile: 'auth.json',
      searchQuery: 'req-42',
      selectedClass: 'timeout',
    });

    expect(JSON.parse(storage.getItem(ERROR_INSIGHT_UI_STATE_STORAGE_KEY) ?? '{}')).toEqual({
      windowKey: '7d',
      model: 'gpt-4o',
      provider: 'openai',
      apiKeyHash: 'abc123',
      bucket: 'bucket-1',
      authFile: 'auth.json',
      searchQuery: 'req-42',
      selectedClass: 'timeout',
    });
    expect(readErrorInsightUiState()).toEqual({
      windowKey: '7d',
      model: 'gpt-4o',
      provider: 'openai',
      apiKeyHash: 'abc123',
      bucket: 'bucket-1',
      authFile: 'auth.json',
      searchQuery: 'req-42',
      selectedClass: 'timeout',
    });
  });

  it('falls back to defaults when the stored payload is invalid JSON', () => {
    storage.setItem(ERROR_INSIGHT_UI_STATE_STORAGE_KEY, '{not json');
    expect(readErrorInsightUiState()).toEqual(getDefaultErrorInsightFilters());
  });

  it('falls back to defaults when the stored payload has illegal field values', () => {
    storage.setItem(
      ERROR_INSIGHT_UI_STATE_STORAGE_KEY,
      JSON.stringify({
        windowKey: 'nonsense',
        model: '   ',
        provider: '',
        apiKeyHash: 123,
        bucket: 'all',
        authFile: 'all',
        searchQuery: 42,
        selectedClass: 'not_a_real_class',
      })
    );
    expect(readErrorInsightUiState()).toEqual(getDefaultErrorInsightFilters());
  });

  it('does not write default values into search params', () => {
    const params = buildErrorInsightSearchParams(getDefaultErrorInsightFilters());
    expect([...params.keys()]).toEqual([]);
  });

  it('serializes non-default state into the documented query parameter names', () => {
    const params = buildErrorInsightSearchParams({
      windowKey: '6h',
      model: 'gpt-4o',
      provider: 'openai',
      apiKeyHash: 'abc123',
      bucket: 'bucket-1',
      authFile: 'auth.json',
      searchQuery: 'req-42',
      selectedClass: 'timeout',
    });

    expect(params.get('window')).toBe('6h');
    expect(params.get('model')).toBe('gpt-4o');
    expect(params.get('provider')).toBe('openai');
    expect(params.get('api_key_hash')).toBe('abc123');
    expect(params.get('bucket')).toBe('bucket-1');
    expect(params.get('auth_file')).toBe('auth.json');
    expect(params.get('search')).toBe('req-42');
    expect(params.get('class')).toBe('timeout');
  });

  it('builds ui state from search params, falling back to the given fallback for missing keys', () => {
    const fallback = { ...getDefaultErrorInsightFilters(), model: 'fallback-model' };
    const state = buildErrorInsightUiStateFromSearchParams(
      new URLSearchParams('window=3d&provider=openai&api_key_hash=abc&class=auth'),
      fallback
    );

    expect(state).toEqual({
      windowKey: '3d',
      model: 'fallback-model',
      provider: 'openai',
      apiKeyHash: 'abc',
      bucket: 'all',
      authFile: 'all',
      searchQuery: '',
      selectedClass: 'auth',
    });
  });

  it('falls back to the (non-default) fallback state for illegal query parameter values', () => {
    // Use a non-default fallback so this distinguishes "falls back to
    // `fallback`" from "falls back to the hardcoded default" - both happen
    // to be '24h'/'' for the default fallback, which wouldn't prove it.
    const fallback = { ...getDefaultErrorInsightFilters(), windowKey: '6h', selectedClass: 'auth' };
    const state = buildErrorInsightUiStateFromSearchParams(
      new URLSearchParams('window=not_a_window&class=not_a_class'),
      fallback
    );

    expect(state.windowKey).toBe('6h');
    expect(state.selectedClass).toBe('auth');
  });
});
