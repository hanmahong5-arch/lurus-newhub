import { describe, expect, it } from 'vitest';

import {
  buildCatalog,
  filterCatalog,
  formatPrice,
  hasPrice,
  modalityFacets,
  perUnit,
  priceTypeCounts,
  priceTypeOf,
  sortCatalog,
  tagFacets,
  usdPerRatio,
  vendorFacets,
} from './catalog';
import { parseMoneyConfig } from '@/lib/money';

import { curlSnippet } from './snippets';
import { WIRE_CHAT } from './wire';

function mustMoney(raw: Record<string, unknown>) {
  const cfg = parseMoneyConfig(raw);
  if (!cfg) throw new Error('money config did not parse');
  return cfg;
}

const USD = mustMoney({ quota_per_unit: 500000 });
const CNY = mustMoney({
  quota_per_unit: 500000,
  quota_display_type: 'CNY',
  usd_exchange_rate: 7,
});

describe('buildCatalog price conversion', () => {
  it('converts ratio to $/M with 2 USD per ratio unit and group ratio', () => {
    const [e] = buildCatalog({
      usdPerRatio: usdPerRatio(USD),
      pricing: [
        {
          model_name: 'm1',
          model_ratio: 0.5,
          completion_ratio: 4,
          cache_ratio: 0.1,
          quota_type: 0,
        },
      ],
      groupRatio: 1.5,
    });
    expect(e.inputPerM).toBeCloseTo(1.5);
    expect(e.outputPerM).toBeCloseTo(6);
    expect(e.cacheReadPerM).toBeCloseTo(0.15);
    expect(hasPrice(e)).toBe(true);
  });

  it('treats completion_ratio 0 as 1 (output falls back to input price)', () => {
    const [e] = buildCatalog({
      usdPerRatio: usdPerRatio(USD),
      pricing: [{ model_name: 'm', model_ratio: 1, completion_ratio: 0 }],
    });
    expect(e.outputPerM).toBe(e.inputPerM);
  });

  it('applies the group ratio to per-call prices', () => {
    const [e] = buildCatalog({
      usdPerRatio: usdPerRatio(USD),
      pricing: [{ model_name: 'm', quota_type: 1, model_price: 0.04 }],
      groupRatio: 2,
    });
    expect(e.perCall).toBeCloseTo(0.08);
    expect(hasPrice(e)).toBe(true);
  });

  it('marks zero and missing prices as unpriced', () => {
    const entries = buildCatalog({
      usdPerRatio: usdPerRatio(USD),
      pricing: [
        { model_name: 'zero', model_ratio: 0 },
        { model_name: 'zero-call', quota_type: 1, model_price: 0 },
        { model_name: 'missing-call', quota_type: 1 },
      ],
      routable: [{ id: 'only-routable' }],
    });
    expect(entries).toHaveLength(4);
    for (const e of entries) expect(hasPrice(e)).toBe(false);
  });

  it('treats price_configured=false as unpriced whatever ratio it carries', () => {
    const [off, on, legacy] = buildCatalog({
      usdPerRatio: usdPerRatio(USD),
      pricing: [
        { model_name: 'off', model_ratio: 37.5, price_configured: false },
        { model_name: 'on', model_ratio: 1, price_configured: true },
        // Older server: no flag at all counts as configured.
        { model_name: 'legacy', model_ratio: 1 },
      ],
    });
    expect(off.priced).toBe(false);
    expect(off.inputPerM).toBeNull();
    expect(hasPrice(off)).toBe(false);
    expect(on.priced).toBe(true);
    expect(hasPrice(on)).toBe(true);
    expect(legacy.priced).toBe(true);
    expect(hasPrice(legacy)).toBe(true);
  });

  it('keeps system one input-only instead of inventing an output price', () => {
    const [e] = buildCatalog({
      usdPerRatio: usdPerRatio(USD),
      pricing: [
        {
          model_name: 's1',
          model_ratio: 1,
          supported_endpoint_types: ['systemone'],
        },
      ],
    });
    expect(e.inputOnly).toBe(true);
    expect(e.outputPerM).toBeNull();
  });

  it('unions routable, pricing and catalogue by model name', () => {
    const entries = buildCatalog({
      usdPerRatio: usdPerRatio(USD),
      routable: [
        { id: 'a', owned_by: 'VendorA', supported_endpoint_types: [WIRE_CHAT] },
      ],
      pricing: [{ model_name: 'a', model_ratio: 1, vendor: 'VendorA' }],
      catalogue: [{ id: 7, model_name: 'b', vendor: 'VendorB', status: 1 }],
      usage: [{ name: 'a', total_tokens: 900, requests: 3 }],
      performance: [
        {
          model_name: 'a',
          p50_latency_ms: 800,
          p95_latency_ms: 1500,
          error_rate: 0.02,
          enough_samples: true,
        },
      ],
    });
    const a = entries.find((e) => e.id === 'a');
    const b = entries.find((e) => e.id === 'b');
    if (!a || !b) throw new Error('entries a/b missing');
    expect(a.routable).toBe(true);
    expect(a.tokens).toBe(900);
    expect(a.enoughSamples).toBe(true);
    expect(b.routable).toBe(false);
    expect(b.catalogueId).toBe(7);
  });
});

