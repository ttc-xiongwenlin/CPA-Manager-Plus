import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import type { SelectOption } from '@/components/ui/Select';
import { UNTAGGED_BUCKET_FILTER } from '@/features/authFiles/bucketOptions';
import { useAuthFilesBucketOptions } from '@/features/authFiles/hooks/useAuthFilesBucketOptions';
import { useMonitoringAnalytics } from '@/features/monitoring/hooks/useMonitoringAnalytics';
import { buildMonitoringAuthMetaMap } from '@/features/monitoring/model/authMeta';
import { buildMonitoringFilterSelectorsInclude } from '@/features/monitoring/model/monitoringAnalyticsModel';
import type { MonitoringAuthMeta } from '@/features/monitoring/model/types';
import { loadMonitoringMetaPayload } from '@/features/monitoring/services/monitoringMetaService';
import { errorInsightApi, type ErrorInsightFilters } from '@/services/api/errorInsight';
import { useConfigStore } from '@/stores';
import type { AuthFileItem } from '@/types/authFile';
import {
  buildErrorInsightView,
  ERROR_INSIGHT_WINDOW_PRESETS,
  type ErrorInsightView,
} from '../model/errorInsightModel';
import {
  buildErrorInsightSearchParams,
  buildErrorInsightUiStateFromSearchParams,
  getDefaultErrorInsightFilters,
  readErrorInsightUiState,
  writeErrorInsightUiState,
  type ErrorInsightFiltersState,
} from '../model/errorInsightUiState';

export type ErrorInsightStatus = 'idle' | 'loading' | 'ready' | 'error';

interface UseErrorInsightOptions {
  serviceBase: string;
  managementKey?: string;
}

export interface ErrorInsightSelectorOptions {
  models: string[];
  providers: string[];
  apiKeys: string[];
  authFiles: string[];
  buckets: SelectOption[];
}

const SEARCH_DEBOUNCE_MS = 350;

const WINDOW_MS_BY_KEY = new Map<string, number>(
  ERROR_INSIGHT_WINDOW_PRESETS.map((preset) => [preset.key, preset.ms])
);
const DEFAULT_WINDOW_MS = ERROR_INSIGHT_WINDOW_PRESETS[2].ms; // '24h'

const resolveWindowMs = (windowKey: string): number =>
  WINDOW_MS_BY_KEY.get(windowKey) ?? DEFAULT_WINDOW_MS;

// usageAnalyticsModel.ts:600-610 keeps isActiveSelectValue/normalizeLowerSelectValue
// module-private, so they're reimplemented here rather than imported.
const isActiveSelectValue = (value: string): boolean => {
  const trimmed = value.trim();
  return trimmed !== '' && trimmed !== 'all';
};
const normalizeLowerSelectValue = (value: string): string => value.trim().toLowerCase();

const NO_MATCHING_AUTH_INDEX = '__no_matching_auth_index__';

function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debouncedValue, setDebouncedValue] = useState(value);

  useEffect(() => {
    const timer = setTimeout(() => setDebouncedValue(value), delayMs);
    return () => clearTimeout(timer);
  }, [delayMs, value]);

  return debouncedValue;
}

