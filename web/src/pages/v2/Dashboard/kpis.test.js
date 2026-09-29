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
import { describe, it, expect } from 'vitest';
import {
  computeQPS,
  computeLatencyP50,
  computeLatencyP95,
  computeLatencyP99,
  computeLatencyPercentile,
  computeErrorRate,
  computeCostByModel,
  pickRecent,
  formatLatencyMs,
  formatErrorRate,
  formatQPS,
  dailyTotals,
  sumSeries,
  percentDelta,
} from './kpis';

const consume = (ts, latency) => ({
  type: 2,
  total_latency_ms: latency,
  created_at: ts,
});
const errLog = (ts) => ({
  type: 5,
  total_latency_ms: 0,
  created_at: ts,
});

describe('Dashboard KPI derivations', () => {
  describe('computeQPS', () => {
    it('returns 0 on empty input', () => {
      expect(computeQPS([], 300)).toBe(0);
      expect(computeQPS(null, 300)).toBe(0);
    });

    it('returns count divided by window seconds', () => {
      const logs = [1, 2, 3, 4, 5].map((t) => consume(t, 100));
      expect(computeQPS(logs, 300)).toBeCloseTo(5 / 300, 5);
    });

    it('returns 0 if window is < 1 second (avoid divide-by-zero)', () => {
      expect(computeQPS([consume(1, 100)], 0)).toBe(0);
      expect(computeQPS([consume(1, 100)], -10)).toBe(0);
    });
  });

  describe('computeLatencyP50', () => {
    it('returns null on empty input', () => {
      expect(computeLatencyP50([])).toBeNull();
    });

    it('ignores zero-latency entries and error-type logs', () => {
      const logs = [
        consume(1, 0), // dropped: latency 0
        errLog(2), // dropped: type 5
        consume(3, 100),
        consume(4, 300),
        consume(5, 200),
      ];
      expect(computeLatencyP50(logs)).toBe(200); // median of [100, 200, 300]
    });

    it('returns mean of two middle values when sample size is even', () => {
      const logs = [100, 200, 300, 400].map((l) => consume(1, l));
      expect(computeLatencyP50(logs)).toBe(250); // (200 + 300) / 2
    });

    it('returns null if no qualifying samples', () => {
      expect(computeLatencyP50([errLog(1), errLog(2)])).toBeNull();
    });

    it('accepts snake_case AND PascalCase keys', () => {
      const mixed = [
        { type: 2, total_latency_ms: 100, created_at: 1 },
        { Type: 2, TotalLatencyMs: 300, CreatedAt: 2 },
        { type: 2, total_latency_ms: 500, created_at: 3 },
      ];
      expect(computeLatencyP50(mixed)).toBe(300);
    });
  });

  describe('computeLatencyPercentile (P95 / P99 / generic)', () => {
    it('returns null on empty input', () => {
      expect(computeLatencyP95([])).toBeNull();
      expect(computeLatencyP99([])).toBeNull();
    });

    it('returns null for out-of-range percentile', () => {
      expect(computeLatencyPercentile([consume(1, 100)], -1)).toBeNull();
      expect(computeLatencyPercentile([consume(1, 100)], 101)).toBeNull();
    });

    it('P95 picks the right tail value (nearest-rank)', () => {
      // 20 samples: 100, 200, ..., 2000. P95 = ceil(0.95*20)-1 = 18, so value at idx 18 = 1900.
      const logs = Array.from({ length: 20 }, (_, i) =>
        consume(i, (i + 1) * 100),
      );
      expect(computeLatencyP95(logs)).toBe(1900);
    });

    it('P99 with small sample falls back to last value', () => {
      const logs = [100, 200, 300, 400, 500].map((l) => consume(1, l));
      // ceil(0.99*5)-1 = 4, value at idx 4 = 500
      expect(computeLatencyP99(logs)).toBe(500);
    });

    it('P95 ignores error logs and zero-latency entries', () => {
      const logs = [
        ...Array.from({ length: 19 }, (_, i) => consume(i, (i + 1) * 10)),
        errLog(20),
        consume(21, 0),
      ];
      // 19 samples sorted: 10..190. P95: ceil(0.95*19)-1 = 18, value = 190.
      expect(computeLatencyP95(logs)).toBe(190);
    });

    it('P99 single-sample case returns that sample', () => {
      expect(computeLatencyP99([consume(1, 250)])).toBe(250);
    });
  });

  describe('computeCostByModel', () => {
    it('returns [] on empty input', () => {
      expect(computeCostByModel([])).toEqual([]);
      expect(computeCostByModel(null)).toEqual([]);
    });

    it('groups by model and sums quota, sorted desc', () => {
      const logs = [
        { type: 2, model_name: 'gpt-4o', quota: 1000 },
        { type: 2, model_name: 'claude-3', quota: 5000 },
        { type: 2, model_name: 'gpt-4o', quota: 2000 },
        { type: 2, model_name: 'gemini-pro', quota: 500 },
      ];
      const result = computeCostByModel(logs);
      expect(result).toEqual([
        { model: 'claude-3', totalQuota: 5000, requestCount: 1 },
        { model: 'gpt-4o', totalQuota: 3000, requestCount: 2 },
        { model: 'gemini-pro', totalQuota: 500, requestCount: 1 },
      ]);
    });

    it('ignores error logs and zero-quota entries', () => {
      const logs = [
        { type: 2, model_name: 'gpt-4o', quota: 1000 },
        { type: 5, model_name: 'gpt-4o', quota: 999 }, // error — ignored
        { type: 2, model_name: 'gpt-4o', quota: 0 }, // zero — ignored
      ];
      expect(computeCostByModel(logs)).toEqual([
        { model: 'gpt-4o', totalQuota: 1000, requestCount: 1 },
      ]);
    });

    it('skips logs with no model_name', () => {
      const logs = [
        { type: 2, model_name: '', quota: 1000 },
        { type: 2, quota: 500 },
        { type: 2, model_name: 'gpt-4o', quota: 100 },
      ];
      expect(computeCostByModel(logs)).toEqual([
        { model: 'gpt-4o', totalQuota: 100, requestCount: 1 },
      ]);
    });

    it('accepts PascalCase fallback', () => {
      const logs = [{ Type: 2, ModelName: 'gpt-4o', Quota: 100 }];
      expect(computeCostByModel(logs)).toEqual([
        { model: 'gpt-4o', totalQuota: 100, requestCount: 1 },
      ]);
    });
  });

  describe('computeErrorRate', () => {
    it('returns 0 on empty input', () => {
      expect(computeErrorRate([])).toBe(0);
    });

    it('returns 0 when only consume traffic, no errors', () => {
      expect(computeErrorRate([consume(1, 100), consume(2, 200)])).toBe(0);
    });

    it('computes errors / (consume + errors)', () => {
      const logs = [
        consume(1, 100),
        consume(2, 100),
        consume(3, 100),
        errLog(4), // 1 error out of 4 total
      ];
      expect(computeErrorRate(logs)).toBe(0.25);
    });

    it('ignores other log types (manage, system, refund, etc.)', () => {
      const logs = [
        consume(1, 100),
        { type: 1, created_at: 2 }, // topup — ignored
        { type: 3, created_at: 3 }, // manage — ignored
        errLog(4),
      ];
      expect(computeErrorRate(logs)).toBe(0.5); // 1 err / (1 consume + 1 err)
    });
  });

  describe('pickRecent', () => {
    it('returns most recent N by created_at desc', () => {
      const logs = [
        consume(100, 1),
        consume(300, 1),
        consume(200, 1),
        consume(400, 1),
      ];
      const recent = pickRecent(logs, 2);
      expect(recent.map((l) => l.created_at)).toEqual([400, 300]);
    });

    it('does not mutate input', () => {
      const logs = [consume(2, 1), consume(1, 1)];
      const before = logs.map((l) => l.created_at);
      pickRecent(logs, 1);
      expect(logs.map((l) => l.created_at)).toEqual(before);
    });
  });

  describe('formatters', () => {
    it('formatLatencyMs', () => {
      expect(formatLatencyMs(null)).toBe('—');
      expect(formatLatencyMs(0)).toBe('0ms');
      expect(formatLatencyMs(250)).toBe('250ms');
      expect(formatLatencyMs(1500)).toBe('1.50s');
    });

    it('formatErrorRate', () => {
      expect(formatErrorRate(0)).toBe('0%');
      expect(formatErrorRate(0.0005)).toBe('<0.1%');
      expect(formatErrorRate(0.123)).toBe('12.3%');
      expect(formatErrorRate(undefined)).toBe('—');
    });

    it('formatQPS', () => {
      expect(formatQPS(0)).toBe('0');
      expect(formatQPS(0.005)).toBe('<0.01');
      expect(formatQPS(0.42)).toBe('0.42');
      expect(formatQPS(2.7)).toBe('2.7');
      expect(formatQPS(42.6)).toBe('43');
      expect(formatQPS(undefined)).toBe('—');
    });
  });
});

