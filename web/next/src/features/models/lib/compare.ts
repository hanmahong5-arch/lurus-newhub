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

import { hasPrice, type ModelEntry } from './catalog';

/** Most models the comparison holds at once. */
export const MAX_COMPARE = 4;

/**
 * Indexes holding the lowest of the given prices. Only positive, finite
 * values take part (a missing or zero price is "not configured", never the
 * cheapest). A mark needs a contest: with fewer than two priced values, or
 * with every priced value equal, nothing is marked.
 */
export function cheapestIndexes(
  values: ReadonlyArray<number | null | undefined>,
): Set<number> {
  const priced: Array<[number, number]> = [];
  values.forEach((v, i) => {
    if (typeof v === 'number' && Number.isFinite(v) && v > 0) {
      priced.push([i, v]);
    }
  });
  if (priced.length < 2) return new Set();
  const min = Math.min(...priced.map(([, v]) => v));
  const max = Math.max(...priced.map(([, v]) => v));
  if (min === max) return new Set();
  return new Set(priced.filter(([, v]) => v === min).map(([i]) => i));
}

/** Per-token input price that can be ranked; per-call and search-unit models are billed differently. */
export const rankableInput = (e: ModelEntry): number | null =>
  e.usageUnit === 'token' && e.quotaType !== 1 && hasPrice(e)
    ? e.inputPerM
    : null;

/** Per-token output price that can be ranked; input-only models bill none. */
export const rankableOutput = (e: ModelEntry): number | null =>
  rankableInput(e) !== null && !e.inputOnly ? e.outputPerM : null;

/** Toggle an id in the selection; a full selection ignores new ids. */
export function toggleCompareId(ids: string[], id: string): string[] {
  if (ids.includes(id)) return ids.filter((x) => x !== id);
  return ids.length >= MAX_COMPARE ? ids : [...ids, id];
}
