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

// window.test.js — the Log page's query time bound, tested directly now that
// it lives in a module of its own (cycle-13 W extraction).
//
// The bound is the reason the page does not ask the database to scan every
// row a tenant ever wrote: with no start date, every logs / logs-stat query
// carries `start_time = now - DEFAULT_LOOKBACK_SEC`, and an id search carries
// its own lookback because request_id filtering goes through an unindexed
// JSON extract. Removing either fallback is a one-line edit that the page's
// own render tests would not notice — they assert on what is drawn, not on
// the query string.

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  DEFAULT_LOOKBACK_SEC,
  ID_FILTER_LOOKBACK_SEC,
  computeStartTimeSec,
} from './window';

const NOW_MS = Date.UTC(2026, 8, 20, 12, 0, 0);
const NOW_SEC = Math.floor(NOW_MS / 1000);

afterEach(() => {
  vi.useRealTimers();
});

describe('computeStartTimeSec', () => {
  it('uses an explicit start date verbatim, whatever else is set', () => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW_MS);
    const start = '2026-01-02T03:04';
    const expected = Math.floor(new Date(start).getTime() / 1000);

    expect(computeStartTimeSec(start, '', false)).toBe(expected);
    expect(computeStartTimeSec(start, '2026-02-01T00:00', true)).toBe(expected);
  });

  it('falls back to the default lookback anchored on now when nothing is set', () => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW_MS);

    expect(computeStartTimeSec('', '', false)).toBe(
      NOW_SEC - DEFAULT_LOOKBACK_SEC,
    );
  });

  it('anchors the lookback on an explicit end so the start can never follow it', () => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW_MS);
    const end = '2025-06-01T00:00';
    const endSec = Math.floor(new Date(end).getTime() / 1000);

    const startSec = computeStartTimeSec('', end, false);
    expect(startSec).toBe(endSec - DEFAULT_LOOKBACK_SEC);
    expect(startSec).toBeLessThan(endSec);
  });

  it('uses the id-filter lookback when an id filter is active', () => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW_MS);

    expect(computeStartTimeSec('', '', true)).toBe(
      NOW_SEC - ID_FILTER_LOOKBACK_SEC,
    );
  });

  it('never returns an unbounded window', () => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW_MS);

    // 0 (or anything <= 0) would be "since the epoch" — exactly the
    // full-table scan the bound exists to prevent.
    for (const hasIdFilter of [false, true]) {
      expect(computeStartTimeSec('', '', hasIdFilter)).toBeGreaterThan(0);
      expect(computeStartTimeSec('', '', hasIdFilter)).toBeLessThan(NOW_SEC);
    }
  });
});

describe('the lookback constants', () => {
  it('are positive whole-day windows', () => {
    for (const secs of [DEFAULT_LOOKBACK_SEC, ID_FILTER_LOOKBACK_SEC]) {
      expect(secs).toBeGreaterThan(0);
      expect(secs % 86400).toBe(0);
    }
  });
});
