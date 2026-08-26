import { describe, expect, it } from 'vitest';
import { buildMonitoringAuthMetaMap } from './authMeta';

describe('buildMonitoringAuthMetaMap bucket', () => {
  it('carries a trimmed bucket through', () => {
    const map = buildMonitoringAuthMetaMap([
      { name: 'a.json', auth_index: '1', bucket: '  anon  ' },
    ]);
    expect(map.get('1')?.bucket).toBe('anon');
  });

  it('uses an empty string when untagged', () => {
    const map = buildMonitoringAuthMetaMap([{ name: 'b.json', auth_index: '2' }]);
    expect(map.get('2')?.bucket).toBe('');
  });
});
