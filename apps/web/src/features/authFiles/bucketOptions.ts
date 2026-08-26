import { parse as parseYaml } from 'yaml';

/** Reserved filter value selecting accounts that carry no bucket tag. */
export const UNTAGGED_BUCKET_FILTER = '__untagged__';

const sortedUnique = (values: string[]): string[] =>
  Array.from(new Set(values)).sort((left, right) => left.localeCompare(right));

/**
 * Bucket names declared in CPA's config.yaml under `codex-buckets`.
 * Returns [] rather than throwing when the config is absent or unparseable —
 * the dropdown degrades to observed values instead of breaking the page.
 */
export const parseConfiguredBucketNames = (configYaml: string): string[] => {
  try {
    const parsed = parseYaml(configYaml);
    if (!parsed || typeof parsed !== 'object') return [];
    const buckets = (parsed as Record<string, unknown>)['codex-buckets'];
    if (!buckets || typeof buckets !== 'object' || Array.isArray(buckets)) return [];
    return sortedUnique(
      Object.keys(buckets as Record<string, unknown>)
        .map((key) => key.trim())
        .filter((key) => key !== '')
    );
  } catch {
    return [];
  }
};

/** Bucket values actually present on accounts. */
export const collectObservedBucketNames = (files: Array<{ bucket?: string }>): string[] =>
  sortedUnique(
    files
      .map((file) => (typeof file.bucket === 'string' ? file.bucket.trim() : ''))
      .filter((bucket) => bucket !== '')
  );

/**
 * Options for the edit dropdown: declared names plus anything already applied
 * out-of-band, so a hand-written tag is never silently dropped on save.
 */
export const buildBucketEditOptions = (configured: string[], observed: string[]): string[] =>
  sortedUnique([...configured, ...observed]);
