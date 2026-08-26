import { describe, expect, it } from 'vitest';
import type {
  MonitoringAnalyticsChannelShareRow,
  MonitoringAnalyticsEventRow,
} from '@/services/api/usageService';
import { buildSourceInfoMap } from '@/utils/sourceResolver';
import { UNTAGGED_BUCKET_FILTER } from '@/features/authFiles/bucketOptions';
import {
  buildAnalyticsFilters,
  buildMonitoringAccountFilterValue,
  buildChannelRowsFromAnalytics,
  buildFailureRowsFromAnalytics,
  buildFailureSourceRowsFromAnalytics,
  buildFilterOptionsFromAnalytics,
  buildUsageDetailsFromAnalyticsEvents,
  parseMonitoringAccountFilterValue,
} from './analyticsAdapters';
import type { MonitoringAuthMeta } from './types';

describe('buildUsageDetailsFromAnalyticsEvents', () => {
  it('maps resolved model and auth project snapshots into usage details', () => {
    const events: MonitoringAnalyticsEventRow[] = [
      {
        event_hash: 'event-1',
        timestamp_ms: Date.UTC(2026, 4, 20, 1, 2, 3),
        model: 'alias-model(max)',
        analytics_model: 'alias-model',
        requested_model: 'original-alias-model(max)',
        resolved_model: 'upstream-model',
        endpoint: 'POST /v1/chat/completions',
        method: 'POST',
        path: '/v1/chat/completions',
        client_ip: '192.0.2.10',
        x_forwarded_for: '203.0.113.5, 198.51.100.8',
        user_agent: 'test-client/1.0',
        auth_index: 'auth-1',
        source: 'source.json',
        source_hash: 'source-hash',
        api_key_hash: 'api-key-hash',
        account_snapshot: 'account@example.com',
        auth_label_snapshot: 'label',
        auth_provider_snapshot: 'codex',
        auth_project_id_snapshot: 'project-1',
        reasoning_effort: 'medium',
        input_tokens: 10,
        output_tokens: 5,
        cached_tokens: 0,
        cache_read_tokens: 4,
        cache_creation_tokens: 1,
        reasoning_tokens: 1,
        total_tokens: 18,
        latency_ms: 123,
        ttft_ms: 45,
        failed: true,
        fail_status_code: 429,
        fail_summary: 'rate limit exceeded',
      },
    ];

    const details = buildUsageDetailsFromAnalyticsEvents(events);

    expect(details[0]).toMatchObject({
      __modelName: 'alias-model',
      __requestedModel: 'original-alias-model(max)',
      __resolvedModel: 'upstream-model',
      analytics_model: 'alias-model',
      requested_model: 'original-alias-model(max)',
      auth_project_id_snapshot: 'project-1',
      client_ip: '192.0.2.10',
      x_forwarded_for: '203.0.113.5, 198.51.100.8',
      user_agent: 'test-client/1.0',
      reasoning_effort: 'medium',
      latency_ms: 123,
      ttft_ms: 45,
      tokens: {
        cached_tokens: 0,
        cache_read_tokens: 4,
        cache_creation_tokens: 1,
      },
      failed: true,
      fail_status_code: 429,
      fail_summary: 'rate limit exceeded',
    });
  });

  it('derives analytics model when the backend field is absent', () => {
    const events: MonitoringAnalyticsEventRow[] = [
      {
        event_hash: 'event-legacy-model',
        timestamp_ms: Date.UTC(2026, 4, 20, 1, 2, 3),
        model: 'deepseek-v4-flash(max)',
        requested_model: 'deepseek-v4-flash(max)',
        endpoint: 'POST /v1/chat/completions',
        method: 'POST',
        path: '/v1/chat/completions',
        auth_index: '',
        source: '',
        source_hash: '',
        api_key_hash: '',
        account_snapshot: '',
        auth_label_snapshot: '',
        auth_provider_snapshot: '',
        input_tokens: 1,
        output_tokens: 0,
        cached_tokens: 0,
        cache_read_tokens: 0,
        cache_creation_tokens: 0,
        reasoning_tokens: 0,
        total_tokens: 1,
        latency_ms: null,
        failed: false,
      },
    ];

    expect(buildUsageDetailsFromAnalyticsEvents(events)[0]).toMatchObject({
      __modelName: 'deepseek-v4-flash',
      __requestedModel: 'deepseek-v4-flash(max)',
      analytics_model: 'deepseek-v4-flash',
    });
  });

  it('trusts backend-deduped cached tokens from analytics events', () => {
    const events: MonitoringAnalyticsEventRow[] = [
      {
        event_hash: 'event-cache',
        timestamp_ms: Date.UTC(2026, 4, 20, 1, 2, 3),
        model: 'mixed-cache-model',
        endpoint: 'POST /v1/chat/completions',
        method: 'POST',
        path: '/v1/chat/completions',
        auth_index: 'auth-1',
        source: 'source.json',
        source_hash: 'source-hash',
        api_key_hash: 'api-key-hash',
        account_snapshot: '',
        auth_label_snapshot: '',
        auth_provider_snapshot: '',
        input_tokens: 100,
        output_tokens: 20,
        cached_tokens: 5,
        cache_read_tokens: 4,
        cache_creation_tokens: 1,
        reasoning_tokens: 0,
        total_tokens: 130,
        latency_ms: null,
        failed: false,
      },
    ];

    const details = buildUsageDetailsFromAnalyticsEvents(events);

    expect(details[0].tokens.cached_tokens).toBe(5);
    expect(details[0].tokens.cache_read_tokens).toBe(4);
    expect(details[0].tokens.cache_creation_tokens).toBe(1);
  });
});

