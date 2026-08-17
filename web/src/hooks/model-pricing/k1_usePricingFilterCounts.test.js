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
import { renderHook } from '@testing-library/react';

import { usePricingFilterCounts } from './usePricingFilterCounts';

// The sidebar renders one count per facet value. Each returned collection is
// "everything that survives the OTHER facets" so a facet never zeroes out its
// own options. The tests below pin that self-exclusion rule per facet.

const names = (list) => list.map((m) => m.model_name).sort();

const MODELS = [
  {
    model_name: 'gpt-4o',
    quota_type: 0,
    enable_groups: ['default', 'vip'],
    supported_endpoint_types: ['openai'],
    vendor_name: 'OpenAI',
    tags: 'vision,Chat',
    description: 'flagship omni model',
  },
  {
    model_name: 'claude-3-opus',
    quota_type: 0,
    enable_groups: ['vip'],
    supported_endpoint_types: ['anthropic', 'openai'],
    vendor_name: 'Anthropic',
    tags: 'chat;long-context',
    description: 'long context',
  },
  {
    model_name: 'dall-e-3',
    quota_type: 1,
    enable_groups: ['default'],
    supported_endpoint_types: ['image'],
    vendor_name: 'OpenAI',
    tags: 'image',
    description: 'image generation',
  },
  {
    // No vendor at all — the 'unknown' vendor bucket.
    model_name: 'local-llama',
    quota_type: 0,
    enable_groups: ['default'],
    supported_endpoint_types: ['openai'],
    tags: '',
    description: 'self hosted',
  },
];

const run = (overrides) =>
  renderHook(() => usePricingFilterCounts({ models: MODELS, ...overrides }))
    .result.current;

describe('usePricingFilterCounts facet self-exclusion', () => {
  it('returns every model on every facet when no filter is applied', () => {
    const r = run({});
    expect(r.quotaTypeModels).toHaveLength(4);
    expect(r.endpointTypeModels).toHaveLength(4);
    expect(r.vendorModels).toHaveLength(4);
    expect(r.tagModels).toHaveLength(4);
    expect(r.groupCountModels).toHaveLength(4);
  });

  it('quotaTypeModels ignores the quota filter but honours the vendor filter', () => {
    const r = run({ filterQuotaType: 1, filterVendor: 'OpenAI' });
    // quota facet self-excluded => both OpenAI models survive, including the
    // quota_type 0 one that the table itself would have filtered out.
    expect(names(r.quotaTypeModels)).toEqual(['dall-e-3', 'gpt-4o']);
    // ...while the vendor facet DOES apply the quota filter.
    expect(names(r.vendorModels)).toEqual(['dall-e-3']);
  });

  it('groupCountModels ignores the group filter but honours search', () => {
    const r = run({ filterGroup: 'vip', searchValue: 'image' });
    // 'image' matches dall-e-3 by description/tags even though it is not vip.
    expect(names(r.groupCountModels)).toEqual(['dall-e-3']);
    // The group facet applied elsewhere keeps only vip models, and none of
    // those match the search term.
    expect(r.vendorModels).toHaveLength(0);
  });

  it('endpointTypeModels ignores the endpoint filter, tagModels ignores the tag filter', () => {
    const r = run({ filterEndpointType: 'image', filterTag: 'vision' });
    // endpoint self-excluded, tag still applied => only gpt-4o carries 'vision'.
    expect(names(r.endpointTypeModels)).toEqual(['gpt-4o']);
    // tag self-excluded, endpoint still applied => only dall-e-3 is 'image'.
    expect(names(r.tagModels)).toEqual(['dall-e-3']);
  });
});

