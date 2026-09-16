import { describe, expect, it } from 'vitest';
import { buildEventRows } from './eventRows';
import { buildScopeFilteredRows } from './rowBuilders';
import type { MonitoringAuthMeta } from './types';

// Account detail -> "前往请求监控" links to /monitoring?auth_file=<file>&auth_index=<index>.
// The backend already filters on auth_file_snapshot; the local re-filter must not
// drop those rows just because the display label differs from the file name.
describe('auth_file drilldown local filter', () => {
  it('keeps rows whose persisted auth_file_snapshot matches the drilldown auth_file', () => {
    const authIndex = 'e5533d0011223344';
    const authMetaMap = new Map<string, MonitoringAuthMeta>([
      [
        authIndex,
        {
          authIndex,
          label: 'pce.di',
          account: 'pce.di@gmail.com',
          provider: 'codex',
          status: 'active',
          disabled: false,
          unavailable: false,
          runtimeOnly: false,
          planType: 'plus',
          bucket: '',
          updatedAt: '',
        },
      ],
    ]);
    const rows = buildEventRows(
      [
        {
          timestamp: '2026-09-16T07:30:00Z',
          source: 'pce***@gmail.com',
          auth_index: authIndex,
          auth_file_snapshot: 'codex-9d57f2c3.json',
          auth_label_snapshot: 'pce.di',
          account_snapshot: 'pce.di@gmail.com',
          auth_provider_snapshot: 'codex',
          latency_ms: 1200,
          tokens: { input_tokens: 10, output_tokens: 20, total_tokens: 30 },
          failed: false,
          __modelName: 'gpt-5.4',
          __endpoint: 'POST /v1/responses',
          __endpointMethod: 'POST',
          __endpointPath: '/v1/responses',
          __timestampMs: Date.parse('2026-09-16T07:30:00Z'),
        },
      ],
      authMetaMap,
      new Map(),
      { byAuthIndex: new Map(), bySource: new Map(), byIdentityKey: new Map() },
      new Map(),
      {},
      new Map()
    );
    expect(rows).toHaveLength(1);

    const filtered = buildScopeFilteredRows(
      rows,
      { authFile: 'codex-9d57f2c3.json', authIndex },
      authMetaMap
    );

    expect(filtered).toHaveLength(1);
  });
});
