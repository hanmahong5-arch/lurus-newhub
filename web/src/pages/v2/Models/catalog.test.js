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
  buildCatalog,
  filterCatalog,
  sortCatalog,
  vendorFacets,
  capabilityFacets,
  fmtUsd,
  fmtCompact,
  fmtMs,
  fmtPct,
} from './catalog';

// The UAT state that made the old page say "0 models": two models routable
// and priced, nothing in the catalogue table.
const uat = {
  routable: [
    {
      id: 'deepseek-chat',
      owned_by: 'deepseek',
      supported_endpoint_types: ['openai'],
    },
    { id: 'faultsim-music', owned_by: 'custom', supported_endpoint_types: [] },
  ],
  pricing: [
    {
      model_name: 'deepseek-chat',
      vendor: 'DeepSeek',
      quota_type: 0,
      model_ratio: 0.135,
      completion_ratio: 4,
      cache_ratio: 0.25,
      description: 'DeepSeek V3 chat',
      supported_endpoint_types: ['openai', 'anthropic'],
    },
    { model_name: 'faultsim-music', quota_type: 1, model_price: 0.1 },
  ],
  catalogue: [],
};

describe('buildCatalog', () => {
  it('lists routable models even when the catalogue table is empty', () => {
    const ids = buildCatalog(uat).map((e) => e.id);
    expect(ids).toEqual(
      expect.arrayContaining(['deepseek-chat', 'faultsim-music']),
    );
    expect(ids).toHaveLength(2);
  });

  it('turns ratios into $/1M: ratio × 2, output × completion, cache × cache', () => {
    const e = buildCatalog(uat).find((x) => x.id === 'deepseek-chat');
    expect(e.inputPerM).toBeCloseTo(0.27);
    expect(e.outputPerM).toBeCloseTo(1.08);
    expect(e.cacheReadPerM).toBeCloseTo(0.0675);
    expect(e.vendor).toBe('DeepSeek');
    expect(e.description).toBe('DeepSeek V3 chat');
    expect(e.capabilities).toEqual(['openai', 'anthropic']);
  });

  it('applies the caller group ratio to every price', () => {
    const e = buildCatalog({ ...uat, groupRatio: 2 }).find(
      (x) => x.id === 'deepseek-chat',
    );
    expect(e.inputPerM).toBeCloseTo(0.54);
    const call = buildCatalog({ ...uat, groupRatio: 2 }).find(
      (x) => x.id === 'faultsim-music',
    );
    expect(call.perCall).toBeCloseTo(0.2);
  });

  it('reads an unset completion ratio as 1, not as a free output', () => {
    const e = buildCatalog({
      pricing: [{ model_name: 'm', quota_type: 0, model_ratio: 1 }],
    })[0];
    expect(e.outputPerM).toBe(2);
  });

  it('marks priced-but-not-routable models as such', () => {
    const e = buildCatalog({
      pricing: [{ model_name: 'gpt-x', quota_type: 0, model_ratio: 1 }],
    })[0];
    expect(e.routable).toBe(false);
    expect(e.priced).toBe(true);
  });

  it('attaches usage by name and ignores usage rows for unknown models', () => {
    const entries = buildCatalog({
      ...uat,
      usage: [
        { name: 'deepseek-chat', total_tokens: 1500, requests: 3 },
        { name: '', total_tokens: 9, requests: 9 },
      ],
    });
    expect(entries.find((e) => e.id === 'deepseek-chat').tokens).toBe(1500);
    expect(entries).toHaveLength(2);
  });
});

describe('filter / sort / facets', () => {
  const entries = buildCatalog({
    ...uat,
    usage: [{ name: 'faultsim-music', total_tokens: 10, requests: 1 }],
  });

  it('filters by query, vendor, capability and routability', () => {
    expect(filterCatalog(entries, { q: 'v3' }).map((e) => e.id)).toEqual([
      'deepseek-chat',
    ]);
    expect(
      filterCatalog(entries, { vendors: ['DeepSeek'] }).map((e) => e.id),
    ).toEqual(['deepseek-chat']);
    expect(
      filterCatalog(entries, { capabilities: ['anthropic'] }).map((e) => e.id),
    ).toEqual(['deepseek-chat']);
    expect(filterCatalog(entries, { routableOnly: true })).toHaveLength(2);
  });

  it('sorts popular by tokens, and prices unpriced entries last', () => {
    expect(sortCatalog(entries, 'popular')[0].id).toBe('faultsim-music');
    const withUnpriced = [
      ...entries,
      ...buildCatalog({ routable: [{ id: 'aaa-unpriced' }] }),
    ];
    const byInput = sortCatalog(withUnpriced, 'input_asc').map((e) => e.id);
    expect(byInput[byInput.length - 1]).toBe('aaa-unpriced');
  });

  it('counts vendors and capabilities', () => {
    // Equal counts fall back to a case-insensitive name order.
    expect(vendorFacets(entries)).toEqual([
      { vendor: 'custom', count: 1 },
      { vendor: 'DeepSeek', count: 1 },
    ]);
    expect(capabilityFacets(entries).map((c) => c.capability)).toEqual([
      'openai',
      'anthropic',
    ]);
  });
});

describe('performance', () => {
  const perf = [
    {
      model_name: 'deepseek-chat',
      p50_latency_ms: 850,
      p95_latency_ms: 2100,
      error_rate: 0.02,
      enough_samples: true,
    },
    {
      model_name: 'faultsim-music',
      p50_latency_ms: 90,
      p95_latency_ms: 90,
      error_rate: 0,
      enough_samples: false,
    },
    { model_name: 'not-listed', p50_latency_ms: 1, enough_samples: true },
  ];

  it('attaches tenant performance by name and ignores unknown models', () => {
    const entries = buildCatalog({ ...uat, performance: perf });
    const e = entries.find((x) => x.id === 'deepseek-chat');
    expect([e.p50Ms, e.p95Ms, e.errorRate, e.enoughSamples]).toEqual([
      850,
      2100,
      0.02,
      true,
    ]);
    expect(entries.map((x) => x.id)).not.toContain('not-listed');
  });

  it('ranks "fastest" by p50 among models with enough samples only', () => {
    // faultsim-music is faster on paper (90 ms) but from too few samples.
    const entries = buildCatalog({ ...uat, performance: perf });
    expect(sortCatalog(entries, 'fastest')[0].id).toBe('deepseek-chat');
  });

  it('formats latency and rates', () => {
    expect(fmtMs(850)).toBe('850ms');
    expect(fmtMs(1234)).toBe('1.2s');
    expect(fmtMs(null)).toBe('—');
    expect(fmtPct(0.0213)).toBe('2.1%');
  });
});

describe('formatting', () => {
  it('never renders a real price as $0', () => {
    expect(fmtUsd(0.0000675)).toBe('$0.0001');
    expect(fmtUsd(0.27)).toBe('$0.27');
    expect(fmtUsd(1.08)).toBe('$1.08');
    expect(fmtUsd(null)).toBe('—');
  });

  it('compacts counts', () => {
    expect(fmtCompact(1234)).toBe('1.2K');
    expect(fmtCompact(3_400_000)).toBe('3.4M');
    expect(fmtCompact(0)).toBe('0');
  });
});
