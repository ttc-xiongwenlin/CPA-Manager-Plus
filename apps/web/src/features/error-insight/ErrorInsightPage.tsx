import { useCallback, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { ECElementEvent, EChartsCoreOption } from 'echarts/core';
import { EChartsView } from '@/components/charts/EChartsView';
import { Button } from '@/components/ui/Button';
import { Select, type SelectOption } from '@/components/ui/Select';
import {
  IconBan,
  IconChartLine,
  IconRefreshCw,
  IconSatellite,
  IconSearch,
  IconShield,
  IconTriangleAlert,
  IconX,
} from '@/components/ui/icons';
import { useRequestMonitoringAvailability } from '@/hooks/useRequestMonitoringAvailability';
import { useAuthStore } from '@/stores';
import { useErrorInsight } from './hooks/useErrorInsight';
import {
  ERROR_CLASS_COLORS,
  ERROR_INSIGHT_WINDOW_PRESETS,
  foldClass,
  type ErrorClassShare,
  type ErrorInsightBreakdownView,
  type ErrorInsightView,
} from './model/errorInsightModel';
import {
  escapeHtml,
  getTooltipOption,
  tooltipHtml,
  tooltipRowHtml,
  useErrorChartTheme,
  type ErrorChartTheme,
} from './model/chartTheme';
import styles from './ErrorInsightPage.module.scss';

type TFn = ReturnType<typeof useTranslation>['t'];

// Local, not imported from usage-analytics (see chartTheme.ts's own note on
// why this feature avoids coupling to that churn-prone module).
const formatLocalDateTime = (timestampMs: number, locale: string) =>
  new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(timestampMs));

const formatLocalBucketLabel = (bucketMs: number, locale: string) =>
  new Intl.DateTimeFormat(locale, {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
  }).format(new Date(bucketMs));

const formatPercent = (value: number) => `${(value * 100).toFixed(1)}%`;

const maskApiKeyHash = (hash: string) => (hash.length > 8 ? `${hash.slice(0, 8)}…` : hash);

const classLabel = (t: TFn, cls: string) => t(`error_insight.class.${cls}`, cls);

function buildDonutOption(
  chartTheme: ErrorChartTheme,
  shares: ErrorClassShare[],
  selectedClass: string,
  t: TFn
): EChartsCoreOption {
  return {
    animationDuration: 260,
    backgroundColor: 'transparent',
    tooltip: {
      appendToBody: true,
      trigger: 'item',
      ...getTooltipOption(chartTheme),
      borderRadius: 10,
      borderWidth: 1,
      className: styles.echartsTooltipWrapper,
      confine: true,
      padding: 0,
      formatter: (params: unknown) => {
        const p = params as { name?: string; value?: number; percent?: number; marker?: string };
        const percentText = typeof p.percent === 'number' ? p.percent.toFixed(1) : '0';
        const row = tooltipRowHtml(
          chartTheme,
          styles.echartsTooltipRow,
          `${p.marker ?? ''}${escapeHtml(p.name)}`,
          `${escapeHtml(p.value ?? 0)} · ${percentText}%`
        );
        return tooltipHtml(chartTheme, styles.echartsTooltip, row, null);
      },
    },
    legend: {
      type: 'scroll',
      orient: 'vertical',
      right: 4,
      top: 'middle',
      icon: 'circle',
      itemWidth: 8,
      itemHeight: 8,
      textStyle: { color: chartTheme.surface.axisLabel, fontSize: 12, fontWeight: 700 },
    },
    series: [
      {
        type: 'pie',
        radius: ['52%', '78%'],
        center: ['38%', '50%'],
        avoidLabelOverlap: false,
        label: { show: false },
        labelLine: { show: false },
        itemStyle: {
          borderColor: chartTheme.surface.pieBorder,
          borderWidth: 2,
        },
        data: shares.map((share) => ({
          name: classLabel(t, share.class),
          value: share.count,
          itemStyle: {
            color: ERROR_CLASS_COLORS[share.class],
            opacity: selectedClass && selectedClass !== share.class ? 0.35 : 1,
          },
        })),
      },
    ],
  };
}

