import { describe, expect, it } from 'vitest';
import {
  buildBucketEditOptions,
  collectObservedBucketNames,
  parseConfiguredBucketNames,
  providerSupportsBuckets,
  scopeBucketFilterToProvider,
  UNTAGGED_BUCKET_FILTER,
} from './bucketOptions';

describe('parseConfiguredBucketNames', () => {
  it('reads codex-buckets keys', () => {
    const yaml = ['codex-buckets:', '  anon:', '    api-keys:', '      - sk-1', '  team:', '    api-keys: []'].join('\n');
    expect(parseConfiguredBucketNames(yaml)).toEqual(['anon', 'team']);
  });

  it('returns empty when the block is absent', () => {
    expect(parseConfiguredBucketNames('port: 8317')).toEqual([]);
  });

  it('returns empty on malformed yaml instead of throwing', () => {
    expect(parseConfiguredBucketNames('codex-buckets: [unclosed')).toEqual([]);
  });
});

describe('collectObservedBucketNames', () => {
  it('dedupes, trims, drops empties, and sorts', () => {
    expect(
      collectObservedBucketNames([
        { bucket: 'team' },
        { bucket: '  anon ' },
        { bucket: 'anon' },
        { bucket: '   ' },
        {},
      ])
    ).toEqual(['anon', 'team']);
  });
});

describe('buildBucketEditOptions', () => {
  it('unions configured and observed names', () => {
    expect(buildBucketEditOptions(['anon'], ['legacy', 'anon'])).toEqual(['anon', 'legacy']);
  });
});

describe('UNTAGGED_BUCKET_FILTER', () => {
  it('is the reserved sentinel', () => {
    expect(UNTAGGED_BUCKET_FILTER).toBe('__untagged__');
  });
});

describe('providerSupportsBuckets', () => {
  it('is true only for codex, regardless of case', () => {
    expect(providerSupportsBuckets('codex')).toBe(true);
    expect(providerSupportsBuckets('Codex')).toBe(true);
    expect(providerSupportsBuckets('all')).toBe(false);
    expect(providerSupportsBuckets('gemini')).toBe(false);
    expect(providerSupportsBuckets('')).toBe(false);
  });
});

describe('scopeBucketFilterToProvider', () => {
  it('keeps the bucket when the provider is codex', () => {
    const filters = { provider: 'codex', bucket: 'team-a', model: 'gpt-5' };
    expect(scopeBucketFilterToProvider(filters)).toBe(filters);
  });

  it('resets the bucket to all for any other provider', () => {
    expect(scopeBucketFilterToProvider({ provider: 'all', bucket: 'team-a' })).toEqual({
      provider: 'all',
      bucket: 'all',
    });
    expect(
      scopeBucketFilterToProvider({ provider: 'gemini', bucket: UNTAGGED_BUCKET_FILTER })
    ).toEqual({ provider: 'gemini', bucket: 'all' });
  });
});
