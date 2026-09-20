import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  buildCandidateUsageSourceIds,
  calculateCacheHitRate,
  calculateCacheHitRateFromTotals,
  collectUsageDetails,
  collectUsageDetailsWithEndpoint,
  compatibleCachedTokens,
  extractTotalTokens,
  formatCompactNumber,
  formatCostPair,
  formatUsd,
  inferCacheInputMode,
  loadModelPrices,
  normalizeAnalyticsModel,
  normalizeCacheAccounting,
  normalizeUsageSourceId,
} from './usage';
import { maskSensitiveText } from './format';
import cacheInputAccountingFixtures from './cacheInputAccounting.fixtures.json';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('formatCompactNumber', () => {
  it('keeps large values compact as data grows beyond millions', () => {
    expect(formatCompactNumber(999)).toBe('999');
    expect(formatCompactNumber(1_200)).toBe('1.2K');
    expect(formatCompactNumber(999_950)).toBe('1.0M');
    expect(formatCompactNumber(2_795_200_000)).toBe('2.8B');
    expect(formatCompactNumber(1_200_000_000_000)).toBe('1.2T');
    expect(formatCompactNumber(-2_500_000_000_000_000)).toBe('-2.5P');
    expect(formatCompactNumber(Number.POSITIVE_INFINITY)).toBe('0');
  });
});

describe('formatUsd', () => {
  it('formats costs globally to two decimal places', () => {
    expect(formatUsd(19.99)).toBe('$19.99');
    expect(formatUsd(0.006)).toBe('$0.01');
    expect(formatUsd(Number.NaN)).toBe('$0.00');
  });

  it('allows request-scoped precision overrides', () => {
    expect(formatUsd(19.99, 3)).toBe('$19.990');
    expect(formatUsd(0.0006, 3)).toBe('$0.001');
    expect(formatUsd(Number.NaN, 3)).toBe('$0.000');
  });
});

describe('formatCostPair', () => {
  it('shows only the currency that exists', () => {
    expect(formatCostPair({ cny: 1234.56 })).toBe('¥1,234.56');
    expect(formatCostPair({ usd: 12.34 })).toBe('$12.34');
    expect(formatCostPair({ usd: 12.34, cny: 0 })).toBe('$12.34');
    expect(formatCostPair({ usd: null, cny: 1234.56 })).toBe('¥1,234.56');
  });

  it('shows both currencies without converting or merging them', () => {
    expect(formatCostPair({ cny: 1234.56, usd: 12.34 })).toBe('¥1,234.56 · $12.34');
  });

  it('falls back to a placeholder when neither currency has a value', () => {
    expect(formatCostPair({})).toBe('--');
    expect(formatCostPair({ usd: 0, cny: 0 })).toBe('--');
    expect(formatCostPair({ usd: Number.NaN, cny: undefined })).toBe('--');
  });

  it('honours request-scoped precision', () => {
    expect(formatCostPair({ usd: 0.0006, cny: 0.0012 }, 3)).toBe('¥0.001 · $0.001');
  });
});