describe('usdPerRatio', () => {
  it('derives from quota_per_unit through money.ts', () => {
    expect(usdPerRatio(USD)).toBeCloseTo(2);
    expect(usdPerRatio(mustMoney({ quota_per_unit: 250000 }))).toBeCloseTo(4);
  });
});

describe('formatPrice', () => {
  it('returns null (rendered as Unpriced) for 0, null and negatives', () => {
    expect(formatPrice(0, USD)).toBeNull();
    expect(formatPrice(null, USD)).toBeNull();
    expect(formatPrice(undefined, USD)).toBeNull();
    expect(formatPrice(-1, USD)).toBeNull();
    expect(formatPrice(Number.NaN, USD)).toBeNull();
  });
  it('keeps enough digits to never read as zero', () => {
    expect(formatPrice(12, USD)).toBe('$12');
    expect(formatPrice(0.27, USD)).toBe('$0.27');
    expect(formatPrice(0.0004, USD)).toBe('$0.0004');
    expect(formatPrice(0.00001, USD)).toBe('<$0.0001');
  });
  it('follows the display currency', () => {
    expect(formatPrice(1, CNY)).toBe('¥7');
    expect(
      formatPrice(
        1,
        parseMoneyConfig({
          quota_per_unit: 500000,
          quota_display_type: 'TOKENS',
        }),
      ),
    ).toBe('$1');
  });
});

describe('filter and sort', () => {
  const entries = buildCatalog({
    usdPerRatio: usdPerRatio(USD),
    routable: [{ id: 'model-a', owned_by: 'VendorA' }],
    pricing: [
      { model_name: 'model-a', vendor: 'VendorA', model_ratio: 2 },
      {
        model_name: 'model-b',
        vendor: 'VendorB',
        model_ratio: 1,
        supported_endpoint_types: ['embeddings'],
        tags: 'cheap, fast',
      },
      { model_name: 'free-z', vendor: 'VendorB', model_ratio: 0 },
    ],
  });

  it('filters by search text, vendor, capability and callability', () => {
    expect(filterCatalog(entries, { q: 'FAST' }).map((e) => e.id)).toEqual([
      'model-b',
    ]);
    expect(
      filterCatalog(entries, { vendors: ['VendorB'] }).map((e) => e.id),
    ).toEqual(['model-b', 'free-z']);
    expect(
      filterCatalog(entries, { capabilities: ['embeddings'] }).map((e) => e.id),
    ).toEqual(['model-b']);
    expect(
      filterCatalog(entries, { routableOnly: true }).map((e) => e.id),
    ).toEqual(['model-a']);
  });

  it('sorts unpriced models last in both price directions', () => {
    expect(sortCatalog(entries, 'input_asc').map((e) => e.id)).toEqual([
      'model-b',
      'model-a',
      'free-z',
    ]);
    expect(sortCatalog(entries, 'output_asc').map((e) => e.id)).toEqual([
      'model-b',
      'model-a',
      'free-z',
    ]);
  });

  it('counts vendors, most models first', () => {
    expect(vendorFacets(entries)[0]).toEqual({ vendor: 'VendorB', count: 2 });
  });
});

describe('snippets', () => {
  it('points an embeddings-only model at /v1/embeddings', () => {
    const s = curlSnippet(
      { id: 'emb', capabilities: ['embeddings'] },
      'https://hub.example',
    );
    expect(s).toContain('https://hub.example/v1/embeddings');
    expect(s).toContain('"model": "emb"');
  });
});

describe('buildCatalog modality and search-unit price', () => {
  const row = (over: Record<string, unknown>) =>
    buildCatalog({
      pricing: [{ model_name: 'rr', ...over }],
    })[0];

  it('keeps an unconfigured price (null) apart from an explicit zero', () => {
    expect(row({ usage_unit: 'search_unit' }).searchUnitPrice).toBeNull();
    expect(
      row({ usage_unit: 'search_unit', search_unit_price: null })
        .searchUnitPrice,
    ).toBeNull();
    expect(
      row({ usage_unit: 'search_unit', search_unit_price: 0 }).searchUnitPrice,
    ).toBe(0);
  });

  it('reads modality and defaults to token billing', () => {
    const e = row({ modality: 'rerank' });
    expect(e.modality).toBe('rerank');
    expect(e.usageUnit).toBe('token');
    expect(row({}).modality).toBe('');
  });

  it('a search-unit model priced at 0 counts as priced (free), an unset one does not', () => {
    expect(
      hasPrice(row({ usage_unit: 'search_unit', search_unit_price: 0 })),
    ).toBe(true);
    expect(hasPrice(row({ usage_unit: 'search_unit' }))).toBe(false);
  });
});

