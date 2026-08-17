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
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { renderHook } from '@testing-library/react';

import { useUsageGauge } from './useUsageGauge';

const userWith = (quota, used_quota) => ({ user: { quota, used_quota } });

describe('useUsageGauge — quota arithmetic', () => {
  it('splits quota/used into a percentage of the lifetime total', () => {
    const { result } = renderHook(() =>
      useUsageGauge(userWith(750, 250), null),
    );

    expect(result.current.quota).toBe(750);
    expect(result.current.usedQuota).toBe(250);
    // totalQuota is granted+consumed, NOT the remaining balance.
    expect(result.current.totalQuota).toBe(1000);
    expect(result.current.usagePercent).toBe(25);
    expect(result.current.level).toBe('green');
  });

  it('returns 0% instead of NaN when the account has no quota at all', () => {
    const { result } = renderHook(() => useUsageGauge(userWith(0, 0), null));

    expect(result.current.totalQuota).toBe(0);
    expect(result.current.usagePercent).toBe(0);
    expect(Number.isNaN(result.current.usagePercent)).toBe(false);
    expect(result.current.level).toBe('green');
  });

  it('defaults quota/used to 0 when the user object is absent', () => {
    const { result } = renderHook(() => useUsageGauge(undefined, undefined));

    expect(result.current.quota).toBe(0);
    expect(result.current.usedQuota).toBe(0);
    expect(result.current.usagePercent).toBe(0);
    expect(result.current.daysRemaining).toBeNull();
    expect(result.current.exhaustionDate).toBeNull();
  });

  it('rounds the percentage to one decimal place', () => {
    // 1/3 -> 33.333...% -> 33.3
    const { result } = renderHook(() => useUsageGauge(userWith(2, 1), null));
    expect(result.current.usagePercent).toBe(33.3);
  });

  it('clamps the percentage at 100 when used exceeds the grant', () => {
    // Overdraft: quota went negative, used dwarfs what is left.
    const { result } = renderHook(() =>
      useUsageGauge(userWith(-100, 500), null),
    );
    expect(result.current.usagePercent).toBe(100);
    expect(result.current.level).toBe('critical');
  });
});

describe('useUsageGauge — alert levels', () => {
  // Boundaries are inclusive-lower: >=50 yellow, >=80 red, >=95 critical.
  const cases = [
    [1000, 0, 'green'],
    [501, 499, 'green'], // 49.9%
    [500, 500, 'yellow'], // exactly 50%
    [201, 799, 'yellow'], // 79.9%
    [200, 800, 'red'], // exactly 80%
    [51, 949, 'red'], // 94.9%
    [50, 950, 'critical'], // exactly 95%
  ];

  for (const [quota, used, expected] of cases) {
    it(`quota=${quota} used=${used} -> ${expected}`, () => {
      const { result } = renderHook(() =>
        useUsageGauge(userWith(quota, used), null),
      );
      expect(result.current.level).toBe(expected);
    });
  }
});

describe('useUsageGauge — burn rate and exhaustion projection', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-03-01T00:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('averages the drop across the trend gaps, not the points', () => {
    // 4 points => 3 gaps; total drop 300 => 100/day.
    const trend = { balance: [1000, 900, 800, 700] };
    const { result } = renderHook(() =>
      useUsageGauge(userWith(500, 500), trend),
    );

    expect(result.current.dailyRate).toBe(100);
    // 500 remaining / 100 per day = 5 days.
    expect(result.current.daysRemaining).toBe(5);
    expect(result.current.exhaustionDate).toBe('2026-03-06');
  });

  it('only considers the most recent 7 trend points', () => {
    // 9 points; the leading 1e6 spike must be ignored by the slice(-7).
    const trend = {
      balance: [1000000, 999999, 700, 600, 500, 400, 300, 200, 100],
    };
    const { result } = renderHook(() => useUsageGauge(userWith(600, 0), trend));

    // slice(-7) => [700..100]: drop 600 over 6 gaps = 100/day.
    expect(result.current.dailyRate).toBe(100);
    expect(result.current.daysRemaining).toBe(6);
  });

  it('floors a rising balance at rate 0 and reports no exhaustion date', () => {
    // Balance grew (a top-up landed) — a negative burn rate is meaningless.
    const trend = { balance: [100, 500, 900] };
    const { result } = renderHook(() => useUsageGauge(userWith(900, 0), trend));

    expect(result.current.dailyRate).toBe(0);
    expect(result.current.daysRemaining).toBeNull();
    expect(result.current.exhaustionDate).toBeNull();
  });

  it('needs at least two trend points before projecting', () => {
    const { result } = renderHook(() =>
      useUsageGauge(userWith(900, 100), { balance: [900] }),
    );

    expect(result.current.dailyRate).toBe(0);
    expect(result.current.daysRemaining).toBeNull();
  });

  it('reports no exhaustion date once the quota is already drained', () => {
    const trend = { balance: [500, 400, 300] };
    const { result } = renderHook(() =>
      useUsageGauge(userWith(0, 1000), trend),
    );

    expect(result.current.dailyRate).toBe(100);
    // quota === 0 -> no projection, even though burn rate is positive.
    expect(result.current.daysRemaining).toBeNull();
    expect(result.current.exhaustionDate).toBeNull();
  });

  it('rounds the daily rate to two decimals', () => {
    // drop 1 over 3 gaps = 0.3333... -> 0.33
    const trend = { balance: [10, 9.7, 9.4, 9] };
    const { result } = renderHook(() => useUsageGauge(userWith(10, 0), trend));
    expect(result.current.dailyRate).toBe(0.33);
  });

  it('rounds days remaining UP so the date is never optimistic', () => {
    // 250 / 100 = 2.5 days -> must report 3, not 2.
    const trend = { balance: [400, 300, 200] };
    const { result } = renderHook(() => useUsageGauge(userWith(250, 0), trend));

    expect(result.current.daysRemaining).toBe(3);
    expect(result.current.exhaustionDate).toBe('2026-03-04');
  });
});