describe('usage source candidates', () => {
  it('includes the masked source emitted by CPA for raw upstream keys', () => {
    expect(buildCandidateUsageSourceIds({ apiKey: 'sk-1234567890abcdef' })).toContain(
      'm:sk-1...cdef'
    );
  });

  it('aligns short secret masking with the backend source contract', () => {
    expect(buildCandidateUsageSourceIds({ apiKey: 'sk-12345' })).toContain('m:****');
  });

  it('preserves already-normalized masked usage event sources', () => {
    const usageData = {
      apis: {
        'POST /v1/responses': {
          models: {
            'gpt-5.5': {
              details: [
                {
                  timestamp: '2026-05-26T10:00:00Z',
                  source: 'm:sk-1...cdef',
                  auth_index: '',
                  tokens: {},
                  failed: false,
                },
              ],
            },
          },
        },
      },
    };

    expect(collectUsageDetails(usageData)[0].source).toBe('m:sk-1...cdef');
  });

  it('does not trust text-prefixed raw API key sources', () => {
    const sourceId = buildCandidateUsageSourceIds({ prefix: 'codex' })[0];
    expect(sourceId).toBe('t:codex');

    const usageData = {
      apis: {
        'POST /v1/responses': {
          models: {
            'gpt-5.5': {
              details: [
                {
                  timestamp: '2026-05-26T10:00:00Z',
                  source: 't:sk-1234567890abcdef',
                  auth_index: '',
                  tokens: {},
                  failed: false,
                },
              ],
            },
          },
        },
      },
    };

    const normalized = collectUsageDetails(usageData)[0].source;
    expect(normalized).toMatch(/^k:/);
    expect(normalized).not.toContain('sk-1234567890abcdef');
  });

  it('does not trust abnormal masked sources that contain raw secrets', () => {
    const normalized = normalizeUsageSourceId('m:sk-realsecret');

    expect(normalized).toMatch(/^k:/);
    expect(normalized).not.toContain('sk-realsecret');
  });

  it('preserves legacy UI-masked source IDs when no raw secret is present', () => {
    expect(normalizeUsageSourceId('m:sk******ef')).toBe('m:sk******ef');
  });
});

describe('normalizeAnalyticsModel', () => {
  it('removes only supported CPA reasoning suffixes', () => {
    expect(normalizeAnalyticsModel('deepseek-v4-flash(max)')).toBe('deepseek-v4-flash');
    expect(normalizeAnalyticsModel('gemini-2.5-pro(+08192)')).toBe('gemini-2.5-pro');
    expect(normalizeAnalyticsModel('gemini-2.5-pro(-000)')).toBe('gemini-2.5-pro');
    expect(normalizeAnalyticsModel('custom(model)(HIGH)')).toBe('custom(model)');
    expect(normalizeAnalyticsModel('custom-model(region-us)')).toBe('custom-model(region-us)');
    expect(normalizeAnalyticsModel('custom-model(9223372036854775808)')).toBe(
      'custom-model(9223372036854775808)'
    );
    expect(normalizeAnalyticsModel(' custom-model(max) ')).toBe(' custom-model(max) ');
  });
});