describe('buildAnalyticsFilters', () => {
  it('maps failed-only status and known accounts into backend filters', () => {
    const filters = buildAnalyticsFilters(
      {
        account: 'alice@example.com',
        status: 'failed',
      },
      new Map([
        [
          'auth-1',
          {
            authIndex: 'auth-1',
            label: 'Alice',
            account: 'alice@example.com',
            provider: 'codex',
            status: 'active',
            disabled: false,
            unavailable: false,
            runtimeOnly: false,
            planType: 'pro',
            bucket: '',
            updatedAt: '',
          },
        ],
      ]),
      []
    );

    expect(filters).toMatchObject({
      auth_indices: ['auth-1'],
      failed_only: true,
    });
    expect(filters.accounts).toBeUndefined();
  });

  it('falls back to account snapshot filters when auth metadata cannot resolve an account', () => {
    const filters = buildAnalyticsFilters(
      {
        account: 'legacy@example.com',
      },
      new Map(),
      []
    );

    expect(filters).toEqual({
      accounts: ['legacy@example.com'],
    });
  });

  it('maps account filter tokens into exact backend identity filters', () => {
    expect(
      buildAnalyticsFilters(
        {
          account: buildMonitoringAccountFilterValue({
            account: 'OpenAI Compatible',
            authIndices: ['openai-auth'],
          }),
        },
        new Map(),
        []
      )
    ).toEqual({
      auth_indices: ['openai-auth'],
    });

    expect(
      buildAnalyticsFilters(
        {
          account: buildMonitoringAccountFilterValue({
            account: 'OpenAI Compatible',
            sourceHashes: ['source-hash'],
          }),
        },
        new Map(),
        []
      )
    ).toEqual({
      source_hashes: ['source-hash'],
    });

    expect(
      buildAnalyticsFilters(
        {
          account: buildMonitoringAccountFilterValue({
            account: 'OpenAI Compatible',
            apiKeyHashes: ['API-Key-Hash'],
          }),
        },
        new Map(),
        []
      )
    ).toEqual({
      api_key_hashes: ['api-key-hash'],
    });
  });

  it('falls back to provider filters when auth metadata cannot resolve a provider', () => {
    const filters = buildAnalyticsFilters(
      {
        provider: 'legacy-provider',
      },
      new Map(),
      []
    );

    expect(filters).toEqual({
      providers: ['legacy-provider'],
    });
  });

  it('maps usage analytics drilldown dimensions into backend filters', () => {
    const filters = buildAnalyticsFilters(
      {
        authFile: 'codex-auth.json',
        authIndex: 'auth-1',
        projectId: 'project-1',
        requestType: 'codex',
        minLatencyMs: 10_000,
        cacheStatus: 'hit',
      },
      new Map(),
      []
    );

    expect(filters).toEqual({
      auth_files: ['codex-auth.json'],
      auth_indices: ['auth-1'],
      project_ids: ['project-1'],
      request_types: ['codex'],
      min_latency_ms: 10_000,
      cache_status: 'hit',
    });
  });

  it('scopes account fallback filterValue by provider so same-email rows do not collide', () => {
    const codexFilter = buildMonitoringAccountFilterValue({
      provider: 'codex',
      account: 'same@example.com',
    });
    const antigravityFilter = buildMonitoringAccountFilterValue({
      provider: 'antigravity',
      account: 'same@example.com',
    });

    expect(codexFilter).not.toBe(antigravityFilter);
    expect(codexFilter.startsWith('account-provider:')).toBe(true);

    const codexCriteria = parseMonitoringAccountFilterValue(codexFilter);
    expect(codexCriteria.provider).toBe('codex');
    expect(codexCriteria.accounts).toEqual(['same@example.com']);

    const antigravityCriteria = parseMonitoringAccountFilterValue(antigravityFilter);
    expect(antigravityCriteria.provider).toBe('antigravity');
    expect(antigravityCriteria.accounts).toEqual(['same@example.com']);
  });

  it('still parses legacy account: selectors without a provider', () => {
    const criteria = parseMonitoringAccountFilterValue('account:same@example.com');
    expect(criteria.accounts).toEqual(['same@example.com']);
    expect(criteria.provider).toBeUndefined();
  });

  it('emits provider-scoped account AND provider backend filters when no exact selector matches', () => {
    const codexFilter = buildMonitoringAccountFilterValue({
      provider: 'codex',
      account: 'same@example.com',
    });
    const filters = buildAnalyticsFilters({ account: codexFilter }, new Map(), []);

    expect(filters.accounts).toEqual(['same@example.com']);
    expect(filters.providers).toEqual(['codex']);
  });

  it('bypasses authMeta expansion for account-provider selectors to avoid excluding historical events', () => {
    const codexFilter = buildMonitoringAccountFilterValue({
      provider: 'codex',
      account: 'same@example.com',
    });
    const authMetaMap = new Map([
      [
        'current-auth',
        {
          authIndex: 'current-auth',
          label: 'Current',
          account: 'same@example.com',
          provider: 'codex',
          status: 'active',
          disabled: false,
          unavailable: false,
          runtimeOnly: false,
          planType: 'pro',
          bucket: '',
          updatedAt: '',
        },
      ],
    ]);

    const filters = buildAnalyticsFilters({ account: codexFilter }, authMetaMap, []);

    expect(filters.accounts).toEqual(['same@example.com']);
    expect(filters.providers).toEqual(['codex']);
    expect(filters.auth_indices).toBeUndefined();
  });

  it('keeps legacy account: authMeta expansion for backward compatibility', () => {
    const authMetaMap = new Map([
      [
        'auth-1',
        {
          authIndex: 'auth-1',
          label: 'Legacy',
          account: 'legacy@example.com',
          provider: 'codex',
          status: 'active',
          disabled: false,
          unavailable: false,
          runtimeOnly: false,
          planType: 'pro',
          bucket: '',
          updatedAt: '',
        },
      ],
    ]);

    const filters = buildAnalyticsFilters({ account: 'account:legacy@example.com' }, authMetaMap, []);

    expect(filters.auth_indices).toEqual(['auth-1']);
  });
});