describe('buildCatalog facets and filters', () => {
  const entries = buildCatalog({
    usdPerRatio: 2,
    pricing: [
      {
        model_name: 'tok',
        vendor: 'V1',
        model_ratio: 1,
        tags: 'Fast, chat',
        modality: 'chat',
        icon: 'brand-a',
        enable_groups: ['vip', 'default'],
        supported_endpoint_types: [WIRE_CHAT],
      },
      {
        model_name: 'call',
        vendor: 'V2',
        quota_type: 1,
        model_price: 0.1,
        tags: 'chat',
        modality: 'image',
        enable_groups: ['default'],
      },
      { model_name: 'zero', vendor: 'V2', model_ratio: 0 },
      {
        model_name: 'cfg-off',
        vendor: 'V2',
        model_ratio: 5,
        price_configured: false,
      },
      {
        model_name: 'su',
        usage_unit: 'search_unit',
        search_unit_price: 0.002,
      },
    ],
  });
  const ids = (list: { id: string }[]) => list.map((e) => e.id).sort();

  it('carries the icon key and groups from the API, never from the name', () => {
    const e = entries.find((x) => x.id === 'tok');
    expect(e?.icon).toBe('brand-a');
    expect(e?.groups).toEqual(['vip', 'default']);
    expect(entries.find((x) => x.id === 'zero')?.icon).toBe('');
  });

  it('classifies billing kinds; zero and price_configured=false are unpriced', () => {
    const kind = (id: string) => {
      const e = entries.find((x) => x.id === id);
      if (!e) throw new Error(`missing ${id}`);
      return priceTypeOf(e);
    };
    expect(kind('tok')).toBe('token');
    expect(kind('call')).toBe('request');
    expect(kind('su')).toBe('search_unit');
    expect(kind('zero')).toBe('unpriced');
    expect(kind('cfg-off')).toBe('unpriced');
    expect(priceTypeCounts(entries)).toEqual({
      token: 1,
      request: 1,
      search_unit: 1,
      unpriced: 2,
    });
  });

  it('builds tag and modality facets case-insensitively', () => {
    expect(tagFacets(entries)).toEqual([
      { tag: 'chat', count: 2 },
      { tag: 'fast', count: 1 },
    ]);
    expect(modalityFacets(entries)).toEqual([
      { modality: 'chat', count: 1 },
      { modality: 'image', count: 1 },
    ]);
  });

  it('filters by tag, modality, billing kind and group', () => {
    expect(ids(filterCatalog(entries, { tags: ['FAST'] }))).toEqual(['tok']);
    expect(ids(filterCatalog(entries, { tags: ['chat', 'fast'] }))).toEqual([
      'tok',
    ]);
    expect(ids(filterCatalog(entries, { modalities: ['image'] }))).toEqual([
      'call',
    ]);
    expect(ids(filterCatalog(entries, { priceType: 'unpriced' }))).toEqual([
      'cfg-off',
      'zero',
    ]);
    expect(ids(filterCatalog(entries, { group: 'vip' }))).toEqual(['tok']);
    expect(ids(filterCatalog(entries, { group: 'default' }))).toEqual([
      'call',
      'tok',
    ]);
  });

  it('sorts price descending with unpriced last, and by reliability', () => {
    const priced = buildCatalog({
      usdPerRatio: 2,
      pricing: [
        { model_name: 'a', model_ratio: 1 },
        { model_name: 'b', model_ratio: 3 },
        { model_name: 'c', model_ratio: 0 },
      ],
      performance: [
        { model_name: 'a', error_rate: 0.05, enough_samples: true },
        { model_name: 'b', error_rate: 0, enough_samples: true },
        { model_name: 'c', error_rate: 0.9, enough_samples: false },
      ],
    });
    expect(sortCatalog(priced, 'input_desc').map((e) => e.id)).toEqual([
      'b',
      'a',
      'c',
    ]);
    // 0% errors beats 5%; the unmeasured one is last whatever its raw rate
    expect(sortCatalog(priced, 'reliable').map((e) => e.id)).toEqual([
      'b',
      'a',
      'c',
    ]);
  });

  it('converts per-1M prices to per-1K without inventing a value', () => {
    expect(perUnit(2, 'M')).toBe(2);
    expect(perUnit(2, 'K')).toBe(0.002);
    expect(perUnit(null, 'K')).toBeNull();
    expect(perUnit(Number.NaN, 'M')).toBeNull();
  });
});