function buildTimelineOption(
  chartTheme: ErrorChartTheme,
  view: ErrorInsightView,
  selectedClass: string,
  locale: string,
  t: TFn
): EChartsCoreOption {
  const buckets = view.timelineBuckets;
  const labels = buckets.map((bucket) => formatLocalBucketLabel(bucket, locale));
  const seriesSource = selectedClass
    ? view.timelineSeries.filter((series) => series.class === selectedClass)
    : view.timelineSeries;

  return {
    animationDuration: 260,
    backgroundColor: 'transparent',
    dataZoom:
      buckets.length > 12
        ? [
            {
              type: 'inside',
              xAxisIndex: 0,
              filterMode: 'none',
              minSpan: Math.min(100, Math.max(10, (12 / buckets.length) * 100)),
              zoomOnMouseWheel: true,
              moveOnMouseMove: true,
              moveOnMouseWheel: false,
            },
          ]
        : [],
    grid: { left: 8, right: 16, top: 24, bottom: 40, containLabel: true },
    legend: {
      bottom: 0,
      icon: 'circle',
      itemWidth: 8,
      itemHeight: 8,
      textStyle: { color: chartTheme.surface.axisLabel, fontSize: 12, fontWeight: 700 },
    },
    tooltip: {
      appendToBody: true,
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      ...getTooltipOption(chartTheme),
      borderRadius: 10,
      borderWidth: 1,
      className: styles.echartsTooltipWrapper,
      confine: true,
      padding: 0,
      formatter: (params: unknown) => {
        const items = Array.isArray(params) ? params : [params];
        const first = items[0] as { dataIndex?: number } | undefined;
        const bucketMs =
          typeof first?.dataIndex === 'number' ? buckets[first.dataIndex] : undefined;
        const rows = items
          .map((item) => {
            const entry = item as { marker?: string; seriesName?: string; data?: number };
            return tooltipRowHtml(
              chartTheme,
              styles.echartsTooltipRow,
              `${entry.marker ?? ''}${escapeHtml(entry.seriesName)}`,
              escapeHtml(entry.data ?? 0)
            );
          })
          .join('');
        return tooltipHtml(
          chartTheme,
          styles.echartsTooltip,
          rows,
          bucketMs !== undefined ? escapeHtml(formatLocalDateTime(bucketMs, locale)) : null
        );
      },
    },
    xAxis: {
      type: 'category',
      data: labels,
      axisLabel: {
        color: chartTheme.surface.axisLabel,
        fontSize: 11,
        fontWeight: 700,
        hideOverlap: true,
      },
      axisLine: { lineStyle: { color: chartTheme.surface.axisLine } },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: chartTheme.surface.axisLabel },
      splitLine: { lineStyle: { color: chartTheme.surface.splitLine, type: 'dashed' } },
    },
    series: seriesSource.map((series) => ({
      type: 'bar',
      stack: 'errors',
      name: classLabel(t, series.class),
      data: series.data,
      barMaxWidth: 28,
      itemStyle: { color: ERROR_CLASS_COLORS[series.class] },
    })),
  };
}