describe('buildFilterOptionsFromAnalytics', () => {
  it('maps backend option aggregates into stable dropdown candidates', () => {
    const options = buildFilterOptionsFromAnalytics(
      {
        account_stats: [
          {
            id: 'alice@example.com',
            account_snapshot: 'alice@example.com',
            auth_label_snapshot: 'Alice Auth',
            auth_provider_snapshot: 'codex',
            auth_indices: ['auth-1'],
            sources: ['alice.json'],
            source_hashes: ['source-a'],
            calls: 1,
            success_calls: 1,
            failure_calls: 0,
            success_rate: 1,
            input_tokens: 1,
            output_tokens: 1,
            cached_tokens: 0,
            cache_read_tokens: 0,
            cache_creation_tokens: 0,
            total_tokens: 2,
            cost: 0,
            average_latency_ms: null,
            last_seen_ms: 1,
            models: [],
          },
          {
            id: 'bob@example.com',
            account_snapshot: 'bob@example.com',
            auth_label_snapshot: 'Bob Auth',
            auth_provider_snapshot: 'gemini',
            auth_indices: ['auth-2'],
            sources: ['bob.json'],
            source_hashes: ['source-b'],
            calls: 1,
            success_calls: 1,
            failure_calls: 0,
            success_rate: 1,
            input_tokens: 1,
            output_tokens: 1,
            cached_tokens: 0,
            cache_read_tokens: 0,
            cache_creation_tokens: 0,
            total_tokens: 2,
            cost: 0,
            average_latency_ms: null,
            last_seen_ms: 2,
            models: [],
          },
        ],
        api_key_stats: [
          {
            id: 'key-a',
            api_key_hash: 'key-a',
            account_snapshot: 'alice@example.com',
            auth_label_snapshot: 'Alice Auth',
            auth_provider_snapshot: 'codex',
            auth_indices: ['auth-1'],
            sources: ['alice.json'],
            source_hashes: ['source-a'],
            calls: 1,
            success_calls: 1,
            failure_calls: 0,
            success_rate: 1,
            input_tokens: 1,
            output_tokens: 1,
            cached_tokens: 0,
            cache_read_tokens: 0,
            cache_creation_tokens: 0,
            total_tokens: 2,
            cost: 0,
            average_latency_ms: null,
            last_seen_ms: 1,
            models: [],
          },
        ],
        channel_share: [
          {
            auth_index: 'auth-1',
            auth_provider_snapshot: 'codex',
            calls: 1,
            success: 1,
            failure: 0,
            tokens: 2,
            cost: 0,
            average_latency_ms: null,
          },
          {
            auth_index: 'auth-2',
            auth_provider_snapshot: 'gemini',
            calls: 1,
            success: 1,
            failure: 0,
            tokens: 2,
            cost: 0,
            average_latency_ms: null,
          },
        ],
        model_stats: [
          {
            model: 'gpt-a',
            calls: 1,
            success_calls: 1,
            failure_calls: 0,
            success_rate: 1,
            input_tokens: 1,
            output_tokens: 1,
            cached_tokens: 0,
            cache_read_tokens: 0,
            cache_creation_tokens: 0,
            total_tokens: 2,
            cost: 0,
          },
          {
            model: 'gpt-b',
            calls: 1,
            success_calls: 1,
            failure_calls: 0,
            success_rate: 1,
            input_tokens: 1,
            output_tokens: 1,
            cached_tokens: 0,
            cache_read_tokens: 0,
            cache_creation_tokens: 0,
            total_tokens: 2,
            cost: 0,
          },
        ],
      },
      new Map([
        [
          'auth-1',
          {
            authIndex: 'auth-1',
            label: 'Alice Auth',
            account: 'alice@example.com',
            provider: 'codex',
            status: 'active',
            disabled: false,
            unavailable: false,
            runtimeOnly: false,
            planType: 'pro',
            bucket: '',
            updatedAt: '',
          },
        ],
      ]),
      new Map(),
      buildSourceInfoMap({}),
      new Map([
        [
          'auth-1',
          {
            key: 'primary:0',
            name: 'Primary Channel',
            baseUrl: 'https://primary.example.com',
            host: 'primary.example.com',
            disabled: false,
            authIndices: ['auth-1'],
            modelNames: [],
          },
        ],
      ]),
      new Map([['key-a', { label: 'Key A', masked: 'sk********aa' }]])
    );

    expect(options.accountRows.map((row) => row.account).sort()).toEqual([
      'alice@example.com',
      'bob@example.com',
    ]);
    expect(options.apiKeyRows.map((row) => row.apiKeyLabel)).toEqual(['Key A']);
    expect(options.providers).toEqual(['codex', 'gemini']);
    expect(options.models).toEqual(['gpt-a', 'gpt-b']);
    expect(options.channels).toEqual(['Primary Channel', 'gemini']);
  });

  it('maps lightweight selector values without requiring aggregate rows', () => {
    const options = buildFilterOptionsFromAnalytics(
      {
        accounts: ['alice@example.com', 'bob@example.com'],
        api_key_hashes: ['key-a'],
        providers: ['codex'],
        models: ['gpt-a'],
      },
      new Map(),
      new Map(),
      buildSourceInfoMap({}),
      new Map([
        [
          'auth-1',
          {
            key: 'primary:0',
            name: 'Primary Channel',
            baseUrl: 'https://primary.example.com',
            host: 'primary.example.com',
            disabled: false,
            authIndices: ['auth-1'],
            modelNames: [],
          },
        ],
      ]),
      new Map([['key-a', { label: 'Key A', masked: 'sk********aa' }]])
    );

    expect(options.accountRows.map((row) => row.account)).toEqual([
      'alice@example.com',
      'bob@example.com',
    ]);
    expect(options.accountRows.map((row) => row.filterValue)).toEqual([
      'account:alice%40example.com',
      'account:bob%40example.com',
    ]);
    expect(options.apiKeyRows.map((row) => row.apiKeyLabel)).toEqual(['Key A']);
    expect(options.providers).toEqual(['codex']);
    expect(options.models).toEqual(['gpt-a']);
    expect(options.channels).toEqual(['Primary Channel', 'codex']);
  });

  it('uses auth or source identities for OpenAI-compatible account option rows', () => {
    const options = buildFilterOptionsFromAnalytics(
      {
        account_stats: [
          {
            id: 'openai-provider',
            account_snapshot: '',
            auth_label_snapshot: '',
            auth_provider_snapshot: 'openai',
            auth_indices: ['openai-auth'],
            sources: ['k:upstream-key'],
            source_hashes: ['source-a'],
            calls: 1,
            success_calls: 1,
            failure_calls: 0,
            success_rate: 1,
            input_tokens: 1,
            output_tokens: 1,
            cached_tokens: 0,
            cache_read_tokens: 0,
            cache_creation_tokens: 0,
            total_tokens: 2,
            cost: 0,
            average_latency_ms: null,
            last_seen_ms: 2,
            models: [],
          },
          {
            id: 'openai-provider-without-auth',
            account_snapshot: '',
            auth_label_snapshot: '',
            auth_provider_snapshot: 'openai',
            auth_indices: [],
            sources: ['k:upstream-key-2'],
            source_hashes: ['source-b'],
            calls: 1,
            success_calls: 1,
            failure_calls: 0,
            success_rate: 1,
            input_tokens: 1,
            output_tokens: 1,
            cached_tokens: 0,
            cache_read_tokens: 0,
            cache_creation_tokens: 0,
            total_tokens: 2,
            cost: 0,
            average_latency_ms: null,
            last_seen_ms: 1,
            models: [],
          },
        ],
      },
      new Map(),
      new Map(),
      buildSourceInfoMap({
        openaiCompatibility: [
          {
            name: 'OpenAI Compatible',
            baseUrl: 'https://compat.example.com',
            apiKeyEntries: [{ apiKey: 'upstream-key', authIndex: 'openai-auth' }],
          },
        ],
      }),
      new Map([
        [
          'openai-auth',
          {
            key: 'openai:0',
            name: 'OpenAI Compatible',
            baseUrl: 'https://compat.example.com',
            host: 'compat.example.com',
            disabled: false,
            authIndices: ['openai-auth'],
            modelNames: [],
          },
        ],
      ]),
      new Map()
    );

    expect(options.accountRows.map((row) => row.filterValue)).toEqual([
      'auth:openai-auth',
      'source:source-b',
    ]);
    expect(options.accountRows[0].sourceKeys).toContain('openai:0:0');
  });

  it('uses persisted identity for filterValue when display fallback would differ from snapshots', () => {
    const options = buildFilterOptionsFromAnalytics(
      {
        account_stats: [
          {
            id: 'backend-row-id',
            account_snapshot: '',
            auth_label_snapshot: 'Shared Label',
            auth_provider_snapshot: 'codex',
            auth_indices: [],
            sources: [],
            source_hashes: [],
            calls: 1,
            success_calls: 1,
            failure_calls: 0,
            success_rate: 1,
            input_tokens: 1,
            output_tokens: 1,
            cached_tokens: 0,
            cache_read_tokens: 0,
            cache_creation_tokens: 0,
            total_tokens: 2,
            cost: 0,
            average_latency_ms: null,
            last_seen_ms: 1,
            models: [],
          },
        ],
      },
      new Map(),
      new Map(),
      buildSourceInfoMap({}),
      new Map(),
      new Map()
    );

    const row = options.accountRows[0];
    expect(row.filterValue).toBe('account-provider:codex|Shared%20Label');
  });
});

