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
import type { RankingSeriesPoint } from './api';

const HOUR = 3600;
const DAY = 24 * HOUR;

/** Chart bucket size for a window: ~24-30 bars whatever the window. */
export function bucketSeconds(hours: number): number {
  if (hours <= 24) return HOUR;
  if (hours <= 168) return 6 * HOUR;
  return DAY;
}

export interface SeriesKey {
  /** Stable data key (s0, s1, ...); names may hold characters unfit for keys. */
  key: string;
  name: string;
  tokens: number;
  /** True for the merged tail of small series. */
  other: boolean;
}

export interface BucketRow {
  ts: number;
  label: string;
  total: number;
  [key: string]: number | string;
}

export interface BucketedSeries {
  keys: SeriesKey[];
  rows: BucketRow[];
}

export interface BucketOptions {
  hours: number;
  window?: { start: number; end: number };
  /** Names beyond this many (by tokens) are merged into one "other" series. */
  maxSeries?: number;
  otherLabel: string;
  locale?: string;
}

function labelFor(ts: number, bs: number, locale?: string): string {
  const d = new Date(ts * 1000);
  if (bs >= DAY) {
    return d.toLocaleDateString(locale, { month: 'short', day: 'numeric' });
  }
  if (bs >= 6 * HOUR) {
    return d.toLocaleString(locale, {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    });
  }
  return d.toLocaleTimeString(locale, {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
}

/**
 * Hourly per-name usage -> evenly spaced chart rows. Empty buckets are real
 * zeros (the server reports only hours with traffic), the biggest `maxSeries`
 * names keep their own column and the rest are summed into "other".
 */
export function bucketSeries(
  points: RankingSeriesPoint[],
  opts: BucketOptions,
): BucketedSeries {
  if (points.length === 0) return { keys: [], rows: [] };
  const bs = bucketSeconds(opts.hours);
  const maxSeries = Math.max(1, opts.maxSeries ?? 8);

  const totals = new Map<string, number>();
  for (const p of points) {
    totals.set(p.name, (totals.get(p.name) ?? 0) + (p.tokens || 0));
  }
  const ordered = [...totals.entries()].sort(
    (a, b) => b[1] - a[1] || a[0].localeCompare(b[0]),
  );
  const head = ordered.slice(0, maxSeries);
  const tail = ordered.slice(maxSeries);
  const keyOf = new Map<string, string>();
  const keys: SeriesKey[] = head.map(([name, tokens], i) => {
    keyOf.set(name, `s${i}`);
    return { key: `s${i}`, name, tokens, other: false };
  });
  if (tail.length > 0) {
    const key = `s${keys.length}`;
    for (const [name] of tail) keyOf.set(name, key);
    keys.push({
      key,
      name: opts.otherLabel,
      tokens: tail.reduce((s, [, n]) => s + n, 0),
      other: true,
    });
  }

  const times = points.map((p) => p.t);
  const first = opts.window?.start
    ? opts.window.start
    : Math.min(...times);
  const last = opts.window?.end ? opts.window.end : Math.max(...times);
  const start = Math.floor(first / bs) * bs;
  const end = Math.floor(last / bs) * bs;

  const byBucket = new Map<number, BucketRow>();
  for (let ts = start; ts <= end; ts += bs) {
    const row: BucketRow = {
      ts,
      label: labelFor(ts, bs, opts.locale),
      total: 0,
    };
    for (const k of keys) row[k.key] = 0;
    byBucket.set(ts, row);
  }
  for (const p of points) {
    const row = byBucket.get(Math.floor(p.t / bs) * bs);
    const key = keyOf.get(p.name);
    // A point outside the requested window is not part of this chart.
    if (!row || !key) continue;
    const n = p.tokens || 0;
    row[key] = (row[key] as number) + n;
    row.total += n;
  }
  return { keys, rows: [...byBucket.values()] };
}

/** Each row's columns as a fraction of that row's total (empty row: all 0). */
export function toShareRows(
  rows: BucketRow[],
  keys: SeriesKey[],
): BucketRow[] {
  return rows.map((r) => {
    const out: BucketRow = { ts: r.ts, label: r.label, total: r.total };
    for (const k of keys) {
      out[k.key] = r.total > 0 ? (r[k.key] as number) / r.total : 0;
    }
    return out;
  });
}

/** Series colours; stable per position so a name keeps its colour on re-render. */
export const SERIES_COLORS = [
  '#0ea5e9',
  '#22c55e',
  '#a855f7',
  '#f97316',
  '#14b8a6',
  '#eab308',
  '#ec4899',
  '#6366f1',
] as const;

const OTHER_COLOR = '#94a3b8';

export function colorAt(i: number, other: boolean): string {
  if (other) return OTHER_COLOR;
  return SERIES_COLORS[i % SERIES_COLORS.length];
}