describe('usage detail collection', () => {
  it('preserves Codex identity from legacy auth type metadata', () => {
    const usageData = {
      apis: {
        'POST /v1/responses': {
          models: {
            'gpt-5.4': {
              details: [
                {
                  timestamp: '2026-07-20T00:00:00Z',
                  source: 'codex-account',
                  auth_index: 'auth-1',
                  auth_type: 'codex',
                  request_service_tier: 'priority',
                  response_service_tier: 'default',
                  tokens: { input_tokens: 100_000 },
                  failed: false,
                },
              ],
            },
          },
        },
      },
    };

    for (const detail of [
      collectUsageDetails(usageData)[0],
      collectUsageDetailsWithEndpoint(usageData)[0],
    ]) {
      expect(detail.auth_type).toBe('codex');
      expect(detail.provider).toBe('codex');
      expect(detail.request_service_tier).toBe('priority');
    }
  });

  it('copies project id snapshots into normalized usage details', () => {
    const usageData = {
      apis: {
        'POST /v1/chat/completions': {
          models: {
            'gemini-2.5-pro': {
              details: [
                {
                  timestamp: '2026-05-09T01:12:43.000Z',
                  source: 'alice@example.com',
                  auth_index: 'auth-1',
                  auth_project_id_snapshot: 'vertex-project-42',
                  tokens: {
                    input_tokens: 10,
                    output_tokens: 5,
                  },
                  failed: false,
                },
              ],
            },
          },
        },
      },
    };

    expect(collectUsageDetails(usageData)[0].auth_project_id_snapshot).toBe('vertex-project-42');
    expect(collectUsageDetailsWithEndpoint(usageData)[0].auth_project_id_snapshot).toBe(
      'vertex-project-42'
    );
  });

  it('accepts camelCase project id snapshots from usage details', () => {
    const usageData = {
      apis: {
        'POST /v1/chat/completions': {
          models: {
            'gemini-2.5-pro': {
              details: [
                {
                  timestamp: '2026-05-09T01:12:43.000Z',
                  source: 'alice@example.com',
                  authIndex: 'auth-1',
                  authProjectIdSnapshot: 'camel-project-42',
                  tokens: {},
                  failed: false,
                },
              ],
            },
          },
        },
      },
    };

    expect(collectUsageDetails(usageData)[0].auth_project_id_snapshot).toBe('camel-project-42');
    expect(collectUsageDetailsWithEndpoint(usageData)[0].auth_project_id_snapshot).toBe(
      'camel-project-42'
    );
  });

  it('extracts analytics, requested, and resolved model identities', () => {
    const usageData = {
      apis: {
        'POST /v1/chat/completions': {
          models: {
            'gpt-5.4': {
              details: [
                {
                  timestamp: '2026-05-19T10:00:00Z',
                  source: 'alice@example.com',
                  auth_index: 'auth-1',
                  analytics_model: 'gpt-5',
                  requested_model: 'gpt-5.4(max)',
                  resolved_model: 'gpt-5.5',
                  tokens: { input_tokens: 1 },
                  failed: false,
                },
              ],
            },
          },
        },
      },
    };

    const detail = collectUsageDetails(usageData)[0];
    expect(detail.__modelName).toBe('gpt-5.4');
    expect(detail.__requestedModel).toBe('gpt-5.4(max)');
    expect(detail.__resolvedModel).toBe('gpt-5.5');
    expect(collectUsageDetailsWithEndpoint(usageData)[0]).toMatchObject({
      __modelName: 'gpt-5.4',
      __requestedModel: 'gpt-5.4(max)',
      __resolvedModel: 'gpt-5.5',
    });
  });

  it('derives analytics identity for legacy payloads without analytics_model', () => {
    const usageData = {
      apis: {
        'POST /v1/chat/completions': {
          models: {
            'deepseek-v4-flash(max)': {
              details: [
                {
                  timestamp: '2026-05-19T10:00:00Z',
                  tokens: { input_tokens: 1 },
                  failed: false,
                },
              ],
            },
            'custom-model(region-us)': {
              details: [
                {
                  timestamp: '2026-05-19T10:00:01Z',
                  tokens: { input_tokens: 1 },
                  failed: false,
                },
              ],
            },
          },
        },
      },
    };

    expect(collectUsageDetailsWithEndpoint(usageData)).toEqual([
      expect.objectContaining({
        __modelName: 'deepseek-v4-flash',
        __requestedModel: 'deepseek-v4-flash(max)',
      }),
      expect.objectContaining({
        __modelName: 'custom-model(region-us)',
        __requestedModel: 'custom-model(region-us)',
      }),
    ]);
  });

  it('copies TTFT metadata into normalized usage details', () => {
    const usageData = {
      apis: {
        'POST /v1/chat/completions': {
          models: {
            'gpt-5.4': {
              details: [
                {
                  timestamp: '2026-05-19T10:00:00Z',
                  source: 'alice@example.com',
                  auth_index: 'auth-1',
                  latency_ms: 1500,
                  ttft_ms: 450,
                  tokens: { output_tokens: 20 },
                  failed: false,
                },
              ],
            },
          },
        },
      },
    };

    expect(collectUsageDetails(usageData)[0].ttft_ms).toBe(450);
    expect(collectUsageDetailsWithEndpoint(usageData)[0].ttft_ms).toBe(450);
  });

  it('normalizes CPA mirrored cached tokens without double counting fine-grained cache', () => {
    const usageData = {
      apis: {
        'POST /v1/messages': {
          models: {
            'claude-sonnet': {
              details: [
                {
                  timestamp: '2026-05-19T10:00:00Z',
                  source: 'alice@example.com',
                  auth_index: 'auth-1',
                  tokens: {
                    input_tokens: 100,
                    output_tokens: 20,
                    cached_tokens: 500,
                    cache_read_tokens: 500,
                  },
                  failed: false,
                },
              ],
            },
          },
        },
      },
    };

    const detail = collectUsageDetailsWithEndpoint(usageData)[0];

    expect(detail.tokens.cached_tokens).toBe(0);
    expect(detail.tokens.cache_read_tokens).toBe(500);
  });

  it('normalizes Anthropic cache input token fields', () => {
    const usageData = {
      apis: {
        'POST /v1/messages': {
          models: {
            'claude-sonnet': {
              details: [
                {
                  timestamp: '2026-05-19T10:00:00Z',
                  source: 'alice@example.com',
                  auth_index: 'auth-1',
                  tokens: {
                    input_tokens: 100,
                    output_tokens: 20,
                    cached_tokens: 34,
                    cache_creation_input_tokens: 11,
                    cache_read_input_tokens: 23,
                  },
                  failed: false,
                },
              ],
            },
          },
        },
      },
    };

    const detail = collectUsageDetailsWithEndpoint(usageData)[0];

    expect(detail.tokens.cached_tokens).toBe(0);
    expect(detail.tokens.cache_creation_tokens).toBe(11);
    expect(detail.tokens.cache_read_tokens).toBe(23);
    expect(detail.tokens.total_tokens).toBe(154);
  });
});

