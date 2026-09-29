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
import { estimateCost, indexCatalogByModel } from './cost';
import { buildCatalog } from '../Models/catalog';

const routable = [
  { id: 'rt-ratio', owned_by: 'custom', supported_endpoint_types: ['openai'] },
  {
    id: 'rt-percall',
    owned_by: 'custom',
    supported_endpoint_types: ['openai'],
  },
  {
    id: 'rt-noprice',
    owned_by: 'custom',
    supported_endpoint_types: ['openai'],
  },
];

// model_ratio 0.5 → inputPerM = 0.5 * USD_PER_M_PER_RATIO(2) * groupRatio.
// completion_ratio 2 → outputPerM = inputPerM * 2.
const pricingRows = [
  {
    model_name: 'rt-ratio',
    quota_type: 0,
    model_ratio: 0.5,
    completion_ratio: 2,
  },
  { model_name: 'rt-percall', quota_type: 1, model_price: 0.02 },
];

const catalogAt = (groupRatio) =>
  indexCatalogByModel(
    buildCatalog({ routable, pricing: pricingRows, groupRatio }),
  );

describe('estimateCost', () => {
  it('ratio billing: cost = prompt*in/1e6 + completion*out/1e6', () => {
    const byModel = catalogAt(1);
    const entry = byModel.get('rt-ratio');
    // inputPerM = 0.5 * 2 * 1 = 1 ; outputPerM = 1 * 2 = 2
    expect(entry.inputPerM).toBe(1);
    expect(entry.outputPerM).toBe(2);

    const cost = estimateCost(entry, 1_000_000, 500_000);
    // 1_000_000/1e6 * 1 + 500_000/1e6 * 2 = 1 + 1 = 2
    expect(cost).toBeCloseTo(2, 10);
  });

  it('per-call billing: cost is the flat perCall price, ignoring token counts', () => {
    const byModel = catalogAt(1);
    const entry = byModel.get('rt-percall');
    expect(entry.perCall).toBe(0.02);
    expect(estimateCost(entry, 999, 999)).toBeCloseTo(0.02, 10);
    // Even with no token counts at all — the price is flat.
    expect(estimateCost(entry, undefined, undefined)).toBeCloseTo(0.02, 10);
  });

  it('returns null, never $0/0, when the entry has no price at all', () => {
    const byModel = catalogAt(1);
    const entry = byModel.get('rt-noprice');
    expect(entry.priced).toBe(false);
    expect(estimateCost(entry, 100, 100)).toBeNull();
  });

  it('returns null when there is no catalog entry for the model', () => {
    expect(estimateCost(undefined, 100, 100)).toBeNull();
  });

  it('returns null for ratio billing when token counts are missing', () => {
    const byModel = catalogAt(1);
    const entry = byModel.get('rt-ratio');
    expect(estimateCost(entry, undefined, 500)).toBeNull();
    expect(estimateCost(entry, 500, undefined)).toBeNull();
  });

  it('doubles when the caller group_ratio doubles', () => {
    const entryAt1 = catalogAt(1).get('rt-ratio');
    const entryAt2 = catalogAt(2).get('rt-ratio');
    const costAt1 = estimateCost(entryAt1, 1_000_000, 1_000_000);
    const costAt2 = estimateCost(entryAt2, 1_000_000, 1_000_000);
    expect(costAt2).toBeCloseTo(costAt1 * 2, 10);
  });
});

describe('indexCatalogByModel', () => {
  it('indexes by model id, skipping entries with no id', () => {
    const idx = indexCatalogByModel([{ id: 'a' }, { id: 'b' }, {}]);
    expect(idx.get('a')).toBeTruthy();
    expect(idx.get('b')).toBeTruthy();
    expect(idx.size).toBe(2);
  });

  it('handles an empty/undefined list', () => {
    expect(indexCatalogByModel(undefined).size).toBe(0);
    expect(indexCatalogByModel([]).size).toBe(0);
  });
});
