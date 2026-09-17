import { act } from 'react';
import { create, type ReactTestRenderer } from 'react-test-renderer';
import { describe, expect, it, vi } from 'vitest';
import { Select } from '@/components/ui/Select';
import { ErrorInsightPage } from './ErrorInsightPage';
import {
  getDefaultErrorInsightFilters,
  type ErrorInsightFiltersState,
} from './model/errorInsightUiState';

const { mocks } = vi.hoisted(() => ({
  mocks: {
    filters: null as ErrorInsightFiltersState | null,
  },
}));

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: () => {} },
  useTranslation: () => ({
    i18n: { language: 'en' },
    t: (key: string) => key,
  }),
}));

vi.mock('@/stores', () => ({
  useAuthStore: (selector: (state: { managementKey: string }) => unknown) =>
    selector({ managementKey: 'manager-key' }),
  useThemeStore: (selector: (state: { resolvedTheme: string }) => unknown) =>
    selector({ resolvedTheme: 'light' }),
}));

vi.mock('@/hooks/useRequestMonitoringAvailability', () => ({
  useRequestMonitoringAvailability: () => ({
    checking: false,
    available: true,
    managerServiceAvailable: true,
    modelPricesAvailable: true,
    serviceBase: 'http://manager.local:8318',
    reason: '',
  }),
}));

vi.mock('./hooks/useErrorInsight', () => ({
  useErrorInsight: () => ({
    status: 'idle',
    view: null,
    filters: mocks.filters,
    setFilters: vi.fn(),
    clearFilters: vi.fn(),
    refresh: vi.fn(),
    options: {
      models: [],
      providers: ['codex', 'gemini'],
      apiKeys: [],
      authFiles: [],
      buckets: [{ value: 'team-a', label: 'team-a' }],
    },
    apiKeyDisplayMap: new Map(),
  }),
}));

const renderPage = () => {
  let renderer!: ReactTestRenderer;
  act(() => {
    renderer = create(<ErrorInsightPage />);
  });
  return renderer;
};

const findBucketSelect = (renderer: ReactTestRenderer) =>
  renderer.root
    .findAllByType(Select)
    .find((node) => node.props.ariaLabel === 'error_insight.filter_bucket');

describe('ErrorInsightPage bucket filter', () => {
  it('is hidden unless the provider filter is codex', () => {
    mocks.filters = getDefaultErrorInsightFilters();
    expect(findBucketSelect(renderPage())).toBeUndefined();
  });

  it('is shown while the provider filter is codex', () => {
    mocks.filters = { ...getDefaultErrorInsightFilters(), provider: 'codex' };
    expect(findBucketSelect(renderPage())).toBeDefined();
  });
});