describe('analytics failure source display', () => {
  const authMetaMap = new Map([
    [
      'auth-1',
      {
        authIndex: 'auth-1',
        label: 'Team Auth',
        account: 'alice@example.com',
        provider: 'codex',
        status: 'active',
        disabled: false,
        unavailable: false,
        runtimeOnly: false,
        planType: 'pro',
        bucket: '',
        updatedAt: '',
      },
    ],
  ]);
  const authFileMap = new Map([['auth-1', { name: 'Team Auth', type: 'codex' }]]);
  const sourceInfoMap = buildSourceInfoMap({});
  const channelByAuthIndex = new Map([
    [
      'auth-1',
      {
        key: 'relay:0',
        name: 'Production Relay',
        baseUrl: 'https://relay.example.com/v1',
        host: 'relay.example.com',
        disabled: false,
        authIndices: ['auth-1'],
        modelNames: [],
      },
    ],
  ]);

  it('uses channel metadata for channel share rows when auth metadata exists', () => {
    const rows = buildChannelRowsFromAnalytics(
      [
        {
          auth_index: 'auth-1',
          source: 'm:sk-a...zzzz',
          account_snapshot: 'snapshot@example.com',
          auth_label_snapshot: 'Snapshot Auth',
          auth_provider_snapshot: 'codex',
          calls: 10,
          success: 8,
          failure: 2,
          tokens: 1000,
          cost: 0.12,
          average_latency_ms: 120,
        },
      ],
      authMetaMap,
      authFileMap,
      sourceInfoMap,
      channelByAuthIndex
    );

    expect(rows[0].label).toBe('Production Relay');
    expect(rows[0].host).toBe('relay.example.com');
    expect(rows[0].authLabels).toEqual(['Team Auth']);
  });

  it('uses channel share snapshots when current auth metadata is missing', () => {
    const rows = buildChannelRowsFromAnalytics(
      [
        {
          auth_index: 'legacy-auth',
          source: 'm:sk-a...zzzz',
          account_snapshot: 'legacy@example.com',
          auth_label_snapshot: 'Legacy Auth',
          auth_provider_snapshot: 'codex',
          calls: 4,
          success: 3,
          failure: 1,
          tokens: 500,
          cost: 0.04,
          average_latency_ms: 200,
        } satisfies MonitoringAnalyticsChannelShareRow,
      ],
      new Map(),
      new Map(),
      sourceInfoMap,
      new Map()
    );

    expect(rows[0].label).toBe('Legacy Auth');
    expect(rows[0].provider).toBe('codex');
    expect(rows[0].label).not.toBe('legacy-auth');
  });

  it('uses readable account metadata for recent failures instead of hashes', () => {
    const rows = buildFailureRowsFromAnalytics(
      [
        {
          timestamp_ms: Date.UTC(2026, 4, 20, 1, 2, 3),
          model: 'gpt-test',
          api_key_hash: 'api-key-hash',
          source: 'm:sk-a...zzzz',
          source_hash: 'source-hash',
          auth_index: 'auth-1',
          account_snapshot: 'snapshot@example.com',
          auth_label_snapshot: 'Snapshot Auth',
          auth_provider_snapshot: 'codex',
          endpoint: 'POST /v1/chat/completions',
          duration_ms: 123,
          fail_status_code: 429,
          fail_summary: 'rate limit exceeded',
        },
      ],
      authMetaMap,
      authFileMap,
      sourceInfoMap,
      channelByAuthIndex
    );

    expect(rows[0].source).toBe('Team Auth');
    expect(rows[0].channel).toBe('Production Relay');
    expect(rows[0].source).not.toBe('source-hash');
  });

  it('uses readable source labels for failure source rows', () => {
    const rows = buildFailureSourceRowsFromAnalytics(
      [
        {
          source_hash: 'source-hash',
          auth_index: 'auth-1',
          calls: 10,
          failure: 2,
          last_seen_ms: Date.UTC(2026, 4, 20, 1, 2, 3),
          average_latency_ms: 120,
        },
      ],
      authMetaMap,
      authFileMap,
      sourceInfoMap,
      channelByAuthIndex
    );

    expect(rows[0].label).toBe('Team Auth');
    expect(rows[0].channel).toBe('Production Relay');
    expect(rows[0].label).not.toBe('source-hash');
  });
});

