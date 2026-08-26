import { useEffect, useMemo, useState } from 'react';
import { configFileApi } from '@/services/api';
import type { AuthFileItem } from '@/types';
import {
  buildBucketEditOptions,
  collectObservedBucketNames,
  parseConfiguredBucketNames,
} from '@/features/authFiles/bucketOptions';

/**
 * Bucket names available for tagging Codex accounts in the auth-files editor:
 * names declared in CPA's config.yaml (fetched once) plus any bucket already
 * applied to an account (recomputed whenever the auth-files list changes).
 */
export function useAuthFilesBucketOptions(files: AuthFileItem[]): string[] {
  const [configuredBuckets, setConfiguredBuckets] = useState<string[]>([]);

  useEffect(() => {
    let cancelled = false;

    configFileApi
      .fetchConfigYaml()
      .then((configYaml) => {
        if (cancelled) return;
        setConfiguredBuckets(parseConfiguredBucketNames(configYaml));
      })
      .catch(() => {
        if (!cancelled) setConfiguredBuckets([]);
      });

    return () => {
      cancelled = true;
    };
  }, []);

  return useMemo(
    () => buildBucketEditOptions(configuredBuckets, collectObservedBucketNames(files)),
    [configuredBuckets, files]
  );
}
