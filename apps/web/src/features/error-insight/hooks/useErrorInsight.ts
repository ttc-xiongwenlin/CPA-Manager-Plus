import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { errorInsightApi } from '@/services/api/errorInsight';
import {
  buildErrorInsightView,
  ERROR_INSIGHT_WINDOW_PRESETS,
  type ErrorInsightView,
} from '../model/errorInsightModel';

export type ErrorInsightStatus = 'idle' | 'loading' | 'ready' | 'error';

interface UseErrorInsightOptions {
  serviceBase: string;
  managementKey?: string;
}

export function useErrorInsight({ serviceBase, managementKey }: UseErrorInsightOptions) {
  const [windowMs, setWindowMs] = useState<number>(ERROR_INSIGHT_WINDOW_PRESETS[2].ms); // 24h
  const [status, setStatus] = useState<ErrorInsightStatus>('idle');
  const [view, setView] = useState<ErrorInsightView | null>(null);
  const [generation, setGeneration] = useState(0);
  const controllerRef = useRef<AbortController | null>(null);

  const refresh = useCallback(() => setGeneration((value) => value + 1), []);

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
    void (async () => {
      try {
        const response = await errorInsightApi.fetch(
          serviceBase,
          managementKey,
          { from_ms: toMs - windowMs, to_ms: toMs },
          controller.signal
        );
        if (controller.signal.aborted) return;
        setView(buildErrorInsightView(response, { fromMs: toMs - windowMs, toMs }));
        setStatus('ready');
      } catch {
        if (controller.signal.aborted) return;
        setStatus('error');
      }
    })();
    return () => controller.abort();
  }, [serviceBase, managementKey, windowMs, generation]);

  return useMemo(
    () => ({ status, view, windowMs, setWindowMs, refresh }),
    [status, view, windowMs, refresh]
  );
}
