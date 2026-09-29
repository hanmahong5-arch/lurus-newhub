/*
Copyright (C) 2025 QuantumNous

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

import { localDayKey } from '../../../components/hifi/activitySeries';

const LOG_TYPE_CONSUME = 2;
const LOG_TYPE_ERROR = 5;

const readNum = (obj, ...keys) => {
  for (const k of keys) {
    const v = obj?.[k];
    if (typeof v === 'number' && !Number.isNaN(v)) return v;
  }
  return 0;
};

const logType = (l) => readNum(l, 'type', 'Type');
const logLatencyMs = (l) => readNum(l, 'total_latency_ms', 'TotalLatencyMs');
const logCreatedAt = (l) => readNum(l, 'created_at', 'CreatedAt');

export const computeQPS = (logs, windowSeconds) => {
  if (!Array.isArray(logs) || logs.length === 0) return 0;
  if (typeof windowSeconds !== 'number' || windowSeconds < 1) return 0;
  return logs.length / windowSeconds;
};

// Generic percentile across consume logs with positive latency. Returns null
// if no qualifying samples. Uses nearest-rank method; for p=50 with even N,
// averages the two middle values (kept for back-compat with prior computeLatencyP50).
export const computeLatencyPercentile = (logs, percentile) => {
  if (!Array.isArray(logs)) return null;
  if (typeof percentile !== 'number' || percentile < 0 || percentile > 100) {
    return null;
  }
  const samples = logs
    .filter((l) => logType(l) === LOG_TYPE_CONSUME && logLatencyMs(l) > 0)
    .map(logLatencyMs)
    .sort((a, b) => a - b);
  if (samples.length === 0) return null;
  if (percentile === 50) {
    const mid = Math.floor(samples.length / 2);
    return samples.length % 2 === 0
      ? Math.round((samples[mid - 1] + samples[mid]) / 2)
      : samples[mid];
  }
  // Nearest-rank: index = ceil(p/100 * N) - 1, clamped to [0, N-1].
  const idx = Math.min(
    samples.length - 1,
    Math.max(0, Math.ceil((percentile / 100) * samples.length) - 1),
  );
  return samples[idx];
};

export const computeLatencyP50 = (logs) => computeLatencyPercentile(logs, 50);
export const computeLatencyP95 = (logs) => computeLatencyPercentile(logs, 95);
export const computeLatencyP99 = (logs) => computeLatencyPercentile(logs, 99);

// Group consume logs by model_name, sum quota and count. Returns array
// sorted by totalQuota desc. Empty-input returns [].
// Quota units match Dashboard's existing QUOTA_PER_USD divisor (5e5).
const logModel = (l) =>
  l?.model || l?.ModelName || l?.model_name || l?.channel_name || '';
const logQuota = (l) => readNum(l, 'quota', 'Quota');

export const computeCostByModel = (logs) => {
  if (!Array.isArray(logs)) return [];
  const byModel = new Map();
  for (const l of logs) {
    if (logType(l) !== LOG_TYPE_CONSUME) continue;
    const m = logModel(l);
    if (!m) continue;
    const q = logQuota(l);
    if (q <= 0) continue;
    const prev = byModel.get(m) ?? { model: m, totalQuota: 0, requestCount: 0 };
    prev.totalQuota += q;
    prev.requestCount += 1;
    byModel.set(m, prev);
  }
  return [...byModel.values()].sort((a, b) => b.totalQuota - a.totalQuota);
};

export const computeErrorRate = (logs) => {
  if (!Array.isArray(logs)) return 0;
  let consume = 0;
  let error = 0;
  for (const l of logs) {
    const t = logType(l);
    if (t === LOG_TYPE_CONSUME) consume++;
    else if (t === LOG_TYPE_ERROR) error++;
  }
  const total = consume + error;
  return total === 0 ? 0 : error / total;
};

// Pick the most recent N entries by created_at. Defensive: works even when
// caller already pre-sorted desc, since sort is stable enough for ties.
export const pickRecent = (logs, n) => {
  if (!Array.isArray(logs)) return [];
  return [...logs]
    .sort((a, b) => logCreatedAt(b) - logCreatedAt(a))
    .slice(0, n);
};

export const formatLatencyMs = (ms) => {
  if (ms == null) return '—';
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
};

export const formatErrorRate = (rate) => {
  if (typeof rate !== 'number' || Number.isNaN(rate)) return '—';
  if (rate === 0) return '0%';
  const pct = rate * 100;
  return pct < 0.1 ? '<0.1%' : `${pct.toFixed(1)}%`;
};

export const formatQPS = (qps) => {
  if (typeof qps !== 'number' || Number.isNaN(qps)) return '—';
  if (qps === 0) return '0';
  if (qps < 0.01) return '<0.01';
  if (qps < 1) return qps.toFixed(2);
  if (qps < 10) return qps.toFixed(1);
  return Math.round(qps).toString();
};

export const DASHBOARD_REALTIME_WINDOW_SECONDS = 300; // 5 min

// ─── KPI-card trend + delta (cycle-19: sparkline + 环比) ──────────────────
//
// KpiCards draws a small sparkline + period-over-period delta under the
// all-time spend/request totals, sourced from the same /api/data/self/ rows
// HfActivityChart uses. The two pure pieces live here (not in
// components/hifi/activitySeries.js) because they are KPI-card concerns —
// a single summed-across-models daily series, not the per-model stack
// activitySeries builds — and kpis.js is already the home for this page's
// other derived-number math.

/**
 * Sum `field` per LOCAL calendar day (see activitySeries.localDayKey — keys
 * on the browser's clock, not UTC, for the same reason the activity chart
 * does) for the trailing `days` local days ending on `end`'s day. Always
 * returns exactly `days` entries, oldest first, zero-filled for days with no
 * rows — the sparkline needs a fixed-length series so it never reflows.
 *
 * @param {Array} rows        /api/data/self/ rows (quota, count, created_at)
 * @param {object} o
 * @param {number} o.end      window end, unix seconds
 * @param {number} o.days     how many local days, ending with o.end's day
 * @param {'quota'|'count'|'token_used'} [o.field='quota']
 * @returns {number[]}
 */