const meta = (
  authIndex: string,
  bucket: string,
  provider = 'codex'
): [string, MonitoringAuthMeta] => [
  authIndex,
  {
    authIndex,
    label: authIndex,
    account: authIndex,
    provider,
    status: 'active',
    disabled: false,
    unavailable: false,
    runtimeOnly: false,
    planType: 'pro',
    bucket,
    updatedAt: '',
  },
];

describe('buildAnalyticsFilters bucket', () => {
  const authMetaMap = new Map([meta('1', 'anon'), meta('2', 'anon'), meta('3', '')]);

  it('expands a bucket into its auth indices', () => {
    const filters = buildAnalyticsFilters({ bucket: 'anon' }, authMetaMap, []);
    expect(filters.auth_indices).toEqual(['1', '2']);
  });

  it('expands the untagged sentinel', () => {
    const filters = buildAnalyticsFilters({ bucket: UNTAGGED_BUCKET_FILTER }, authMetaMap, []);
    expect(filters.auth_indices).toEqual(['3']);
  });

  it('yields the no-match sentinel for an empty bucket', () => {
    const filters = buildAnalyticsFilters({ bucket: 'ghost' }, authMetaMap, []);
    expect(filters.auth_indices).toEqual(['__no_matching_auth_index__']);
  });

  it('applies no constraint when unset', () => {
    expect(buildAnalyticsFilters({}, authMetaMap, []).auth_indices).toBeUndefined();
  });

  it('matches bucket names case-sensitively, keeping Anon and anon as distinct pools', () => {
    // CPA routes on an exact, case-sensitive comparison
    // (sdk/cliproxy/auth/conductor_selection.go), so a case-insensitive match
    // here would silently merge two pools CPA keeps separate.
    const caseAuthMetaMap = new Map([meta('1', 'Anon'), meta('2', 'anon')]);
    expect(buildAnalyticsFilters({ bucket: 'Anon' }, caseAuthMetaMap, []).auth_indices).toEqual([
      '1',
    ]);
    expect(buildAnalyticsFilters({ bucket: 'anon' }, caseAuthMetaMap, []).auth_indices).toEqual([
      '2',
    ]);
  });

  // '1' and '2' are codex+anon, '3' is claude+anon (wrong provider), '4' is
  // codex+untagged (wrong bucket) — shared by both tests below.
  const combinedAuthMetaMap = new Map([
    meta('1', 'anon'),
    meta('2', 'anon'),
    meta('3', 'anon', 'claude'),
    meta('4', '', 'codex'),
  ]);

  it('intersects with an already-active account constraint instead of no-opping', () => {
    // Pins the intersection semantics: the result is neither the
    // provider-alone set (['1', '2', '4']) nor the bucket-alone set
    // (['1', '2', '3']), only their true intersection. Note this case alone
    // does NOT guard the addAuthIndexConstraint no-op-on-empty regression —
    // bucket's own match list here is non-empty, so both the correct
    // intersection and the buggy no-op-on-empty implementation happen to
    // produce the same ['1', '2'] for this fixture. See the 'ghost' bucket
    // case below for the test that actually discriminates the regression.
    const filters = buildAnalyticsFilters(
      { provider: 'codex', bucket: 'anon' },
      combinedAuthMetaMap,
      []
    );

    expect(filters.auth_indices).toEqual(['1', '2']);
  });

  it('collapses to the no-match sentinel when a matching bucket is combined with an empty-match one', () => {
    // 'ghost' matches no accounts, so bucket's own list is empty here. This
    // is the case that actually discriminates a regression to
    // addAuthIndexConstraint for this branch: its `if (next.size === 0)
    // return current` guard would fire and silently return the
    // provider-alone set (['1', '2', '4']) instead of collapsing to the
    // sentinel — i.e. the bucket constraint would be dropped rather than
    // intersected.
    const filters = buildAnalyticsFilters(
      { provider: 'codex', bucket: 'ghost' },
      combinedAuthMetaMap,
      []
    );

    expect(filters.auth_indices).toEqual(['__no_matching_auth_index__']);
  });
});
