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

import { buildCatalog, type ModelEntry } from './catalog';
import {
  cheapestIndexes,
  MAX_COMPARE,
  rankableInput,
  rankableOutput,
  toggleCompareId,
} from './compare';

const entry = (over: Record<string, unknown>): ModelEntry =>
  buildCatalog({
    pricing: [{ model_name: 'm', model_ratio: 1, ...over }],
    usdPerRatio: 2,
  })[0];

describe('cheapestIndexes', () => {
  it('marks the lowest price among at least two', () => {
    expect([...cheapestIndexes([4, 1, 2])]).toEqual([1]);
  });

  it('marks every model that shares the lowest price', () => {
    expect([...cheapestIndexes([1, 3, 1])]).toEqual([0, 2]);
  });

  it('marks nothing when there is no contest', () => {
    expect(cheapestIndexes([1]).size).toBe(0);
    expect(cheapestIndexes([null, 2, undefined]).size).toBe(0);
    expect(cheapestIndexes([]).size).toBe(0);
    // Everyone costs the same: "cheapest" would say nothing.
    expect(cheapestIndexes([2, 2]).size).toBe(0);
  });

  it('never lets a zero, negative or non-finite price win', () => {
    // 0 means "not configured" here, not "free": it cannot be the cheapest.
    expect([...cheapestIndexes([0, 5, 3])]).toEqual([2]);
    expect(cheapestIndexes([0, 5]).size).toBe(0);
    expect([...cheapestIndexes([-1, 5, 3])]).toEqual([2]);
    expect([...cheapestIndexes([Number.NaN, 5, 3])]).toEqual([2]);
    expect([...cheapestIndexes([Number.POSITIVE_INFINITY, 5, 3])]).toEqual([2]);
  });
});

describe('rankable prices', () => {
  it('ranks per-token input and output', () => {
    const e = entry({ completion_ratio: 3 });
    expect(rankableInput(e)).toBe(2);
    expect(rankableOutput(e)).toBe(6);
  });

  it('does not rank per-call, search-unit or unpriced models', () => {
    const perCall = entry({ quota_type: 1, model_price: 0.05 });
    expect(rankableInput(perCall)).toBeNull();
    expect(rankableOutput(perCall)).toBeNull();

    const search = entry({
      usage_unit: 'search_unit',
      search_unit_price: 0.01,
    });
    expect(rankableInput(search)).toBeNull();

    const unpriced = entry({ price_configured: false });
    expect(rankableInput(unpriced)).toBeNull();
    expect(rankableOutput(unpriced)).toBeNull();

    const zero = entry({ model_ratio: 0 });
    expect(rankableInput(zero)).toBeNull();
  });

  it('ranks input but not output for an input-only model', () => {
    const e = entry({ supported_endpoint_types: ['systemone'] });
    expect(rankableInput(e)).toBe(2);
    expect(rankableOutput(e)).toBeNull();
  });
});

describe('toggleCompareId', () => {
  it('adds, removes and keeps pick order', () => {
    let ids: string[] = [];
    ids = toggleCompareId(ids, 'a');
    ids = toggleCompareId(ids, 'b');
    expect(ids).toEqual(['a', 'b']);
    expect(toggleCompareId(ids, 'a')).toEqual(['b']);
  });

  it('refuses a fifth model but still lets any pick be removed', () => {
    const full = Array.from({ length: MAX_COMPARE }, (_, i) => `m${i}`);
    expect(MAX_COMPARE).toBe(4);
    expect(toggleCompareId(full, 'extra')).toBe(full);
    expect(toggleCompareId(full, 'm0')).toHaveLength(MAX_COMPARE - 1);
  });
});