export function dailyTotals(rows, { end, days, field = 'quota' }) {
  if (!end || !days || days < 1) return [];
  const lastKey = localDayKey(end);
  const cursor = new Date(lastKey * 1000);
  cursor.setDate(cursor.getDate() - (days - 1));
  const firstKey = Math.floor(cursor.getTime() / 1000);

  const byDay = new Map();
  for (const row of rows || []) {
    const ts = Number(row?.created_at) || 0;
    if (!ts) continue;
    const key = localDayKey(ts);
    if (key < firstKey || key > lastKey) continue;
    const v = Number(row?.[field]) || 0;
    byDay.set(key, (byDay.get(key) || 0) + v);
  }

  const out = [];
  const walk = new Date(firstKey * 1000);
  for (let key = firstKey; key <= lastKey; ) {
    out.push(byDay.get(key) || 0);
    walk.setDate(walk.getDate() + 1);
    key = Math.floor(walk.getTime() / 1000);
  }
  return out;
}

// Sum of a plain number array — the one place dailyTotals' output gets
// collapsed to a single total for percentDelta below.
export const sumSeries = (values) =>
  (values || []).reduce((s, v) => s + (Number(v) || 0), 0);

/**
 * 环比 (period-over-period % change), signed: positive means `current` is
 * higher than `previous`. Returns `null` when there is nothing honest to
 * compare against — `previous` is 0, missing, or non-finite — rather than
 * fabricating a 0% or a divide-by-zero infinity. Both inputs are already-
 * summed totals (see sumSeries), not raw daily arrays.
 *
 * @param {number} current
 * @param {number} previous
 * @returns {number|null}
 */
export function percentDelta(current, previous) {
  if (typeof current !== 'number' || !Number.isFinite(current)) return null;
  if (typeof previous !== 'number' || !Number.isFinite(previous) || !previous) {
    return null;
  }
  return ((current - previous) / previous) * 100;
}