describe('usage token helpers', () => {
  it('keeps legacy cached tokens separate from fine-grained cache buckets', () => {
    expect(compatibleCachedTokens(5, 0, 4, 1)).toBe(0);
    expect(compatibleCachedTokens(10, 0, 4, 1)).toBe(5);
    expect(compatibleCachedTokens(0, 8, 3, 0)).toBe(5);
  });

  it('normalizes cache hit rates across legacy, Anthropic, and GPT-5.6 usage', () => {
    expect(
      calculateCacheHitRate({
        modelName: 'gpt-5.4',
        inputTokens: 1_000,
        cachedTokens: 400,
        cacheReadTokens: 0,
        cacheCreationTokens: 0,
      })
    ).toBeCloseTo(0.4, 6);
    expect(
      calculateCacheHitRate({
        modelName: 'claude-sonnet-4',
        inputTokens: 450,
        cachedTokens: 0,
        cacheReadTokens: 300,
        cacheCreationTokens: 50,
      })
    ).toBeCloseTo(300 / 450, 6);
    expect(
      calculateCacheHitRate({
        modelName: 'openai/gpt-5.6-sol',
        inputTokens: 152_600,
        cachedTokens: 0,
        cacheReadTokens: 151_000,
        cacheCreationTokens: 1_000,
      })
    ).toBeCloseTo(151_000 / 152_600, 6);
  });

  it('clamps aggregated malformed cache ratios to 100%', () => {
    expect(calculateCacheHitRateFromTotals(1_500, 1_000)).toBe(1);
  });

  it('uses fine-grained cache fields when total tokens are missing', () => {
    expect(
      extractTotalTokens({
        tokens: {
          input_tokens: 10,
          output_tokens: 20,
          reasoning_tokens: 3,
          cached_tokens: 10,
          cache_read_tokens: 4,
          cache_creation_tokens: 1,
        },
      })
    ).toBe(43);
  });

  it('uses Anthropic cache input fields when total tokens are missing', () => {
    expect(
      extractTotalTokens({
        tokens: {
          input_tokens: 100,
          output_tokens: 20,
          cached_tokens: 34,
          cache_read_input_tokens: 23,
          cache_creation_input_tokens: 11,
        },
      })
    ).toBe(154);
  });
});

