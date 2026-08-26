import { describe, expect, it } from 'vitest';
import {
  buildBucketEditOptions,
  collectObservedBucketNames,
  parseConfiguredBucketNames,
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