function buildBreakdownOption(
  chartTheme: ErrorChartTheme,
  breakdown: ErrorInsightBreakdownView,
  t: TFn
): EChartsCoreOption {
  // Reversed so the biggest key lands last in the array -> rendered at the
  // top of the category axis (echarts draws category index 0 at the bottom).
  const orderedIndices = breakdown.keys.map((_, index) => index).reverse();
  const categories = orderedIndices.map((index) => breakdown.keys[index]);

  return {
    animationDuration: 260,
    backgroundColor: 'transparent',
    grid: { left: 8, right: 24, top: 8, bottom: 8, containLabel: true },
    legend: {
      bottom: 0,
      icon: 'circle',
      itemWidth: 8,
      itemHeight: 8,
      textStyle: { color: chartTheme.surface.axisLabel, fontSize: 12, fontWeight: 700 },
    },
    tooltip: {
      appendToBody: true,
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      ...getTooltipOption(chartTheme),
      borderRadius: 10,
      borderWidth: 1,
      className: styles.echartsTooltipWrapper,
      confine: true,
      padding: 0,
      formatter: (params: unknown) => {
        const items = Array.isArray(params) ? params : [params];
        const first = items[0] as { name?: string } | undefined;
        const rows = items
          .map((item) => {
            const entry = item as { marker?: string; seriesName?: string; data?: number };
            return tooltipRowHtml(
              chartTheme,
              styles.echartsTooltipRow,
              `${entry.marker ?? ''}${escapeHtml(entry.seriesName)}`,
              escapeHtml(entry.data ?? 0)
            );
          })
          .join('');
        return tooltipHtml(chartTheme, styles.echartsTooltip, rows, escapeHtml(first?.name));
      },
    },
    xAxis: {
      type: 'value',
      axisLabel: { color: chartTheme.surface.axisLabel },
      splitLine: { lineStyle: { color: chartTheme.surface.splitLine, type: 'dashed' } },
    },
    yAxis: {
      type: 'category',
      data: categories,
      axisLabel: { color: chartTheme.surface.axisLabel, fontSize: 11, fontWeight: 700 },
      axisLine: { lineStyle: { color: chartTheme.surface.axisLine } },
      axisTick: { show: false },
    },
    series: breakdown.series.map((series) => ({
      type: 'bar',
      stack: 'errors',
      name: classLabel(t, series.class),
      data: orderedIndices.map((index) => series.data[index]),
      barMaxWidth: 22,
      itemStyle: { color: ERROR_CLASS_COLORS[series.class] },
    })),
  };
}

