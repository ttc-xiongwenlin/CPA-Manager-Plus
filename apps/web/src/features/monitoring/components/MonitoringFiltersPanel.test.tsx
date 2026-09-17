import type { TFunction } from 'i18next';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { describe, expect, it } from 'vitest';
import { Select } from '@/components/ui/Select';
import { MonitoringFiltersPanel } from './MonitoringFiltersPanel';

const t = ((key: string) => key) as unknown as TFunction;
const options = [{ value: 'all', label: 'all' }];
const noop = () => {};

const renderPanel = (showBucketFilter: boolean) => {
  let renderer!: ReactTestRenderer;
  act(() => {
    renderer = create(
      <MonitoringFiltersPanel
        timeRange="today"
        autoRefreshMs="0"
        selectedAccount="all"
        selectedProvider="all"
        selectedModel="all"
        selectedChannel="all"
        selectedBucket="all"
        selectedApiKeyHash="all"
        selectedStatus="all"
        searchInput=""
        accountOptions={options}
        providerOptions={options}
        modelOptions={options}
        channelOptions={options}
        bucketOptions={options}
        apiKeyOptions={options}
        statusOptions={options}
        combinedError={null}
        usageStatisticsEnabled
        overallLoading={false}
        showBucketFilter={showBucketFilter}
        t={t}
        onTimeRangeChange={noop}
        onAutoRefreshChange={noop}
        onRefreshAll={noop}
        onAccountFilterChange={noop}
        onProviderChange={noop}
        onModelChange={noop}
        onChannelChange={noop}
        onBucketChange={noop}
        onApiKeyChange={noop}
        onStatusChange={noop}
        onSearchChange={noop}
        onClearFilters={noop}
      />
    );
  });
  return renderer;
};

const hasBucketSelect = (renderer: ReactTestRenderer) =>
  renderer.root
    .findAllByType(Select)
    .some((node) => node.props.ariaLabel === 'monitoring.filter_bucket');

describe('MonitoringFiltersPanel bucket filter', () => {
  it('is omitted unless showBucketFilter is set', () => {
    expect(hasBucketSelect(renderPanel(false))).toBe(false);
  });

  it('is rendered when showBucketFilter is set', () => {
    expect(hasBucketSelect(renderPanel(true))).toBe(true);
  });
});
