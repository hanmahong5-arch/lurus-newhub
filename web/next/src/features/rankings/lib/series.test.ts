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
import { describe, expect, it } from 'vitest';

import type { RankingRow, RankingSeriesPoint } from './api';
import { formatSharePct, formatTokens, movers } from './format';
import { hoursOf, PERIODS } from './periods';
import { bucketSeconds, bucketSeries, toShareRows } from './series';

const H = 3600;
const point = (
  t: number,
  name: string,
  tokens: number,
): RankingSeriesPoint => ({ t, name, tokens, requests: 1, quota: 0 });

describe('periods', () => {
  it('maps tabs to hours the endpoint serves, with no yearly window', () => {
    expect(PERIODS.map((p) => p.hours)).toEqual([24, 168, 720]);
    expect(hoursOf('today')).toBe(24);
    expect(hoursOf('month')).toBe(720);
    expect(PERIODS.map((p) => p.id)).not.toContain('year');
  });
});

describe('bucketSeries', () => {
  const opts = { hours: 24, otherLabel: 'Other', locale: 'en-US' };

  it('fills hours without traffic with real zeros', () => {
    const start = 1_700_000_000 - (1_700_000_000 % H);
    const out = bucketSeries(
      [point(start, 'a', 10), point(start + 3 * H, 'a', 5)],
      { ...opts, window: { start, end: start + 3 * H } },
    );
    expect(out.rows.map((r) => r.s0)).toEqual([10, 0, 0, 5]);
    expect(out.rows.map((r) => r.total)).toEqual([10, 0, 0, 5]);
  });

  it('keeps the biggest names and merges the rest into "other"', () => {
    const start = 1_700_000_000 - (1_700_000_000 % H);
    const out = bucketSeries(
      [
        point(start, 'big', 100),
        point(start, 'mid', 50),
        point(start, 'tiny-1', 3),
        point(start, 'tiny-2', 2),
      ],
      { ...opts, maxSeries: 2 },
    );
    expect(out.keys.map((k) => k.name)).toEqual(['big', 'mid', 'Other']);
    expect(out.keys[2].other).toBe(true);
    expect(out.keys[2].tokens).toBe(5);
    expect(out.rows[0].s2).toBe(5);
    expect(out.rows[0].total).toBe(155);
  });

  it('drops points outside the requested window instead of misplacing them', () => {
    const start = 1_700_000_000 - (1_700_000_000 % H);
    const out = bucketSeries(
      [point(start, 'a', 1), point(start + 40 * H, 'a', 99)],
      { ...opts, window: { start, end: start + 2 * H } },
    );
    expect(out.rows.reduce((s, r) => s + r.total, 0)).toBe(1);
  });

  it('returns nothing for an empty series', () => {
    expect(bucketSeries([], opts)).toEqual({ keys: [], rows: [] });
  });

  it('sizes buckets so every window draws a readable bar count', () => {
    expect(bucketSeconds(24)).toBe(H);
    expect(bucketSeconds(168)).toBe(6 * H);
    expect(bucketSeconds(720)).toBe(24 * H);
  });

  it('normalises each bar to a share; an empty bar stays zero', () => {
    const start = 1_700_000_000 - (1_700_000_000 % H);
    const out = bucketSeries(
      [point(start, 'a', 30), point(start, 'b', 10), point(start + 2 * H, 'a', 4)],
      { ...opts, window: { start, end: start + 2 * H } },
    );
    const share = toShareRows(out.rows, out.keys);
    expect(share[0].s0).toBeCloseTo(0.75);
    expect(share[0].s1).toBeCloseTo(0.25);
    expect(share[1].s0).toBe(0);
    expect(share[2].s0).toBe(1);
  });
});

describe('format helpers', () => {
  it('formats tokens and shares compactly', () => {
    expect(formatTokens(0)).toBe('0');
    expect(formatTokens(512)).toBe('512');
    expect(formatTokens(1500)).toBe('1.5K');
    expect(formatTokens(2_500_000)).toBe('2.50M');
    expect(formatTokens(3_000_000_000)).toBe('3.00B');
    expect(formatSharePct(0)).toBe('0%');
    expect(formatSharePct(0.04)).toBe('<0.1%');
    expect(formatSharePct(12.34)).toBe('12.3%');
  });

  it('splits climbers, fallers and newcomers', () => {
    const row = (name: string, rank: number, delta: number, isNew = false) =>
      ({
        name,
        rank,
        rank_delta: delta,
        is_new: isNew,
        requests: 1,
        total_tokens: 1,
        quota: 1,
        token_share_pct: 1,
        quota_share_pct: 1,
        requests_growth_pct: null,
      }) satisfies RankingRow;
    const m = movers([
      row('a', 1, 3),
      row('b', 2, -2),
      row('c', 3, 0, true),
      row('d', 4, 1),
      row('e', 5, 0),
    ]);
    expect(m.up.map((r) => r.name)).toEqual(['a', 'd']);
    expect(m.down.map((r) => r.name)).toEqual(['b']);
    expect(m.fresh.map((r) => r.name)).toEqual(['c']);
  });
});
