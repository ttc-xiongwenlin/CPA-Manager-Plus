// Copied from UsageAnalyticsPage.tsx:159-292,373-399 — do not import from
// usage-analytics (upstream churn zone). Color tokens and surface values are
// kept byte-identical to the source so error-insight's charts render a
// consistent theme with usage-analytics's. The tooltip HTML helpers were
// parameterized to take the caller's own module.scss class name(s) instead
// of the source's hardcoded `styles.echartsTooltip` / `styles.echartsTooltipRow`.
import { useThemeStore } from '@/stores';

export type ErrorChartTheme = {
  axisColors: Record<'requests' | 'tokens' | 'cost', string>;
  categoryPalette: string[];
  heatmapColors: string[];
  healthColors: {
    failure: string;
    latency: string;
    success: string;
  };
  metricColors: Record<
    'cachedTokens' | 'estimatedCost' | 'inputTokens' | 'outputTokens' | 'requestCount' | 'totalTokens',
    string
  >;
  surface: {
    axisLabel: string;
    axisLine: string;
    axisPointer: string;
    barBackground: string;
    heatmapCellBorder: string;
    heatmapEmphasisBorder: string;
    pieBorder: string;
    pieShadow: string;
    selectedLine: string;
    splitLine: string;
    tooltipBackground: string;
    tooltipBorder: string;
    tooltipMuted: string;
    tooltipShadow: string;
    tooltipText: string;
  };
  tokenStructureColors: string[];
};

const lightErrorChartTheme: ErrorChartTheme = {
  axisColors: {
    requests: '#409eff',
    tokens: '#14b8a6',
    cost: '#f59e0b',
  },
  categoryPalette: ['#409eff', '#14b8a6', '#f59e0b', '#f56c6c', '#94a3b8'],
  heatmapColors: ['#eff6ff', '#93c5fd', '#409eff', '#0f766e'],
  healthColors: {
    failure: '#f56c6c',
    latency: '#0ea5e9',
    success: '#67c23a',
  },
  metricColors: {
    cachedTokens: '#06b6d4',
    estimatedCost: '#f59e0b',
    inputTokens: '#60a5fa',
    outputTokens: '#22c55e',
    requestCount: '#409eff',
    totalTokens: '#14b8a6',
  },
  surface: {
    axisLabel: '#5f6c7b',
    axisLine: '#d8e5f2',
    axisPointer: '#8b95a6',
    barBackground: 'rgba(139, 149, 166, 0.14)',
    heatmapCellBorder: '#ffffff',
    heatmapEmphasisBorder: '#2c3e50',
    pieBorder: '#ffffff',
    pieShadow: 'rgba(15, 23, 42, 0.18)',
    selectedLine: '#8b95a6',
    splitLine: '#d3e1ef',
    tooltipBackground: 'rgba(255, 255, 255, 0.96)',
    tooltipBorder: '#d8e5f2',
    tooltipMuted: '#5f6c7b',
    tooltipShadow: 'box-shadow: 0 16px 36px rgba(15, 23, 42, 0.14);',
    tooltipText: '#2c3e50',
  },
  tokenStructureColors: ['#60a5fa', '#22c55e', '#06b6d4', '#f59e0b'],
};

const darkErrorChartTheme: ErrorChartTheme = {
  axisColors: {
    requests: '#79bbff',
    tokens: '#2dd4bf',
    cost: '#fbbf24',
  },
  categoryPalette: ['#79bbff', '#2dd4bf', '#fbbf24', '#fab6b6', '#a3a6ad'],
  heatmapColors: ['#102f4f', '#1d5f98', '#409eff', '#79bbff'],
  healthColors: {
    failure: '#fab6b6',
    latency: '#7dd3fc',
    success: '#95d475',
  },
  metricColors: {
    cachedTokens: '#22d3ee',
    estimatedCost: '#fbbf24',
    inputTokens: '#60a5fa',
    outputTokens: '#95d475',
    requestCount: '#79bbff',
    totalTokens: '#2dd4bf',
  },
  surface: {
    axisLabel: '#a3a3a3',
    axisLine: 'rgba(255, 255, 255, 0.12)',
    axisPointer: '#7a7a7a',
    barBackground: 'rgba(255, 255, 255, 0.08)',
    heatmapCellBorder: '#1b1f2a',
    heatmapEmphasisBorder: '#e5e5e5',
    pieBorder: '#1b1f2a',
    pieShadow: 'rgba(0, 0, 0, 0.36)',
    selectedLine: '#7a7a7a',
    splitLine: 'rgba(255, 255, 255, 0.1)',
    tooltipBackground: 'rgba(24, 28, 40, 0.96)',
    tooltipBorder: 'rgba(255, 255, 255, 0.12)',
    tooltipMuted: '#a3a3a3',
    tooltipShadow: 'box-shadow: 0 16px 36px rgba(0, 0, 0, 0.38);',
    tooltipText: '#e5e5e5',
  },
  tokenStructureColors: ['#60a5fa', '#95d475', '#22d3ee', '#fbbf24'],
};

export const getErrorChartTheme = (resolvedTheme: 'light' | 'dark'): ErrorChartTheme =>
  resolvedTheme === 'dark' ? darkErrorChartTheme : lightErrorChartTheme;

export const useErrorChartTheme = () => {
  const resolvedTheme = useThemeStore((state) => state.resolvedTheme);
  return getErrorChartTheme(resolvedTheme);
};

export const appendHexAlpha = (color: string, alphaHex: string) =>
  /^#[\da-f]{6}$/i.test(color) ? `${color}${alphaHex}` : color;

export const escapeHtml = (value: string | number | null | undefined) =>
  String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');

export const tooltipTitleHtml = (chartTheme: ErrorChartTheme, titleHtml: string) =>
  `<b style="color:${chartTheme.surface.tooltipText}">${titleHtml}</b>`;

export const tooltipRowHtml = (
  chartTheme: ErrorChartTheme,
  rowClassName: string,
  labelHtml: string,
  valueHtml: string,
) =>
  `<div class="${rowClassName}" style="color:${chartTheme.surface.tooltipMuted}"><span>${labelHtml}</span><strong style="color:${chartTheme.surface.tooltipText}">${valueHtml}</strong></div>`;

export const tooltipHtml = (
  chartTheme: ErrorChartTheme,
  wrapperClassName: string,
  rowsHtml: string,
  titleHtml?: string | null,
) =>
  `<div class="${wrapperClassName}" style="color:${chartTheme.surface.tooltipMuted}">${
    titleHtml ? tooltipTitleHtml(chartTheme, titleHtml) : ''
  }${rowsHtml}</div>`;

export const getTooltipOption = (chartTheme: ErrorChartTheme) => ({
  backgroundColor: chartTheme.surface.tooltipBackground,
  borderColor: chartTheme.surface.tooltipBorder,
  extraCssText: chartTheme.surface.tooltipShadow,
  textStyle: {
    color: chartTheme.surface.tooltipMuted,
  },
});