// ─── cycle-19: KPI sparkline + 环比 (dailyTotals / percentDelta) ───────────

describe('dailyTotals — per-local-day sums for the KPI sparkline', () => {
  const DAY = 24 * 60 * 60;

  it('returns exactly `days` entries, zero-filled, oldest first', () => {
    // Pin `end` to a fixed local noon so the window boundaries are
    // unambiguous regardless of the hour the suite happens to run at.
    const end = new Date();
    end.setHours(12, 0, 0, 0);
    const endTs = Math.floor(end.getTime() / 1000);
    const rows = [
      { created_at: endTs, quota: 100 }, // today
      { created_at: endTs - 2 * DAY, quota: 40 }, // 2 days ago
    ];
    const out = dailyTotals(rows, { end: endTs, days: 5, field: 'quota' });
    expect(out).toHaveLength(5);
    // oldest .. newest: [4 ago, 3 ago, 2 ago, 1 ago, today]
    expect(out).toEqual([0, 0, 40, 0, 100]);
  });

  it('sums a different field (count) independently of quota', () => {
    const end = new Date();
    end.setHours(12, 0, 0, 0);
    const endTs = Math.floor(end.getTime() / 1000);
    const rows = [
      { created_at: endTs, quota: 500, count: 3 },
      { created_at: endTs, quota: 500, count: 2 },
    ];
    const out = dailyTotals(rows, { end: endTs, days: 1, field: 'count' });
    expect(out).toEqual([5]);
  });

  it('rows outside the window are excluded', () => {
    const end = new Date();
    end.setHours(12, 0, 0, 0);
    const endTs = Math.floor(end.getTime() / 1000);
    const rows = [{ created_at: endTs - 10 * DAY, quota: 999 }];
    const out = dailyTotals(rows, { end: endTs, days: 3, field: 'quota' });
    expect(out).toEqual([0, 0, 0]);
  });

  it('an empty rows array (or missing end/days) returns an empty/zero-filled series without throwing', () => {
    expect(dailyTotals([], { end: 0, days: 3 })).toEqual([]);
    expect(dailyTotals(null, { end: 123, days: 0 })).toEqual([]);
  });

  // Same class of bug as the activity chart's local-day bucketing (see
  // index.test.jsx "buckets two rows straddling local midnight"): two rows
  // exactly one hour apart, straddling local midnight, must land in two
  // distinct day buckets. At TZ=UTC (not this suite's pinned zone) a
  // UTC-day bucket would pass this by accident, so assert the pin first.
  it('buckets two rows straddling local midnight into two distinct days', () => {
    expect(new Date().getTimezoneOffset()).not.toBe(0);

    const beforeMidnight = new Date();
    beforeMidnight.setDate(beforeMidnight.getDate() - 1);
    beforeMidnight.setHours(23, 30, 0, 0);
    const afterMidnight = new Date(beforeMidnight);
    afterMidnight.setHours(24, 30, 0, 0); // rolls to 00:30 local, next day

    const end = new Date(afterMidnight);
    end.setHours(23, 59, 0, 0);
    const endTs = Math.floor(end.getTime() / 1000);

    const rows = [
      { created_at: Math.floor(beforeMidnight.getTime() / 1000), quota: 11 },
      { created_at: Math.floor(afterMidnight.getTime() / 1000), quota: 22 },
    ];
    const out = dailyTotals(rows, { end: endTs, days: 2, field: 'quota' });
    expect(out).toEqual([11, 22]);
  });

  it('sumSeries adds a plain number array, treating non-numbers as 0', () => {
    expect(sumSeries([1, 2, 3])).toBe(6);
    expect(sumSeries([])).toBe(0);
    expect(sumSeries(null)).toBe(0);
  });
});

describe('percentDelta — 环比', () => {
  it('returns null when the previous period is 0 — never a fabricated 0%/∞%', () => {
    expect(percentDelta(30, 0)).toBeNull();
  });

  it('returns null when the previous period is missing/non-finite', () => {
    expect(percentDelta(10, null)).toBeNull();
    expect(percentDelta(10, undefined)).toBeNull();
    expect(percentDelta(10, NaN)).toBeNull();
  });

  it('a doubling from the previous period returns +100', () => {
    expect(percentDelta(20, 10)).toBe(100);
  });

  it('a halving from the previous period returns -50', () => {
    expect(percentDelta(5, 10)).toBe(-50);
  });

  it('composes with sumSeries: current-window total vs prior-window total', () => {
    // "近 7 天对比前 7 天", shrunk to 2-day windows here: current [10, 20]
    // sums to 30, prior [5, 5] sums to 10 — a 200% increase (current is 3x
    // the prior total).
    const current = sumSeries([10, 20]);
    const prior = sumSeries([5, 5]);
    expect(percentDelta(current, prior)).toBe(200);
  });
});
