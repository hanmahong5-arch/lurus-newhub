/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { ModelEntry } from './catalog';

// Presentation helpers for the success-rate / latency badges. The data is the
// tenant's own p50 / p95 / error rate (GET ~/models/performance); there is no
// first-token latency, throughput or time series behind it, so none is shown.

export type SuccessRateLevel =
  | 'excellent'
  | 'good'
  | 'warning'
  | 'critical'
  | 'unknown';

const EXCELLENT_MIN = 99.9;
const GOOD_MIN = 99;
const WARNING_MIN = 95;

/** Grade a success rate given as a percentage (0-100). */
export function getSuccessRateLevel(rate: number | null): SuccessRateLevel {
  if (rate == null || !Number.isFinite(rate)) return 'unknown';
  if (rate >= EXCELLENT_MIN) return 'excellent';
  if (rate >= GOOD_MIN) return 'good';
  if (rate >= WARNING_MIN) return 'warning';
  return 'critical';
}

const TEXT_CLASS: Record<SuccessRateLevel, string> = {
  excellent: 'text-emerald-600 dark:text-emerald-400',
  good: 'text-emerald-500 dark:text-emerald-300',
  warning: 'text-amber-600 dark:text-amber-400',
  critical: 'text-red-600 dark:text-red-400',
  unknown: 'text-muted-foreground',
};

const DOT_CLASS: Record<SuccessRateLevel, string> = {
  excellent: 'bg-emerald-500',
  good: 'bg-emerald-400',
  warning: 'bg-amber-500',
  critical: 'bg-red-500',
  unknown: 'bg-muted-foreground/40',
};

export const successRateTextClass = (rate: number | null): string =>
  TEXT_CLASS[getSuccessRateLevel(rate)];

export const successRateDotClass = (rate: number | null): string =>
  DOT_CLASS[getSuccessRateLevel(rate)];

/**
 * Success rate in percent, or null when it is not a measurement: the model
 * has no traffic, or too few samples (the server flags those). An error rate
 * outside 0..1 is rejected rather than clamped.
 */
export function successRateOf(
  e: Pick<ModelEntry, 'errorRate' | 'enoughSamples'>,
): number | null {
  if (!e.enoughSamples || e.errorRate == null) return null;
  if (!Number.isFinite(e.errorRate) || e.errorRate < 0 || e.errorRate > 1) {
    return null;
  }
  return (1 - e.errorRate) * 100;
}

/** 99.04 -> "99.04%"; null -> an em dash. */
export function formatSuccessRate(rate: number | null): string {
  if (rate == null) return '\u2014';
  return `${rate.toFixed(2)}%`;
}

/** 1234 -> "1.23s", 850 -> "850ms", missing or non-positive -> an em dash. */
export function formatLatency(ms: number | null | undefined): string {
  if (ms == null || !Number.isFinite(ms) || ms <= 0) return '\u2014';
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`;
  return `${Math.round(ms)}ms`;
}

/** Windows the performance endpoint accepts (hours). */
export const PERF_WINDOWS = [1, 24, 168] as const;
export type PerfWindow = (typeof PERF_WINDOWS)[number];
