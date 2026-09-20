import { useCallback, useEffect, useId, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { IconPencil, IconPlus, IconTrash2, IconX } from '@/components/ui/icons';
import { usePanelFeatureAvailability } from '@/hooks/usePanelFeatureAvailability';
import {
  getObservedProviderModels,
  getProviderPrices,
  getRepriceState,
  saveProviderPrices,
  startReprice,
  type ObservedProviderModel,
  type ProviderModelPrice,
  type RepriceState,
} from '@/services/api/providerPriceService';
import { useAuthStore, useNotificationStore } from '@/stores';
import {
  buildObservedModels,
  buildObservedProviders,
  buildProviderPriceFromDraft,
  buildUnpricedObservedModels,
  calculateRepriceProgress,
  createEmptyProviderPriceDraft,
  createEmptyWindowDraft,
  createProviderPriceDraft,
  formatCnyRate,
  formatRepriceProgressPercent,
  formatWindowBadge,
  groupProviderPrices,
  parseRepriceFromDate,
  providerPriceKey,
  removeProviderPrice,
  upsertProviderPrice,
  type ProviderPriceDraft,
  type ProviderPriceWindowDraft,
} from './providerPricesPageModel';
import styles from './ProviderPricesPage.module.scss';

const REPRICE_POLL_INTERVAL_MS = 3000;

const resolveErrorMessage = (error: unknown, fallback: string) =>
  error instanceof Error && error.message ? error.message : fallback;

export function ProviderPricesPage() {
  const { t, i18n } = useTranslation();
  const { showNotification } = useNotificationStore();
  const managementKey = useAuthStore((state) => state.managementKey);
  const featureAvailability = usePanelFeatureAvailability();
  const serviceBase = featureAvailability.modelPricesAvailable
    ? featureAvailability.managerServiceBase
    : '';
  const datalistId = useId();
  const providerListId = `${datalistId}-providers`;
  const modelListId = `${datalistId}-models`;

  const [prices, setPrices] = useState<ProviderModelPrice[]>([]);
  const [observed, setObserved] = useState<ObservedProviderModel[]>([]);
  const [repriceState, setRepriceState] = useState<RepriceState | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [repriceStarting, setRepriceStarting] = useState(false);
  const [repriceFromDate, setRepriceFromDate] = useState('');
  const [editorOpen, setEditorOpen] = useState(false);
  const [editingKey, setEditingKey] = useState<string | null>(null);
  const [draft, setDraft] = useState<ProviderPriceDraft>(() => createEmptyProviderPriceDraft());

  const groups = useMemo(() => groupProviderPrices(prices), [prices]);
  const unpriced = useMemo(() => buildUnpricedObservedModels(observed, prices), [observed, prices]);
  const observedProviders = useMemo(() => buildObservedProviders(observed), [observed]);
  const observedModels = useMemo(
    () => buildObservedModels(observed, draft.provider),
    [draft.provider, observed]
  );
  const repriceProgress = calculateRepriceProgress(repriceState);
  const repricing = repriceState?.repricing === true;

  const refreshRepriceState = useCallback(
    async (signal?: AbortSignal) => {
      if (!serviceBase) return;
      try {
        const state = await getRepriceState(serviceBase, managementKey, signal);
        if (!signal?.aborted) setRepriceState(state);
      } catch {
        // Older Manager Server versions do not expose the reprice endpoint; keep the card idle.
      }
    },
    [managementKey, serviceBase]
  );

  useEffect(() => {
    const controller = new AbortController();
    if (!serviceBase) {
      setPrices([]);
      setObserved([]);
      setRepriceState(null);
      setLoading(false);
      return () => controller.abort();
    }

    setLoading(true);
    void Promise.all([
      getProviderPrices(serviceBase, managementKey, controller.signal)
        .then((response) => {
          if (!controller.signal.aborted) setPrices(response.prices);
        })
        .catch((error: unknown) => {
          if (controller.signal.aborted) return;
          showNotification(
            `${t('provider_prices.load_failed')}: ${resolveErrorMessage(error, t('common.unknown_error'))}`,
            'error'
          );
        }),
      getObservedProviderModels(serviceBase, managementKey, controller.signal)
        .then((response) => {
          if (!controller.signal.aborted) setObserved(response.items);
        })
        .catch(() => {
          if (!controller.signal.aborted) setObserved([]);
        }),
      refreshRepriceState(controller.signal),
    ]).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });

    return () => controller.abort();
  }, [managementKey, refreshRepriceState, serviceBase, showNotification, t]);

  useEffect(() => {
    if (!repricing) return undefined;
    const controller = new AbortController();
    const timer = window.setInterval(() => {
      void refreshRepriceState(controller.signal);
    }, REPRICE_POLL_INTERVAL_MS);
    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, [refreshRepriceState, repricing]);

  const openEditor = (price?: ProviderModelPrice, preset?: ObservedProviderModel) => {
    if (price) {
      setDraft(createProviderPriceDraft(price));
      setEditingKey(providerPriceKey(price.provider, price.model));
    } else {
      setDraft(createEmptyProviderPriceDraft(preset));
      setEditingKey(null);
    }
    setEditorOpen(true);
  };

  const closeEditor = () => {
    setDraft(createEmptyProviderPriceDraft());
    setEditingKey(null);
    setEditorOpen(false);
  };

  const setDraftField = (
    field: keyof Omit<ProviderPriceDraft, 'windows' | 'id'>,
    value: string
  ) => {
    setDraft((previous) => ({ ...previous, [field]: value }));
  };

  const setWindowField = (index: number, field: keyof ProviderPriceWindowDraft, value: string) => {
    setDraft((previous) => ({
      ...previous,
      windows: previous.windows.map((window, windowIndex) =>
        windowIndex === index ? { ...window, [field]: value } : window
      ),
    }));
  };

  const addWindow = () => {
    setDraft((previous) => ({
      ...previous,
      windows: [...previous.windows, createEmptyWindowDraft()],
    }));
  };

  const removeWindow = (index: number) => {
    setDraft((previous) => ({
      ...previous,
      windows: previous.windows.filter((_, windowIndex) => windowIndex !== index),
    }));
  };

  const persistPrices = async (next: ProviderModelPrice[], successMessage: string) => {
    if (!serviceBase) return false;
    setSaving(true);
    try {
      const response = await saveProviderPrices(serviceBase, next, managementKey);
      setPrices(response.prices);
      showNotification(successMessage, 'success');
      return true;
    } catch (error: unknown) {
      showNotification(
        `${t('provider_prices.save_failed')}: ${resolveErrorMessage(error, t('common.unknown_error'))}`,
        'error'
      );
      return false;
    } finally {
      setSaving(false);
    }
  };

  const handleSave = async () => {
    const result = buildProviderPriceFromDraft(draft);
    if (result.error) {
      showNotification(t(`provider_prices.validation_${result.error}`), 'warning');
      return;
    }
    const saved = await persistPrices(
      upsertProviderPrice(prices, result.price, editingKey),
      t('provider_prices.saved')
    );
    if (saved) closeEditor();
  };

  const handleDelete = async (price: ProviderModelPrice) => {
    const removed = await persistPrices(
      removeProviderPrice(prices, price),
      t('provider_prices.deleted')
    );
    if (removed && editingKey === providerPriceKey(price.provider, price.model)) {
      closeEditor();
    }
  };

  const handleReprice = async () => {
    if (!serviceBase) return;
    const fromMs = parseRepriceFromDate(repriceFromDate);
    const confirmMessage = fromMs
      ? t('provider_prices.reprice_confirm_from', {
          from: new Date(fromMs).toLocaleDateString(i18n.language),
        })
      : t('provider_prices.reprice_confirm_all');
    if (!window.confirm(confirmMessage)) return;

    setRepriceStarting(true);
    try {
      const state = await startReprice(serviceBase, fromMs, managementKey);
      setRepriceState(state);
      showNotification(t('provider_prices.reprice_started'), 'success');
    } catch (error: unknown) {
      showNotification(
        `${t('provider_prices.reprice_failed')}: ${resolveErrorMessage(error, t('common.unknown_error'))}`,
        'error'
      );
    } finally {
      setRepriceStarting(false);
    }
  };

  const renderCacheRate = (value: number | undefined, configured: boolean | undefined) =>
    configured ? (
      formatCnyRate(value)
    ) : (
      <span className={styles.mutedText}>{t('provider_prices.cache_by_prompt')}</span>
    );

  const statusLabel = repriceState
    ? t(`provider_prices.reprice_status_${repriceState.status}`, {
        defaultValue: repriceState.status,
      })
    : '--';

  return (
    <div className={styles.page}>
      <section className={styles.actionBar} aria-label={t('common.action')}>
        <div className={styles.titleGroup}>
          <Link to="/model-prices" className={styles.backLink}>
            {t('provider_prices.back_to_model_prices')}
          </Link>
          <strong className={styles.pageTitle}>{t('provider_prices.title')}</strong>
        </div>
        <div className={styles.actionGroup}>
          <span className={styles.metaPill}>
            {serviceBase
              ? t('provider_prices.rules_count', { count: prices.length })
              : t('provider_prices.service_required')}
          </span>
          <Button size="xs" onClick={() => openEditor()} disabled={!serviceBase}>
            {t('provider_prices.add_price')}
          </Button>
        </div>
      </section>

      <section className={styles.repriceCard} aria-label={t('provider_prices.reprice_title')}>
        <div className={styles.repriceHeader}>
          <strong>{t('provider_prices.reprice_title')}</strong>
          <span
            className={`${styles.statusBadge} ${
              repriceState?.status === 'failed'
                ? styles.statusBadgeBad
                : repricing || repriceState?.status === 'catching_up'
                  ? styles.statusBadgeBusy
                  : ''
            }`}
          >
            {statusLabel}
          </span>
          <span className={styles.mutedText}>
            {t('provider_prices.reprice_priced_through', {
              priced: repriceState?.pricedThroughEventId ?? 0,
              latest: repriceState?.latestEventId ?? 0,
            })}
          </span>
        </div>
        {repricing && repriceProgress !== null ? (
          <div className={styles.progressRow}>
            <div
              className={styles.progressTrack}
              role="progressbar"
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={Math.round(repriceProgress * 100)}
            >
              <div className={styles.progressBar} style={{ width: `${repriceProgress * 100}%` }} />
            </div>
            <span className={styles.progressLabel}>
              {formatRepriceProgressPercent(repriceProgress)}
              {' · '}
              {t('provider_prices.reprice_processed', {
                count: repriceState?.processedEvents ?? 0,
              })}
            </span>
          </div>
        ) : null}
        {repriceState?.lastError ? (
          <div className={styles.errorText}>
            {t('provider_prices.reprice_last_error')}: {repriceState.lastError}
          </div>
        ) : null}
        <div className={styles.repriceForm}>
          <Input
            label={t('provider_prices.reprice_from_label')}
            hint={t('provider_prices.reprice_from_hint')}
            className={styles.compactInput}
            type="date"
            value={repriceFromDate}
            onChange={(event) => setRepriceFromDate(event.target.value)}
          />
          <Button
            size="xs"
            onClick={() => void handleReprice()}
            loading={repriceStarting}
            disabled={!serviceBase || repricing}
          >
            {t('provider_prices.reprice_button')}
          </Button>
        </div>
      </section>

      <div className={styles.hint}>{t('provider_prices.hint')}</div>

      <section className={styles.pricePanel}>
        {editorOpen ? (
          <div className={styles.editor}>
            <div className={styles.editorGrid}>
              <Input
                label={t('provider_prices.provider')}
                className={styles.compactInput}
                value={draft.provider}
                list={providerListId}
                onChange={(event) => setDraftField('provider', event.target.value)}
                placeholder={t('provider_prices.provider_placeholder')}
              />
              <datalist id={providerListId}>
                {observedProviders.map((provider) => (
                  <option key={provider} value={provider} />
                ))}
              </datalist>
              <Input
                label={t('usage_stats.model_name')}
                className={styles.compactInput}
                value={draft.model}
                list={modelListId}
                onChange={(event) => setDraftField('model', event.target.value)}
                placeholder={t('provider_prices.model_placeholder')}
              />
              <datalist id={modelListId}>
                {observedModels.map((model) => (
                  <option key={model} value={model} />
                ))}
              </datalist>
              <Input
                label={`${t('usage_stats.model_price_prompt')} (¥/1M)`}
                className={styles.compactInput}
                type="number"
                min="0"
                step="0.0001"
                value={draft.prompt}
                onChange={(event) => setDraftField('prompt', event.target.value)}
                placeholder="0.0000"
              />
              <Input
                label={`${t('usage_stats.model_price_completion')} (¥/1M)`}
                className={styles.compactInput}
                type="number"
                min="0"
                step="0.0001"
                value={draft.completion}
                onChange={(event) => setDraftField('completion', event.target.value)}
                placeholder="0.0000"
              />
              <Input
                label={`${t('usage_stats.model_price_cache_read')} (¥/1M)`}
                className={styles.compactInput}
                type="number"
                min="0"
                step="0.0001"
                value={draft.cacheRead}
                onChange={(event) => setDraftField('cacheRead', event.target.value)}
                placeholder={t('provider_prices.cache_placeholder')}
              />
              <Input
                label={`${t('usage_stats.model_price_cache_creation')} (¥/1M)`}
                className={styles.compactInput}
                type="number"
                min="0"
                step="0.0001"
                value={draft.cacheCreation}
                onChange={(event) => setDraftField('cacheCreation', event.target.value)}
                placeholder={t('provider_prices.cache_placeholder')}
              />
              <Input
                label={t('provider_prices.timezone')}
                className={styles.compactInput}
                value={draft.timezone}
                onChange={(event) => setDraftField('timezone', event.target.value)}
                placeholder="Asia/Shanghai"
              />
              <Input
                label={t('provider_prices.note')}
                className={styles.compactInput}
                value={draft.note}
                onChange={(event) => setDraftField('note', event.target.value)}
              />
            </div>

            <div className={styles.windowsEditor}>
              <div className={styles.windowsHeader}>
                <strong>{t('provider_prices.windows')}</strong>
                <span className={styles.mutedText}>{t('provider_prices.windows_hint')}</span>
                <Button size="xs" variant="secondary" onClick={addWindow}>
                  <IconPlus size={12} />
                  {t('provider_prices.add_window')}
                </Button>
              </div>
              {draft.windows.map((window, index) => (
                <div key={window.id ?? `new-${index}`} className={styles.windowRow}>
                  <Input
                    label={t('provider_prices.window_start')}
                    className={styles.compactInput}
                    value={window.start}
                    onChange={(event) => setWindowField(index, 'start', event.target.value)}
                    placeholder="00:30"
                  />
                  <Input
                    label={t('provider_prices.window_end')}
                    className={styles.compactInput}
                    value={window.end}
                    onChange={(event) => setWindowField(index, 'end', event.target.value)}
                    placeholder="08:30"
                  />
                  <Input
                    label={t('provider_prices.window_multiplier')}
                    className={styles.compactInput}
                    type="number"
                    min="0"
                    step="0.01"
                    value={window.multiplier}
                    onChange={(event) => setWindowField(index, 'multiplier', event.target.value)}
                    placeholder="0.5"
                  />
                  <Input
                    label={t('provider_prices.window_label')}
                    className={styles.compactInput}
                    value={window.label}
                    onChange={(event) => setWindowField(index, 'label', event.target.value)}
                  />
                  <button
                    type="button"
                    className={styles.iconAction}
                    title={t('provider_prices.remove_window')}
                    aria-label={t('provider_prices.remove_window')}
                    onClick={() => removeWindow(index)}
                  >
                    <IconTrash2 size={14} />
                  </button>
                </div>
              ))}
            </div>

            <div className={styles.editorActions}>
              <Button
                size="xs"
                variant="ghost"
                iconOnly
                aria-label={t('common.cancel')}
                onClick={closeEditor}
              >
                <IconX size={14} />
              </Button>
              <Button size="xs" onClick={() => void handleSave()} loading={saving}>
                {t('common.save')}
              </Button>
            </div>
          </div>
        ) : null}

        {loading ? (
          <div className={styles.emptyState}>{t('common.loading')}</div>
        ) : groups.length === 0 ? (
          <div className={styles.emptyState}>{t('provider_prices.empty')}</div>
        ) : (
          groups.map((group) => (
            <div key={group.provider} className={styles.providerGroup}>
              <div className={styles.providerHeader}>
                <strong>{group.provider}</strong>
                <span className={styles.mutedText}>
                  {t('provider_prices.models_count', { count: group.items.length })}
                </span>
              </div>
              <div className={styles.tableWrap}>
                <table className={styles.priceTable}>
                  <thead>
                    <tr>
                      <th>{t('usage_stats.model_name')}</th>
                      <th>{t('usage_stats.model_price_prompt')}</th>
                      <th>{t('usage_stats.model_price_completion')}</th>
                      <th>{t('usage_stats.model_price_cache_read')}</th>
                      <th>{t('usage_stats.model_price_cache_creation')}</th>
                      <th>{t('provider_prices.windows')}</th>
                      <th>{t('provider_prices.timezone')}</th>
                      <th>{t('common.action')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {group.items.map((price) => (
                      <tr key={providerPriceKey(price.provider, price.model)}>
                        <td className={styles.modelCell}>
                          <div className={styles.modelContent}>
                            <strong>{price.model}</strong>
                            {price.note ? <span>{price.note}</span> : null}
                          </div>
                        </td>
                        <td>{formatCnyRate(price.prompt)}</td>
                        <td>{formatCnyRate(price.completion)}</td>
                        <td>{renderCacheRate(price.cacheRead, price.cacheReadConfigured)}</td>
                        <td>
                          {renderCacheRate(price.cacheCreation, price.cacheCreationConfigured)}
                        </td>
                        <td className={styles.windowCell}>
                          {price.windows?.length ? (
                            <div className={styles.windowList}>
                              {price.windows.map((window, index) => (
                                <span key={window.id ?? index} className={styles.windowBadge}>
                                  {formatWindowBadge(window)}
                                </span>
                              ))}
                            </div>
                          ) : (
                            <span className={styles.mutedText}>
                              {t('provider_prices.no_windows')}
                            </span>
                          )}
                        </td>
                        <td className={styles.timezoneCell}>{price.timezone}</td>
                        <td className={styles.actionsCell}>
                          <div className={styles.rowActions}>
                            <button
                              type="button"
                              className={styles.iconAction}
                              title={t('common.edit')}
                              aria-label={t('common.edit')}
                              onClick={() => openEditor(price)}
                            >
                              <IconPencil size={14} />
                            </button>
                            <button
                              type="button"
                              className={styles.iconAction}
                              title={t('common.delete')}
                              aria-label={t('common.delete')}
                              disabled={saving}
                              onClick={() => void handleDelete(price)}
                            >
                              <IconTrash2 size={14} />
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          ))
        )}
      </section>

      <section className={styles.pricePanel} aria-label={t('provider_prices.unpriced_title')}>
        <div className={styles.providerHeader}>
          <strong>{t('provider_prices.unpriced_title')}</strong>
          <span className={styles.mutedText}>{t('provider_prices.unpriced_hint')}</span>
        </div>
        {unpriced.length === 0 ? (
          <div className={styles.emptyState}>{t('provider_prices.unpriced_empty')}</div>
        ) : (
          <div className={styles.chipList}>
            {unpriced.map((item) => (
              <button
                key={providerPriceKey(item.provider, item.model)}
                type="button"
                className={styles.chip}
                onClick={() => openEditor(undefined, item)}
              >
                <strong>{item.provider}</strong>
                <span>{item.model}</span>
                <small>{t('provider_prices.unpriced_calls', { count: item.calls })}</small>
              </button>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}