describe('cache input accounting semantics', () => {
  it.each(cacheInputAccountingFixtures)('matches shared fixture: $name', (fixture) => {
    const accounting = normalizeCacheAccounting({
      context: fixture.context,
      inputTokens: fixture.tokens.input,
      cachedTokens: fixture.tokens.cached,
      cacheTokens: fixture.tokens.cache,
      cacheReadTokens: fixture.tokens.read,
      cacheCreationTokens: fixture.tokens.creation,
    });

    expect(accounting).toMatchObject({
      mode: fixture.expected.mode,
      uncachedInputTokens: fixture.expected.uncached,
      totalInputTokens: fixture.expected.totalInput,
      cacheCreationTokens: fixture.expected.cacheCreation,
    });
    expect(accounting.legacyRead + accounting.cacheReadTokens).toBe(fixture.expected.cacheRead);
  });

  it.each([
    {
      name: 'OpenAICompat executor beats Claude alias',
      context: { executorType: 'OpenAICompatExecutor', resolvedModel: 'claude-sonnet-4' },
      mode: 'included_in_input',
    },
    {
      name: 'Claude executor beats Grok alias',
      context: { executorType: 'ClaudeExecutor', resolvedModel: 'grok-4' },
      mode: 'separate_from_input',
    },
    {
      name: 'XAI executor beats Claude alias',
      context: { executorType: 'XAIWebsocketsExecutor', displayModel: 'claude-alias' },
      mode: 'included_in_input',
    },
    {
      name: 'provider snapshot beats model',
      context: { providerSnapshot: 'moonshot', resolvedModel: 'claude-sonnet' },
      mode: 'included_in_input',
    },
    {
      name: 'resolved model beats requested model',
      context: { resolvedModel: 'claude-sonnet', requestedModel: 'gpt-5' },
      mode: 'separate_from_input',
    },
  ])('$name', ({ context, mode }) => {
    expect(inferCacheInputMode(context, 20, 10)).toBe(mode);
  });

  it('keeps a valid explicit mode above executor classification', () => {
    expect(
      inferCacheInputMode(
        { explicitMode: 'separate_from_input', executorType: 'XAIExecutor' },
        20,
        0
      )
    ).toBe('separate_from_input');
  });

  it('normalizes included and separate totals with the mirrored Go formulas', () => {
    expect(
      normalizeCacheAccounting({
        context: { executorType: 'XAIExecutor' },
        inputTokens: 100,
        cachedTokens: 0,
        cacheTokens: 0,
        cacheReadTokens: 20,
        cacheCreationTokens: 10,
      })
    ).toMatchObject({
      mode: 'included_in_input',
      uncachedInputTokens: 70,
      totalInputTokens: 100,
    });
    expect(
      normalizeCacheAccounting({
        context: { executorType: 'ClaudeExecutor' },
        inputTokens: 100,
        cachedTokens: 0,
        cacheTokens: 0,
        cacheReadTokens: 20,
        cacheCreationTokens: 10,
      })
    ).toMatchObject({
      mode: 'separate_from_input',
      uncachedInputTokens: 100,
      totalInputTokens: 130,
    });
  });

  it.each([
    {
      name: 'xAI included input',
      model: 'grok-4',
      detail: { executor_type: 'XAIExecutor' },
      totalInput: 100,
    },
    {
      name: 'Kimi provider included input',
      model: 'claude-alias',
      detail: { provider: 'moonshot' },
      totalInput: 100,
    },
    {
      name: 'Claude executor separate input',
      model: 'grok-alias',
      detail: { executor_type: 'ClaudeExecutor' },
      totalInput: 130,
    },
    {
      name: 'nested explicit mode',
      model: 'grok-4',
      detail: {},
      tokenMode: 'separate_from_input',
      totalInput: 130,
    },
  ])('$name is applied by readTokens', ({ model, detail, tokenMode, totalInput }) => {
    const usageData = {
      apis: {
        'POST /v1/chat/completions': {
          models: {
            [model]: {
              details: [
                {
                  timestamp: '2026-07-15T00:00:00Z',
                  source: 'account',
                  auth_index: 'auth-1',
                  ...detail,
                  tokens: {
                    input_tokens: 100,
                    cache_read_tokens: 20,
                    cache_creation_tokens: 10,
                    cache_input_mode: tokenMode,
                  },
                  failed: false,
                },
              ],
            },
          },
        },
      },
    };
    const [normalized] = collectUsageDetails(usageData);

    expect(normalized.tokens.input_tokens).toBe(totalInput);
    expect(normalized.tokens.total_tokens).toBe(totalInput);
  });

  it('normalizes xAI cache without double-counting included input', () => {
    const usageData = {
      apis: {
        'POST /v1/chat/completions': {
          models: {
            'grok-4': {
              details: [
                {
                  timestamp: '2026-07-15T00:00:00Z',
                  source: 'account',
                  auth_index: 'auth-1',
                  executor_type: 'XAIExecutor',
                  tokens: { input_tokens: 100, cache_read_tokens: 40 },
                  failed: false,
                },
              ],
            },
          },
        },
      },
    };
    const [detail] = collectUsageDetails(usageData);

    expect(detail.tokens.input_tokens).toBe(100);
    expect(
      calculateCacheHitRate({
        inputTokens: detail.tokens.input_tokens,
        cachedTokens: detail.tokens.cached_tokens,
        cacheReadTokens: detail.tokens.cache_read_tokens,
        cacheCreationTokens: detail.tokens.cache_creation_tokens,
      })
    ).toBeCloseTo(0.4);
  });
});