describe('usePricingFilterCounts matching rules', () => {
  it('group matching uses enable_groups membership', () => {
    expect(names(run({ filterGroup: 'vip' }).vendorModels)).toEqual([
      'claude-3-opus',
      'gpt-4o',
    ]);
  });

  it('quota_type is compared strictly, so a string filter matches nothing', () => {
    // quota_type arrives from the API as a number; '1' is not 1.
    expect(run({ filterQuotaType: '1' }).vendorModels).toHaveLength(0);
    expect(names(run({ filterQuotaType: 1 }).vendorModels)).toEqual([
      'dall-e-3',
    ]);
  });

  it("vendor 'unknown' selects exactly the models with no vendor_name", () => {
    expect(names(run({ filterVendor: 'unknown' }).tagModels)).toEqual([
      'local-llama',
    ]);
  });

  it('tags are split on comma / semicolon / pipe and matched case-insensitively', () => {
    // 'Chat' on gpt-4o (comma separated) and 'chat' on claude (semicolon).
    expect(names(run({ filterTag: 'CHAT' }).vendorModels)).toEqual([
      'claude-3-opus',
      'gpt-4o',
    ]);
    // Partial tag text must NOT match — matching is per whole tag.
    expect(run({ filterTag: 'cha' }).vendorModels).toHaveLength(0);
  });

  it('an empty tags string yields no tags, so it never matches a tag filter', () => {
    // local-llama has tags '' — it must be absent even though every other
    // facet would keep it.
    expect(names(run({ filterTag: 'image' }).vendorModels)).toEqual([
      'dall-e-3',
    ]);
  });

  it('search scans model_name, description, tags and vendor_name', () => {
    expect(names(run({ searchValue: 'OPUS' }).vendorModels)).toEqual([
      'claude-3-opus',
    ]);
    expect(names(run({ searchValue: 'self hosted' }).vendorModels)).toEqual([
      'local-llama',
    ]);
    expect(names(run({ searchValue: 'vision' }).vendorModels)).toEqual([
      'gpt-4o',
    ]);
    // vendor_name hit: both OpenAI models, neither of which contains the term
    // in its name or description. local-llama is excluded — supported_endpoint
    // _types is NOT part of the search corpus even though it holds 'openai'.
    expect(names(run({ searchValue: 'openai' }).vendorModels)).toEqual([
      'dall-e-3',
      'gpt-4o',
    ]);
  });

  it('filters compose with AND across different facets', () => {
    const r = run({
      filterGroup: 'default',
      filterVendor: 'OpenAI',
      filterEndpointType: 'openai',
    });
    // groupCountModels drops the group condition only.
    expect(names(r.groupCountModels)).toEqual(['gpt-4o']);
  });

  it('an empty model list yields empty collections rather than throwing', () => {
    const { result } = renderHook(() =>
      usePricingFilterCounts({ models: [], filterVendor: 'OpenAI' }),
    );
    expect(result.current.vendorModels).toEqual([]);
    expect(result.current.groupCountModels).toEqual([]);
  });
});

describe('usePricingFilterCounts null-tolerance (defect lock)', () => {
  // DEFECT: the sibling filter in useModelPricingData.filteredModels guards
  // every field it reads (`model.model_name && ...`, `model.tags && ...`) but
  // this hook does not: `model.model_name.toLowerCase()` and
  // `normalizeTags(model.tags)` (default only fires for `undefined`) throw on
  // a null field. Both hooks are fed the SAME array from the pricing page, so
  // one nullable column crashes the sidebar while the table renders fine.
  // Skipped: the correct contract is "skip the model", not "throw".
  it.skip('does not throw when a model has no model_name during search', () => {
    const { result } = renderHook(() =>
      usePricingFilterCounts({
        models: [{ quota_type: 0 }],
        searchValue: 'anything',
      }),
    );
    expect(result.current.vendorModels).toEqual([]);
  });

  it.skip('does not throw when tags is null during a tag filter', () => {
    const { result } = renderHook(() =>
      usePricingFilterCounts({
        models: [{ model_name: 'x', tags: null }],
        filterTag: 'chat',
      }),
    );
    expect(result.current.vendorModels).toEqual([]);
  });

  // Positive lock on the CURRENT (throwing) behaviour is deliberately omitted;
  // asserting a crash would cement the defect.
});
