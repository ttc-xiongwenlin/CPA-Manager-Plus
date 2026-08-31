import { ERROR_CLASSES } from '@/services/api/errorInsight';
import { ERROR_INSIGHT_WINDOW_PRESETS } from './errorInsightModel';

export interface ErrorInsightFiltersState {
  windowKey: string; // '1h'|'6h'|'24h'|'3d'|'7d'|'14d', default '24h'
  model: string;
  provider: string;
  apiKeyHash: string;
  bucket: string; // 'all' sentinel
  authFile: string; // 'all' sentinel
  searchQuery: string;
  selectedClass: string; // '' = none selected
}

export const ERROR_INSIGHT_UI_STATE_STORAGE_KEY = 'errorInsight.uiState';

const WINDOW_KEY_SET = new Set<string>(ERROR_INSIGHT_WINDOW_PRESETS.map((preset) => preset.key));
const ERROR_CLASS_SET = new Set<string>(ERROR_CLASSES);
const DEFAULT_WINDOW_KEY = '24h';

const normalizeWindowKey = (value: unknown): string =>
  typeof value === 'string' && WINDOW_KEY_SET.has(value) ? value : DEFAULT_WINDOW_KEY;

const normalizeSelectValue = (value: unknown): string => {
  const normalized = typeof value === 'string' ? value.trim() : '';
  return normalized || 'all';
};

const normalizeInputValue = (value: unknown): string => (typeof value === 'string' ? value : '');

const normalizeSelectedClass = (value: unknown): string =>
  typeof value === 'string' && ERROR_CLASS_SET.has(value) ? value : '';

export function getDefaultErrorInsightFilters(): ErrorInsightFiltersState {
  return {
    windowKey: DEFAULT_WINDOW_KEY,
    model: 'all',
    provider: 'all',
    apiKeyHash: 'all',
    bucket: 'all',
    authFile: 'all',
    searchQuery: '',
    selectedClass: '',
  };
}

export function normalizeErrorInsightFilters(value: unknown): ErrorInsightFiltersState {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return getDefaultErrorInsightFilters();
  }
  const record = value as Record<string, unknown>;
  return {
    windowKey: normalizeWindowKey(record.windowKey),
    model: normalizeSelectValue(record.model),
    provider: normalizeSelectValue(record.provider),
    apiKeyHash: normalizeSelectValue(record.apiKeyHash),
    bucket: normalizeSelectValue(record.bucket),
    authFile: normalizeSelectValue(record.authFile),
    searchQuery: normalizeInputValue(record.searchQuery),
    selectedClass: normalizeSelectedClass(record.selectedClass),
  };
}

export function readErrorInsightUiState(): ErrorInsightFiltersState {
  if (typeof window === 'undefined' || typeof window.localStorage === 'undefined') {
    return getDefaultErrorInsightFilters();
  }

  try {
    const raw = window.localStorage.getItem(ERROR_INSIGHT_UI_STATE_STORAGE_KEY);
    if (raw) {
      return normalizeErrorInsightFilters(JSON.parse(raw));
    }
  } catch {
    // Ignore storage failures and fall back to defaults.
  }

  return getDefaultErrorInsightFilters();
}

export function writeErrorInsightUiState(state: ErrorInsightFiltersState): void {
  if (typeof window === 'undefined' || typeof window.localStorage === 'undefined') return;

  try {
    const next = normalizeErrorInsightFilters(state);
    window.localStorage.setItem(ERROR_INSIGHT_UI_STATE_STORAGE_KEY, JSON.stringify(next));
  } catch {
    // Ignore storage failures and keep the runtime state in memory only.
  }
}

const setNonDefaultParam = (
  params: URLSearchParams,
  key: string,
  value: string,
  defaultValue: string
) => {
  const trimmed = value.trim();
  if (trimmed && trimmed !== defaultValue) {
    params.set(key, trimmed);
  }
};

export function buildErrorInsightSearchParams(state: ErrorInsightFiltersState): URLSearchParams {
  const normalized = normalizeErrorInsightFilters(state);
  const defaults = getDefaultErrorInsightFilters();
  const params = new URLSearchParams();

  if (normalized.windowKey !== defaults.windowKey) {
    params.set('window', normalized.windowKey);
  }
  setNonDefaultParam(params, 'model', normalized.model, defaults.model);
  setNonDefaultParam(params, 'provider', normalized.provider, defaults.provider);
  setNonDefaultParam(params, 'api_key_hash', normalized.apiKeyHash, defaults.apiKeyHash);
  setNonDefaultParam(params, 'bucket', normalized.bucket, defaults.bucket);
  setNonDefaultParam(params, 'auth_file', normalized.authFile, defaults.authFile);
  setNonDefaultParam(params, 'search', normalized.searchQuery, defaults.searchQuery);
  setNonDefaultParam(params, 'class', normalized.selectedClass, defaults.selectedClass);

  return params;
}

export function buildErrorInsightUiStateFromSearchParams(
  params: URLSearchParams,
  fallback: ErrorInsightFiltersState
): ErrorInsightFiltersState {
  // windowKey and selectedClass are closed enums: an illegal param value
  // falls back to `fallback` (not to the hardcoded default), so a bad query
  // string never clobbers filters restored from localStorage.
  const windowParam = params.get('window');
  const windowKey =
    windowParam !== null && WINDOW_KEY_SET.has(windowParam) ? windowParam : fallback.windowKey;

  const classParam = params.get('class');
  const selectedClass =
    classParam !== null && ERROR_CLASS_SET.has(classParam) ? classParam : fallback.selectedClass;

  return {
    windowKey,
    model: params.has('model') ? normalizeSelectValue(params.get('model')) : fallback.model,
    provider: params.has('provider')
      ? normalizeSelectValue(params.get('provider'))
      : fallback.provider,
    apiKeyHash: params.has('api_key_hash')
      ? normalizeSelectValue(params.get('api_key_hash'))
      : fallback.apiKeyHash,
    bucket: params.has('bucket') ? normalizeSelectValue(params.get('bucket')) : fallback.bucket,
    authFile: params.has('auth_file')
      ? normalizeSelectValue(params.get('auth_file'))
      : fallback.authFile,
    searchQuery: params.has('search')
      ? normalizeInputValue(params.get('search'))
      : fallback.searchQuery,
    selectedClass,
  };
}
