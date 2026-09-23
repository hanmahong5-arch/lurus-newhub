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
  buildActivity,
  localDayKey,
  localHourKey,
  niceCeil,
  OTHER,
} from './activitySeries';

// Noon local time N days before a fixed local "today".
const today = new Date(2026, 8, 22, 12, 0, 0);
const at = (daysAgo, hour = 12) => {
  const d = new Date(today);
  d.setDate(d.getDate() - daysAgo);
  d.setHours(hour, 0, 0, 0);
  return Math.floor(d.getTime() / 1000);
};
const end = at(0, 23);

const row = (daysAgo, model, quota, tokens = 0, count = 1) => ({
  created_at: at(daysAgo),
  model_name: model,
  quota,
  token_used: tokens,
  count,
});

describe('buildActivity', () => {
  it('zero-fills every day in the window, oldest first', () => {
    const { days } = buildActivity([row(1, 'a', 5)], { end, days: 7 });
    expect(days).toHaveLength(7);
    expect(days[6].day).toBe(localDayKey(end));
    expect(days.map((d) => d.total)).toEqual([0, 0, 0, 0, 0, 5, 0]);
  });

  it('stacks per model per day and ranks series by total', () => {
    const r = buildActivity(
      [row(0, 'a', 1), row(0, 'b', 3), row(1, 'a', 4), row(1, 'b', 1)],
      { end, days: 2 },
    );
    expect(r.series.map((s) => s.model)).toEqual(['a', 'b']);
    expect(r.days[1].parts).toEqual({ a: 1, b: 3 });
    expect(r.total).toBe(9);
  });

  it('folds models past topN into one "other" series', () => {
    const rows = ['m1', 'm2', 'm3', 'm4'].map((m, i) => row(0, m, 10 - i));
    const r = buildActivity(rows, { end, days: 1, topN: 2 });
    expect(r.series.map((s) => s.model)).toEqual(['m1', 'm2', OTHER]);
    expect(r.days[0].parts[OTHER]).toBe(8 + 7);
  });

  it('switches the measured field with the metric', () => {
    const rows = [row(0, 'a', 100, 5000, 3)];
    expect(buildActivity(rows, { end, days: 1, metric: 'tokens' }).total).toBe(
      5000,
    );
    expect(
      buildActivity(rows, { end, days: 1, metric: 'requests' }).total,
    ).toBe(3);
  });

  it('drops rows outside the window', () => {
    const r = buildActivity([row(10, 'old', 9), row(0, 'new', 1)], {
      end,
      days: 7,
    });
    expect(r.series.map((s) => s.model)).toEqual(['new']);
  });
});

describe('buildActivity — hourly', () => {
  // Rows are hourly, so the 24h view is the same rows bucketed by hour.
  const hourRow = (hoursAgo, model, quota) => {
    const d = new Date(today);
    d.setHours(12 - hoursAgo, 15, 0, 0);
    return {
      created_at: Math.floor(d.getTime() / 1000),
      model_name: model,
      quota,
    };
  };
  const noon = at(0, 12);

  it('makes one zero-filled bucket per local hour, ending with the current one', () => {
    const r = buildActivity([hourRow(0, 'a', 2), hourRow(3, 'a', 5)], {
      end: noon,
      hours: 24,
    });
    expect(r.unit).toBe('hour');
    expect(r.days).toHaveLength(24);
    expect(r.days[23].day).toBe(localHourKey(noon));
    expect(r.days[23].total).toBe(2);
    expect(r.days[20].total).toBe(5);
    expect(r.days.filter((d) => d.total > 0)).toHaveLength(2);
  });

  it('drops rows older than the hour window', () => {
    const r = buildActivity([hourRow(30, 'old', 9), hourRow(1, 'new', 1)], {
      end: noon,
      hours: 24,
    });
    expect(r.series.map((s) => s.model)).toEqual(['new']);
  });
});

describe('niceCeil', () => {
  it.each([
    [0.8, 1],
    [3, 5],
    [180, 200],
    [2200, 2500],
    [0, 0],
  ])('%s → %s', (v, want) => {
    expect(niceCeil(v)).toBe(want);
  });
});
