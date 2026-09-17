import { useEffect } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useErrorInsight } from './useErrorInsight';

vi.mock('@/features/monitoring/hooks/useMonitoringAnalytics', () => ({
  useMonitoringAnalytics: () => ({
    enabled: false,
    loading: false,
    error: '',
    data: null,
    dataStale: false,
    lastRefreshedAt: null,
    serviceBase: '',
    unavailableReason: '',
    refresh: vi.fn(async () => undefined),
  }),
}));

vi.mock('@/features/monitoring/hooks/useUsageData', () => ({
  useUsageData: () => ({ apiKeyAliases: [], loadApiKeyAliases: vi.fn(async () => undefined) }),
}));

vi.mock('@/features/monitoring/services/monitoringMetaService', () => ({
  loadMonitoringMetaPayload: () => Promise.resolve({ authFiles: [], channels: [] }),
}));

vi.mock('@/features/authFiles/hooks/useAuthFilesBucketOptions', () => ({
  useAuthFilesBucketOptions: () => [],
}));

vi.mock('@/stores', () => ({
  useConfigStore: (selector: (state: { config: null }) => unknown) => selector({ config: null }),
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe('useErrorInsight filters', () => {
  let renderer: ReactTestRenderer | null = null;
  let latestResult: ReturnType<typeof useErrorInsight> | null = null;

  const Harness = () => {
    const result = useErrorInsight({ serviceBase: '', managementKey: '' });
    useEffect(() => {
      latestResult = result;
    });
    return null;
  };

  afterEach(() => {
    renderer?.unmount();
    renderer = null;
    latestResult = null;
  });

  const renderHook = async () => {
    await act(async () => {
      renderer = create(
        <MemoryRouter initialEntries={['/error-insight']}>
          <Harness />
        </MemoryRouter>
      );
      await Promise.resolve();
    });
  };

  it('drops the bucket filter once the provider filter leaves codex', async () => {
    await renderHook();

    await act(async () => {
      latestResult?.setFilters({ provider: 'codex', bucket: 'team-a' });
    });
    expect(latestResult?.filters.bucket).toBe('team-a');

    await act(async () => {
      latestResult?.setFilters({ provider: 'openai' });
    });
    expect(latestResult?.filters.provider).toBe('openai');
    expect(latestResult?.filters.bucket).toBe('all');
  });
});
