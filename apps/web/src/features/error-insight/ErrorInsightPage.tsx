import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import type { EChartsCoreOption } from 'echarts/core';
import { EChartsView } from '@/components/charts/EChartsView';
import { useAuthStore } from '@/stores';
import { useRequestMonitoringAvailability } from '@/hooks/useRequestMonitoringAvailability';
import { useErrorInsight } from './hooks/useErrorInsight';
import {
  ERROR_CLASS_COLORS,
  ERROR_INSIGHT_WINDOW_PRESETS,
  type ErrorInsightView,
} from './model/errorInsightModel';
import styles from './ErrorInsightPage.module.scss';

function buildTimelineOption(view: ErrorInsightView, labelFor: (cls: string) => string): EChartsCoreOption {
  return {
    tooltip: { trigger: 'axis' },
    grid: { left: 48, right: 16, top: 24, bottom: 32 },
    xAxis: {
      type: 'category',
      data: view.timelineBuckets.map((bucket) =>
        new Date(bucket).toLocaleString(undefined, {
          month: 'numeric',
          day: 'numeric',
          hour: '2-digit',
        })
      ),
    },
    yAxis: { type: 'value' },
    series: view.timelineSeries.map((series) => ({
      type: 'bar',
      stack: 'errors',
      name: labelFor(series.class),
      data: series.data,
      itemStyle: { color: ERROR_CLASS_COLORS[series.class] },
    })),
  };
}

export function ErrorInsightPage() {
  const { t } = useTranslation();
  const managementKey = useAuthStore((state) => state.managementKey);
  const availability = useRequestMonitoringAvailability();
  const { status, view, windowMs, setWindowMs, refresh } = useErrorInsight({
    serviceBase: availability.serviceBase,
    managementKey,
  });

  const timelineOption = useMemo(
    () => (view && view.timelineBuckets.length > 0 ? buildTimelineOption(view, (cls) => t(`error_insight.class.${cls}`, cls)) : null),
    [view, t]
  );

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1>{t('error_insight.title')}</h1>
        <div className={styles.presets}>
          {ERROR_INSIGHT_WINDOW_PRESETS.map((preset) => (
            <button
              key={preset.key}
              type="button"
              className={preset.ms === windowMs ? styles.presetActive : styles.preset}
              onClick={() => setWindowMs(preset.ms)}
            >
              {t(`error_insight.window.${preset.key}`)}
            </button>
          ))}
          <button type="button" className={styles.preset} onClick={refresh}>
            {t('error_insight.refresh')}
          </button>
        </div>
      </header>

      {status === 'loading' && <p className={styles.stateLine}>{t('error_insight.loading')}</p>}
      {status === 'error' && <p className={styles.stateLine}>{t('error_insight.load_failed')}</p>}

      {status === 'ready' && view && (
        <>
          <section className={styles.section}>
            <h2>
              {t('error_insight.distribution')} · {view.totalFailures}
            </h2>
            {view.shares.length === 0 ? (
              <p className={styles.stateLine}>{t('error_insight.empty')}</p>
            ) : (
              <ul className={styles.shareList}>
                {view.shares.map((share) => (
                  <li key={share.class} className={styles.shareRow}>
                    <span
                      className={styles.shareSwatch}
                      style={{ backgroundColor: ERROR_CLASS_COLORS[share.class] }}
                    />
                    <span className={styles.shareLabel}>
                      {t(`error_insight.class.${share.class}`)}
                    </span>
                    <span className={styles.shareBarTrack}>
                      <span
                        className={styles.shareBarFill}
                        style={{
                          width: `${Math.max(share.share * 100, 0.5)}%`,
                          backgroundColor: ERROR_CLASS_COLORS[share.class],
                        }}
                      />
                    </span>
                    <span className={styles.shareCount}>
                      {share.count} · {(share.share * 100).toFixed(1)}%
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </section>

          {timelineOption && (
            <section className={styles.section}>
              <h2>{t('error_insight.timeline')}</h2>
              <EChartsView
                option={timelineOption}
                ariaLabel={t('error_insight.timeline')}
                style={{ height: 280 }}
              />
            </section>
          )}

          {view.recent.length > 0 && (
            <section className={styles.section}>
              <h2>{t('error_insight.recent')}</h2>
              <div className={styles.tableWrap}>
                <table className={styles.recentTable}>
                  <thead>
                    <tr>
                      <th>{t('error_insight.col_time')}</th>
                      <th>{t('error_insight.col_class')}</th>
                      <th>{t('error_insight.col_status')}</th>
                      <th>{t('error_insight.col_model')}</th>
                      <th>{t('error_insight.col_account')}</th>
                      <th>{t('error_insight.col_summary')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {view.recent.map((item, index) => (
                      <tr key={`${item.timestamp_ms}-${index}`}>
                        <td>{new Date(item.timestamp_ms).toLocaleString()}</td>
                        <td>{t(`error_insight.class.${item.class}`, item.class)}</td>
                        <td>{item.status_code ?? '-'}</td>
                        <td>{item.model ?? '-'}</td>
                        <td>{item.account ?? '-'}</td>
                        <td className={styles.summaryCell}>{item.summary ?? ''}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          )}
        </>
      )}
    </div>
  );
}
