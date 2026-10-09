import { describe, expect, it } from 'vitest';

import {
  buildCatalog,
  filterCatalog,
  formatPrice,
  hasPrice,
  sortCatalog,
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