// Adapted from usageAnalyticsModel.ts:706-772 buildUsageAnalyticsFilters: same
// 'all' sentinel stripping and bucket -> auth_indices/bucket_scope resolution,
// narrowed to the fields error-insight filters carry (no status/latency/cache).
function buildErrorInsightRequestFilters(
  filters: Pick<ErrorInsightFiltersState, 'model' | 'provider' | 'apiKeyHash' | 'bucket' | 'authFile'>,
  authMetaMap: Map<string, MonitoringAuthMeta>
): ErrorInsightFilters {
  const payload: ErrorInsightFilters = {};
  if (isActiveSelectValue(filters.model)) {
    payload.models = [filters.model.trim()];
  }
  if (isActiveSelectValue(filters.apiKeyHash)) {
    payload.api_key_hashes = [normalizeLowerSelectValue(filters.apiKeyHash)];
  }
  if (isActiveSelectValue(filters.provider)) {
    payload.providers = [normalizeLowerSelectValue(filters.provider)];
  }
  if (isActiveSelectValue(filters.authFile)) {
    payload.auth_files = [filters.authFile.trim()];
  }
  if (isActiveSelectValue(filters.bucket)) {
    const trimmed = filters.bucket.trim();
    const untagged = trimmed === UNTAGGED_BUCKET_FILTER;
    // Case-sensitive, matching CPA's routing pools — see usageAnalyticsModel.ts:756.
    const bucketAuthIndices = Array.from(authMetaMap.entries())
      .filter(([, meta]) => {
        const value = (meta.bucket || '').trim();
        return untagged ? value === '' : value === trimmed;
      })
      .map(([authIndex]) => authIndex);
    payload.auth_indices =
      bucketAuthIndices.length > 0 ? bucketAuthIndices.sort() : [NO_MATCHING_AUTH_INDEX];
    payload.bucket_scope = true;
  }
  return payload;
}

