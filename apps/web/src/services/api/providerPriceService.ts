import axios from 'axios';
import { isDemoMode } from '@/features/demo/demoMode';
import {
  USAGE_SERVICE_TIMEOUT_MS,
  authHeaders,
  buildUrl,
  withUsageServiceError,
} from './usageService';

export interface ProviderPriceWindow {
  id?: number;
  /** Minutes since local midnight, 0..1439. */
  startMinute: number;
  /** Minutes since local midnight, 1..1440; endMinute <= startMinute wraps past midnight. */
  endMinute: number;
  /** Rate multiplier, > 0 (0.5 = half price, 2 = double). */
  multiplier: number;
  label?: string;
}

export interface ProviderModelPrice {
  id?: number;
  provider: string;
  model: string;
  /** CNY per 1M tokens. */
  prompt: number;
  completion: number;
  cacheRead?: number;
  cacheCreation?: number;
  cacheReadConfigured?: boolean;
  cacheCreationConfigured?: boolean;
  /** IANA timezone, default 'Asia/Shanghai'. */
  timezone: string;
  note?: string;
  windows?: ProviderPriceWindow[];
  updatedAtMs?: number;
}

export interface ProviderPricesResponse {
  prices: ProviderModelPrice[];
}

export interface ObservedProviderModel {
  provider: string;
  model: string;
  calls: number;
  lastSeenMs: number;
}

export interface ObservedProviderModelsResponse {
  items: ObservedProviderModel[];
}

export type RepriceStatus = 'pending' | 'ready' | 'catching_up' | 'repricing' | 'failed';

export interface RepriceState {
  status: RepriceStatus;
  pricedThroughEventId: number;
  latestEventId: number;
  repricing: boolean;
  repriceEpoch: number;
  repriceFromMs: number;
  repriceTargetEventId: number;
  repricedThroughEventId: number;
  processedEvents: number;
  updatedAtMs: number;
  lastError?: string;
}

export const DEFAULT_PROVIDER_PRICE_TIMEZONE = 'Asia/Shanghai';

const PROVIDER_PRICES_PATH = '/v0/management/provider-prices';
const OBSERVED_PROVIDER_MODELS_PATH = '/v0/management/provider-prices/observed';
const REPRICE_PATH = '/v0/management/pricing/reprice';

export const createIdleRepriceState = (): RepriceState => ({
  status: 'ready',
  pricedThroughEventId: 0,
  latestEventId: 0,
  repricing: false,
  repriceEpoch: 0,
  repriceFromMs: 0,
  repriceTargetEventId: 0,
  repricedThroughEventId: 0,
  processedEvents: 0,
  updatedAtMs: 0,
});

export const getProviderPrices = async (
  base: string,
  managementKey?: string,
  signal?: AbortSignal
): Promise<ProviderPricesResponse> => {
  if (__DEMO_SITE__ && isDemoMode()) {
    return { prices: [] };
  }

  return withUsageServiceError(async () => {
    const response = await axios.get<ProviderPricesResponse>(buildUrl(base, PROVIDER_PRICES_PATH), {
      timeout: USAGE_SERVICE_TIMEOUT_MS,
      headers: authHeaders(managementKey),
      signal,
    });
    return { prices: response.data?.prices ?? [] };
  });
};

export const saveProviderPrices = async (
  base: string,
  prices: ProviderModelPrice[],
  managementKey?: string
): Promise<ProviderPricesResponse> => {
  if (__DEMO_SITE__ && isDemoMode()) {
    return { prices };
  }

  return withUsageServiceError(async () => {
    const response = await axios.put<ProviderPricesResponse>(
      buildUrl(base, PROVIDER_PRICES_PATH),
      { prices },
      {
        timeout: USAGE_SERVICE_TIMEOUT_MS,
        headers: authHeaders(managementKey),
      }
    );
    return { prices: response.data?.prices ?? [] };
  });
};

export const getObservedProviderModels = async (
  base: string,
  managementKey?: string,
  signal?: AbortSignal
): Promise<ObservedProviderModelsResponse> => {
  if (__DEMO_SITE__ && isDemoMode()) {
    return { items: [] };
  }

  return withUsageServiceError(async () => {
    const response = await axios.get<ObservedProviderModelsResponse>(
      buildUrl(base, OBSERVED_PROVIDER_MODELS_PATH),
      {
        timeout: USAGE_SERVICE_TIMEOUT_MS,
        headers: authHeaders(managementKey),
        signal,
      }
    );
    return { items: response.data?.items ?? [] };
  });
};

export const getRepriceState = async (
  base: string,
  managementKey?: string,
  signal?: AbortSignal
): Promise<RepriceState> => {
  if (__DEMO_SITE__ && isDemoMode()) {
    return createIdleRepriceState();
  }

  return withUsageServiceError(async () => {
    const response = await axios.get<RepriceState>(buildUrl(base, REPRICE_PATH), {
      timeout: USAGE_SERVICE_TIMEOUT_MS,
      headers: authHeaders(managementKey),
      signal,
    });
    return response.data;
  });
};

export const startReprice = async (
  base: string,
  fromMs: number,
  managementKey?: string
): Promise<RepriceState> => {
  if (__DEMO_SITE__ && isDemoMode()) {
    return createIdleRepriceState();
  }

  return withUsageServiceError(async () => {
    const response = await axios.post<RepriceState>(
      buildUrl(base, REPRICE_PATH),
      { fromMs },
      {
        timeout: USAGE_SERVICE_TIMEOUT_MS,
        headers: authHeaders(managementKey),
      }
    );
    return response.data;
  });
};