export function ErrorInsightPage() {
  const { t, i18n } = useTranslation();
  const locale = i18n.language;
  const managementKey = useAuthStore((state) => state.managementKey);
  const availability = useRequestMonitoringAvailability();
  const chartTheme = useErrorChartTheme();
  const { status, view, filters, setFilters, clearFilters, refresh, options } = useErrorInsight({
    serviceBase: availability.serviceBase,
    managementKey,
  });
  const [advancedOpen, setAdvancedOpen] = useState(false);

  const allOption = useMemo<SelectOption>(
    () => ({ value: 'all', label: t('error_insight.filter_all', 'All') }),
    [t]
  );
  const modelOptions = useMemo<SelectOption[]>(
    () => [allOption, ...options.models.map((name) => ({ value: name, label: name }))],
    [allOption, options.models]
  );
  const providerOptions = useMemo<SelectOption[]>(
    () => [allOption, ...options.providers.map((name) => ({ value: name, label: name }))],
    [allOption, options.providers]
  );
  const apiKeyOptions = useMemo<SelectOption[]>(
    () => [
      allOption,
      ...options.apiKeys.map((hash) => ({ value: hash, label: maskApiKeyHash(hash) })),
    ],
    [allOption, options.apiKeys]
  );
  const bucketOptions = useMemo<SelectOption[]>(
    () => [allOption, ...options.buckets],
    [allOption, options.buckets]
  );
  const authFileOptions = useMemo<SelectOption[]>(
    () => [allOption, ...options.authFiles.map((name) => ({ value: name, label: name }))],
    [allOption, options.authFiles]
  );

  const donutOption = useMemo<EChartsCoreOption | null>(
    () =>
      view && view.donutData.length > 0
        ? buildDonutOption(chartTheme, view.donutData, filters.selectedClass, t)
        : null,
    [chartTheme, view, filters.selectedClass, t]
  );
  const timelineOption = useMemo<EChartsCoreOption | null>(
    () =>
      view && view.totalFailures > 0
        ? buildTimelineOption(chartTheme, view, filters.selectedClass, locale, t)
        : null,
    [chartTheme, view, filters.selectedClass, locale, t]
  );
  const byProviderOption = useMemo<EChartsCoreOption | null>(
    () =>
      view && view.byProvider.keys.length > 0
        ? buildBreakdownOption(chartTheme, view.byProvider, t)
        : null,
    [chartTheme, view, t]
  );
  const byModelOption = useMemo<EChartsCoreOption | null>(
    () =>
      view && view.byModel.keys.length > 0
        ? buildBreakdownOption(chartTheme, view.byModel, t)
        : null,
    [chartTheme, view, t]
  );

  const handleDonutClick = useCallback(
    (event: ECElementEvent) => {
      if (!view || event.componentType !== 'series' || event.seriesType !== 'pie') return;
      const share = view.donutData[event.dataIndex];
      if (!share) return;
      setFilters({ selectedClass: filters.selectedClass === share.class ? '' : share.class });
    },
    [view, filters.selectedClass, setFilters]
  );

  const filteredRecent = useMemo(() => {
    if (!view) return [];
    if (!filters.selectedClass) return view.recent;
    return view.recent.filter((item) => foldClass(item.class) === filters.selectedClass);
  }, [view, filters.selectedClass]);

  const selectedClassLabel = filters.selectedClass ? classLabel(t, filters.selectedClass) : '';
  const clearFiltersLabel = t('error_insight.clear_filters', 'Clear filters');

  return (
    <div className={styles.page}>
      <section className={styles.controlsPanel}>
        <div className={styles.controlBar}>
          <div className={styles.segmentedControl}>
            {ERROR_INSIGHT_WINDOW_PRESETS.map((preset) => (
              <button
                key={preset.key}
                type="button"
                className={`${styles.segmentButton} ${
                  filters.windowKey === preset.key ? styles.segmentButtonActive : ''
                }`}
                onClick={() => setFilters({ windowKey: preset.key })}
              >
                {t(`error_insight.window.${preset.key}`)}
              </button>
            ))}
          </div>
          <div className={styles.refreshControls}>
            <button type="button" className={styles.filterActionButton} onClick={clearFilters}>
              {clearFiltersLabel}
            </button>
            <Button
              variant="secondary"
              size="sm"
              onClick={refresh}
              disabled={status === 'loading'}
            >
              <IconRefreshCw size={15} />
              {t('error_insight.refresh')}
            </Button>
          </div>
        </div>

        <div className={styles.filterBar}>
          <div className={styles.scopeSearchBar}>
            <IconSearch size={16} />
            <input
              value={filters.searchQuery}
              onChange={(event) => setFilters({ searchQuery: event.target.value })}
              placeholder={t('error_insight.search_placeholder', 'Search account / model / key…')}
              aria-label={t(
                'error_insight.search_placeholder',
                'Search account / model / key…'
              )}
            />
            {filters.searchQuery.trim() ? (
              <button
                type="button"
                className={styles.scopeSearchClear}
                onClick={() => setFilters({ searchQuery: '' })}
                aria-label={clearFiltersLabel}
              >
                <IconX size={14} />
              </button>
            ) : null}
          </div>
          <div className={styles.filterGrid}>
            <Select
              value={filters.model}
              options={modelOptions}
              onChange={(model) => setFilters({ model })}
              ariaLabel={t('error_insight.filter_model', 'Model')}
              triggerClassName={styles.filterSelectTrigger}
            />
            <Select
              value={filters.provider}
              options={providerOptions}
              onChange={(provider) => setFilters({ provider })}
              ariaLabel={t('error_insight.filter_provider', 'Provider')}
              triggerClassName={styles.filterSelectTrigger}
            />
            <Select
              value={filters.apiKeyHash}
              options={apiKeyOptions}
              onChange={(apiKeyHash) => setFilters({ apiKeyHash })}
              ariaLabel={t('error_insight.filter_api_key', 'API Key')}
              triggerClassName={styles.filterSelectTrigger}
            />
            <Select
              value={filters.bucket}
              options={bucketOptions}
              onChange={(bucket) => setFilters({ bucket })}
              ariaLabel={t('error_insight.filter_bucket', 'Bucket')}
              triggerClassName={styles.filterSelectTrigger}
            />
          </div>
        </div>

        <div>
          <button
            type="button"
            className={`${styles.filterActionButton} ${
              advancedOpen ? styles.filterActionButtonActive : ''
            }`}
            onClick={() => setAdvancedOpen((open) => !open)}
          >
            {t('error_insight.advanced', 'Advanced')}
          </button>
        </div>

        {advancedOpen ? (
          <div className={styles.advancedPanel}>
            <div className={styles.advancedGrid}>
              <label className={styles.filterGroup}>
                <Select
                  value={filters.authFile}
                  options={authFileOptions}
                  onChange={(authFile) => setFilters({ authFile })}
                  ariaLabel={t('error_insight.filter_auth_file', 'Auth file')}
                />
              </label>
            </div>
          </div>
        ) : null}

        {filters.selectedClass ? (
          <div className={styles.classSelectedHint}>
            <span>
              {t(
                'error_insight.class_selected_hint',
                'Showing only {{class}} — click again to clear',
                { class: selectedClassLabel }
              )}
            </span>
            <button
              type="button"
              onClick={() => setFilters({ selectedClass: '' })}
              aria-label={clearFiltersLabel}
            >
              <IconX size={12} />
            </button>
          </div>
        ) : null}
      </section>

      {status === 'error' ? (
        <section className={styles.alertPanel}>
          <IconShield size={22} />
          <div>
            <strong>{t('error_insight.load_failed')}</strong>
          </div>
        </section>
      ) : null}

      {status === 'loading' ? (
        <p className={styles.stateLine}>{t('error_insight.loading')}</p>
      ) : null}

      {status === 'ready' && view ? (
        <>
          <section className={styles.kpiGrid}>
            <div className={styles.kpiCard}>
              <div className={styles.kpiCardHeader}>
                <span className={styles.kpiIcon}>
                  <IconTriangleAlert size={16} />
                </span>
                <span className={styles.kpiLabel}>
                  {t('error_insight.kpi_total', 'Total failures')}
                </span>
              </div>
              <div className={styles.kpiValue}>{view.kpis.totalFailures.toLocaleString()}</div>
            </div>
            <div className={styles.kpiCard}>
              <div className={styles.kpiCardHeader}>
                <span className={styles.kpiIcon}>
                  <IconChartLine size={16} />
                </span>
                <span className={styles.kpiLabel}>
                  {t('error_insight.kpi_top_class', 'Top class')}
                </span>
              </div>
              <div className={styles.kpiValue}>
                {view.kpis.topClass ? classLabel(t, view.kpis.topClass) : '—'}
              </div>
              {view.kpis.topClass ? (
                <div className={styles.kpiMeta}>{formatPercent(view.kpis.topShare)}</div>
              ) : null}
            </div>
            <div className={styles.kpiCard}>
              <div className={styles.kpiCardHeader}>
                <span className={styles.kpiIcon}>
                  <IconSatellite size={16} />
                </span>
                <span className={styles.kpiLabel}>
                  {t('error_insight.kpi_upstream_share', 'Upstream-side share')}
                </span>
              </div>
              <div className={styles.kpiValue}>{formatPercent(view.kpis.upstreamShare)}</div>
            </div>
            <div className={styles.kpiCard}>
              <div className={styles.kpiCardHeader}>
                <span className={styles.kpiIcon}>
                  <IconBan size={16} />
                </span>
                <span className={styles.kpiLabel}>
                  {t('error_insight.kpi_canceled_share', 'Client-canceled share')}
                </span>
              </div>
              <div className={styles.kpiValue}>{formatPercent(view.kpis.canceledShare)}</div>
            </div>
          </section>

          <section className={styles.dualChartGrid}>
            <div className={styles.panel}>
              <div className={styles.panelHeader}>
                <h2>{t('error_insight.distribution')}</h2>
              </div>
              {donutOption ? (
                <EChartsView
                  option={donutOption}
                  ariaLabel={t('error_insight.distribution')}
                  className={styles.echartsCanvas}
                  style={{ height: 260 }}
                  onClick={handleDonutClick}
                />
              ) : (
                <div className={styles.chartEmptyInline}>{t('error_insight.empty')}</div>
              )}
            </div>
            <div className={styles.panel}>
              <div className={styles.panelHeader}>
                <h2>{t('error_insight.timeline')}</h2>
              </div>
              {timelineOption ? (
                <EChartsView
                  option={timelineOption}
                  ariaLabel={t('error_insight.timeline')}
                  className={styles.echartsCanvas}
                  style={{ height: 260 }}
                />
              ) : (
                <div className={styles.chartEmptyInline}>{t('error_insight.empty')}</div>
              )}
            </div>
          </section>

          <section className={styles.dualChartGrid}>
            <div className={styles.panel}>
              <div className={styles.panelHeader}>
                <h2>{t('error_insight.breakdown_provider', 'Failures by provider')}</h2>
              </div>
              {byProviderOption ? (
                <EChartsView
                  option={byProviderOption}
                  ariaLabel={t('error_insight.breakdown_provider', 'Failures by provider')}
                  className={styles.echartsCanvas}
                  style={{ height: Math.min(360, Math.max(220, view.byProvider.keys.length * 32)) }}
                />
              ) : (
                <div className={styles.chartEmptyInline}>{t('error_insight.empty')}</div>
              )}
            </div>
            <div className={styles.panel}>
              <div className={styles.panelHeader}>
                <h2>{t('error_insight.breakdown_model', 'Failures by model')}</h2>
              </div>
              {byModelOption ? (
                <EChartsView
                  option={byModelOption}
                  ariaLabel={t('error_insight.breakdown_model', 'Failures by model')}
                  className={styles.echartsCanvas}
                  style={{ height: Math.min(360, Math.max(220, view.byModel.keys.length * 32)) }}
                />
              ) : (
                <div className={styles.chartEmptyInline}>{t('error_insight.empty')}</div>
              )}
            </div>
          </section>

          <section className={styles.tablePanel}>
            <div className={styles.panelHeader}>
              <h2>{t('error_insight.recent')}</h2>
            </div>
            {filteredRecent.length === 0 ? (
              <div className={styles.chartEmptyInline}>{t('error_insight.empty')}</div>
            ) : (
              <div className={styles.tableWrap}>
                <table>
                  <thead>
                    <tr>
                      <th>{t('error_insight.col_time')}</th>
                      <th>{t('error_insight.col_class')}</th>
                      <th>{t('error_insight.col_status')}</th>
                      <th>{t('error_insight.col_model')}</th>
                      <th>{t('error_insight.col_account')}</th>
                      <th>{t('error_insight.col_provider', 'Provider')}</th>
                      <th>{t('error_insight.col_summary')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filteredRecent.map((item, index) => {
                      const foldedClass = foldClass(item.class);
                      return (
                        <tr key={`${item.timestamp_ms}-${index}`}>
                          <td>{formatLocalDateTime(item.timestamp_ms, locale)}</td>
                          <td>
                            <span className={styles.classChip}>
                              <span
                                className={styles.classDot}
                                style={{ backgroundColor: ERROR_CLASS_COLORS[foldedClass] }}
                              />
                              {t(`error_insight.class.${item.class}`, item.class)}
                            </span>
                          </td>
                          <td className={styles.monoCell}>{item.status_code ?? '-'}</td>
                          <td>{item.model ?? '-'}</td>
                          <td>{item.account ?? '-'}</td>
                          <td>{item.provider ?? '-'}</td>
                          <td className={styles.summaryCell} title={item.summary ?? ''}>
                            {item.summary ?? ''}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      ) : null}
    </div>
  );
}