describe('sensitive text masking', () => {
  it('does not redact ordinary AI-prefixed diagnostics or swallow JSON after cookie fields', () => {
    const text = `AImproved fallback AIServer down {"cookie":"session=secret","status":"401","detail":"upstream denied","retry_after":30}`;
    const masked = maskSensitiveText(text);

    expect(masked).toContain('AImproved fallback');
    expect(masked).toContain('AIServer down');
    expect(masked).toContain('"status":"401"');
    expect(masked).toContain('"detail":"upstream denied"');
    expect(masked).toContain('"retry_after":30');
    expect(masked).not.toContain('session=secret');
  });
});

describe('model price storage', () => {
  it('normalizes persisted service-tier rules and rejects ambiguous aliases', () => {
    const stored = {
      'gpt-valid': {
        prompt: 5,
        completion: 30,
        cache: 0.5,
        serviceTiers: [
          {
            mode: ' FAST ',
            serviceTier: ' PRIORITY ',
            prompt: 12.5,
            completion: 75,
            cache: 0,
            promptConfigured: true,
            completionConfigured: true,
          },
        ],
      },
      'gpt-ambiguous': {
        prompt: 5,
        completion: 30,
        cache: 0.5,
        serviceTiers: [
          {
            mode: 'fast',
            serviceTier: 'priority',
            prompt: 12.5,
            completion: 75,
            cache: 0,
            promptConfigured: true,
          },
          {
            mode: 'priority',
            serviceTier: 'turbo',
            prompt: 15,
            completion: 80,
            cache: 0,
            promptConfigured: true,
          },
        ],
      },
    };
    vi.stubGlobal('localStorage', {
      getItem: (key: string) =>
        key === 'cli-proxy-model-prices-v2' ? JSON.stringify(stored) : null,
    });

    const prices = loadModelPrices();
    expect(prices['gpt-valid'].serviceTiers).toEqual([
      expect.objectContaining({ mode: 'fast', serviceTier: 'priority', prompt: 12.5 }),
    ]);
    expect(prices['gpt-ambiguous'].serviceTiers).toBeUndefined();
  });
});