export function useErrorInsight({ serviceBase, managementKey }: UseErrorInsightOptions) {
  const config = useConfigStore((state) => state.config);
  const [searchParams, setSearchParams] = useSearchParams();
  const [filters, setFiltersState] = useState<ErrorInsightFiltersState>(() =>
    buildErrorInsightUiStateFromSearchParams(searchParams, readErrorInsightUiState())
  );
  const [authFiles, setAuthFiles] = useState<AuthFileItem[]>([]);
  const [status, setStatus] = useState<ErrorInsightStatus>('idle');
  const [view, setView] = useState<ErrorInsightView | null>(null);
  const [generation, setGeneration] = useState(0);
  // Anchor for the filter-selector query (see below); only moves forward on
  // an explicit refresh() or a window-preset change, never on render.
  const [selectorsToMs, setSelectorsToMs] = useState(() => Date.now());
  const controllerRef = useRef<AbortController | null>(null);

  const loadMonitoringMeta = useCallback(async () => {
    try {
      const payload = await loadMonitoringMetaPayload(config);
      setAuthFiles(payload.authFiles);
    } catch {
      setAuthFiles([]);
    }
  }, [config]);

  useEffect(() => {
    let cancelled = false;
    loadMonitoringMetaPayload(config)
      .then((payload) => {
        if (cancelled) return;
        setAuthFiles(payload.authFiles);
      })
      .catch(() => {
        if (!cancelled) setAuthFiles([]);
      });
    return () => {
      cancelled = true;
    };
  }, [config]);

  const authMetaMap = useMemo(() => buildMonitoringAuthMetaMap(authFiles), [authFiles]);
  const bucketOptionNames = useAuthFilesBucketOptions(authFiles);

  const debouncedSearchQuery = useDebouncedValue(filters.searchQuery.trim(), SEARCH_DEBOUNCE_MS);
  const windowMs = useMemo(() => resolveWindowMs(filters.windowKey), [filters.windowKey]);

  const requestFilters = useMemo(
    () =>
      buildErrorInsightRequestFilters(
        {
          model: filters.model,
          provider: filters.provider,
          apiKeyHash: filters.apiKeyHash,
          bucket: filters.bucket,
          authFile: filters.authFile,
        },
        authMetaMap
      ),
    [filters.model, filters.provider, filters.apiKeyHash, filters.bucket, filters.authFile, authMetaMap]
  );

  const refresh = useCallback(() => {
    setGeneration((value) => value + 1);
    setSelectorsToMs(Date.now());
    void loadMonitoringMeta();
  }, [loadMonitoringMeta]);

  const setFilters = useCallback((patch: Partial<ErrorInsightFiltersState>) => {
    setFiltersState((current) => {
      const next = { ...current, ...patch };
      writeErrorInsightUiState(next);
      return next;
    });
    // windowKey drives the selector query's time bounds too — refresh its
    // anchor here rather than in a synchronizing effect (React discourages
    // calling setState from an effect body for values that only need to
    // change in response to a user action).
    if (patch.windowKey !== undefined) {
      setSelectorsToMs(Date.now());
    }
  }, []);

  const clearFilters = useCallback(() => {
    const defaults = getDefaultErrorInsightFilters();
    writeErrorInsightUiState(defaults);
    setFiltersState(defaults);
    setSelectorsToMs(Date.now());
  }, []);

  useEffect(() => {
    writeErrorInsightUiState(filters);
    const nextParams = buildErrorInsightSearchParams(filters);
    if (nextParams.toString() !== searchParams.toString()) {
      setSearchParams(nextParams, { replace: true });
    }
  }, [filters, searchParams, setSearchParams]);

  // Main error-insight data fetch: v1's abort/refetch pattern (cleanup abort +
  // aborted guard before every setState), with the generation counter now
  // joined by the resolved filters/search so any filter change also refetches.
  useEffect(() => {
    if (!serviceBase) {
      // Reset synchronously so an unavailable service doesn't leave a stale
      // "loading"/previous view on screen; matches the DropdownMenu.tsx
      // precedent for state resets that must happen before the next paint.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setStatus('idle');
      setView(null);
      return;
    }
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    setStatus('loading');
    const toMs = Date.now();
    const fromMs = toMs - windowMs;
    void (async () => {
      try {
        const response = await errorInsightApi.fetch(
          serviceBase,
          managementKey,
          {
            from_ms: fromMs,
            to_ms: toMs,
            ...(debouncedSearchQuery ? { search_query: debouncedSearchQuery } : {}),
            ...(Object.keys(requestFilters).length > 0 ? { filters: requestFilters } : {}),
          },
          controller.signal
        );
        if (controller.signal.aborted) return;
        setView(buildErrorInsightView(response, { fromMs, toMs }));
        setStatus('ready');
      } catch {
        if (controller.signal.aborted) return;
        setStatus('error');
      }
    })();
    return () => controller.abort();
  }, [serviceBase, managementKey, windowMs, debouncedSearchQuery, requestFilters, generation]);

  // Filter-selector candidates (models/providers/api keys/auth files) via a
  // dedicated useMonitoringAnalytics instance — useUsageAnalytics.ts:294-311.
  // Scoped only by window + search (not by the other filter selections, and
  // not by requestFilters) so picking a value out of one dropdown never
  // narrows what the other dropdowns still offer.
  const filterSelectorsInclude = useMemo(() => buildMonitoringFilterSelectorsInclude(), []);
  const selectorsFromMs = selectorsToMs - windowMs;
  const filterSelectorsDataScopeKey = useMemo(
    () => JSON.stringify({ selectorsFromMs, selectorsToMs, searchQuery: debouncedSearchQuery }),
    [selectorsFromMs, selectorsToMs, debouncedSearchQuery]
  );
  const filterSelectorsAnalytics = useMonitoringAnalytics({
    fromMs: selectorsFromMs,
    toMs: selectorsToMs,
    dataScopeKey: filterSelectorsDataScopeKey,
    searchQuery: debouncedSearchQuery,
    include: filterSelectorsInclude,
    throttleMs: 0,
  });
  const filterSelectorsData = filterSelectorsAnalytics.dataStale
    ? null
    : filterSelectorsAnalytics.data;

  const options = useMemo<ErrorInsightSelectorOptions>(
    () => ({
      models: filterSelectorsData?.filter_options?.models ?? [],
      providers: filterSelectorsData?.filter_options?.providers ?? [],
      apiKeys: filterSelectorsData?.filter_options?.api_key_hashes ?? [],
      authFiles: filterSelectorsData?.filter_options?.auth_files ?? [],
      buckets: bucketOptionNames.map((name) => ({ value: name, label: name })),
    }),
    [filterSelectorsData, bucketOptionNames]
  );

  return useMemo(
    () => ({ status, view, filters, setFilters, clearFilters, refresh, options }),
    [status, view, filters, setFilters, clearFilters, refresh, options]
  );
}
