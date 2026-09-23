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

// The model marketplace's data model — pure functions, no React, no fetch.
//
// A model can be known to the console three ways, and the page used to read
// only the third:
//   routable  GET ~/models/routable  — what this caller can actually call
//   pricing   GET ~/pricing          — what it costs (ratios, per-call price)
//   catalogue GET ~/models           — the admin-maintained metadata table
// On UAT (2026-09-22) the catalogue table was empty while two models were
// routable and priced, so the page said "0 models". The marketplace is the
// union, keyed by model name, with routability shown per entry.

// One unit of model_ratio is $0.002 per 1K tokens, i.e. $2 per 1M
// (internal/pkg/common/constants.go QuotaPerUnit = 500 000 quota per $1,
// quota = tokens × model_ratio × group_ratio).
export const USD_PER_M_PER_RATIO = 2;

// supported_endpoint_types → capability label key + fallback. Keys follow
// internal/pkg/constant/endpoint_type.go.
export const CAPABILITIES = {
  openai: ['cap_chat', 'Chat'],
  'openai-response': ['cap_responses', 'Responses API'],
  anthropic: ['cap_anthropic', 'Anthropic API'],
  gemini: ['cap_gemini', 'Gemini API'],
  embeddings: ['cap_embeddings', 'Embeddings'],
  'image-generation': ['cap_image', 'Image'],
  'jina-rerank': ['cap_rerank', 'Rerank'],
  'openai-video': ['cap_video', 'Video'],
};

const num = (v) => (typeof v === 'number' && Number.isFinite(v) ? v : null);

/**
 * Merge the three sources into marketplace entries.
 * @param {object} src
 * @param {Array<{id:string, owned_by?:string, supported_endpoint_types?:string[]}>} src.routable
 * @param {Array<object>} src.pricing   rows of GET ~/pricing data.pricing
 * @param {Array<object>} src.catalogue items of GET ~/models
 * @param {Array<{name:string,total_tokens?:number,requests?:number}>} src.usage
 * @param {Array<object>} [src.performance] items of GET ~/models/performance
 * @param {number} [src.groupRatio=1] the caller's group multiplier
 */
export function buildCatalog({
  routable = [],
  pricing = [],
  catalogue = [],
  usage = [],
  performance = [],
  groupRatio = 1,
} = {}) {
  const byName = new Map();
  const entry = (name) => {
    if (!byName.has(name)) {
      byName.set(name, {
        id: name,
        vendor: '',
        description: '',
        tags: [],
        capabilities: [],
        routable: false,
        priced: false,
        quotaType: null,
        inputPerM: null,
        outputPerM: null,
        cacheReadPerM: null,
        perCall: null,
        tokens: 0,
        requests: 0,
        catalogueId: null,
        status: null,
        // Tenant-scoped, recent window; null = no traffic measured.
        p50Ms: null,
        p95Ms: null,
        errorRate: null,
        enoughSamples: false,
      });
    }
    return byName.get(name);
  };
  const addCaps = (e, types) => {
    for (const t of types || []) {
      if (!e.capabilities.includes(t)) e.capabilities.push(t);
    }
  };
  const gr = num(groupRatio) ?? 1;

  for (const r of routable) {
    if (!r?.id) continue;
    const e = entry(r.id);
    e.routable = true;
    if (!e.vendor && r.owned_by) e.vendor = r.owned_by;
    addCaps(e, r.supported_endpoint_types);
  }

  for (const p of pricing) {
    if (!p?.model_name) continue;
    const e = entry(p.model_name);
    e.priced = true;
    if (p.vendor) e.vendor = p.vendor;
    if (p.description) e.description = p.description;
    if (p.tags) {
      e.tags = String(p.tags)
        .split(',')
        .map((t) => t.trim())
        .filter(Boolean);
    }
    addCaps(e, p.supported_endpoint_types);
    e.quotaType = p.quota_type ?? 0;
    if (e.quotaType === 1) {
      const price = num(p.model_price);
      e.perCall = price == null ? null : price * gr;
    } else {
      const ratio = num(p.model_ratio);
      if (ratio != null) {
        e.inputPerM = ratio * USD_PER_M_PER_RATIO * gr;
        const cr = num(p.completion_ratio);
        // completion_ratio 0/absent means "not configured" upstream, where
        // billing falls back to 1 — show the input price, not $0.
        e.outputPerM = e.inputPerM * (cr && cr > 0 ? cr : 1);
        const cache = num(p.cache_ratio);
        e.cacheReadPerM = cache == null ? null : e.inputPerM * cache;
      }
    }
  }

  for (const m of catalogue) {
    if (!m?.model_name) continue;
    const e = entry(m.model_name);
    e.catalogueId = m.id ?? null;
    e.status = m.status ?? null;
    if (!e.vendor && m.vendor) e.vendor = m.vendor;
    if (!e.description && m.description) e.description = m.description;
  }

  for (const u of usage) {
    if (!u?.name || !byName.has(u.name)) continue;
    const e = byName.get(u.name);
    e.tokens = num(u.total_tokens) ?? 0;
    e.requests = num(u.requests) ?? 0;
  }

  for (const pf of performance) {
    if (!pf?.model_name || !byName.has(pf.model_name)) continue;
    const e = byName.get(pf.model_name);
    e.p50Ms = num(pf.p50_latency_ms) || null;
    e.p95Ms = num(pf.p95_latency_ms) || null;
    e.errorRate = num(pf.error_rate);
    e.enoughSamples = !!pf.enough_samples;
  }

  return Array.from(byName.values());
}

/** Distinct vendors with counts, most models first. */
export function vendorFacets(entries) {
  const counts = new Map();
  for (const e of entries) {
    const v = e.vendor || '';
    counts.set(v, (counts.get(v) || 0) + 1);
  }
  return Array.from(counts, ([vendor, count]) => ({ vendor, count })).sort(
    (a, b) => b.count - a.count || a.vendor.localeCompare(b.vendor),
  );
}

/** Distinct capabilities with counts, in CAPABILITIES order. */
export function capabilityFacets(entries) {
  const counts = new Map();
  for (const e of entries) {
    for (const c of e.capabilities) counts.set(c, (counts.get(c) || 0) + 1);
  }
  const order = Object.keys(CAPABILITIES);
  return Array.from(counts, ([capability, count]) => ({
    capability,
    count,
  })).sort((a, b) => {
    const ia = order.indexOf(a.capability);
    const ib = order.indexOf(b.capability);
    return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib);
  });
}

/**
 * @param {Array} entries
 * @param {{q?:string, vendors?:string[], capabilities?:string[], routableOnly?:boolean}} f
 */
export function filterCatalog(entries, f = {}) {
  const q = (f.q || '').trim().toLowerCase();
  const vendors = f.vendors || [];
  const caps = f.capabilities || [];
  return entries.filter((e) => {
    if (f.routableOnly && !e.routable) return false;
    if (vendors.length && !vendors.includes(e.vendor || '')) return false;
    if (caps.length && !caps.every((c) => e.capabilities.includes(c)))
      return false;
    if (q) {
      const hay =
        `${e.id} ${e.vendor} ${e.description} ${e.tags.join(' ')}`.toLowerCase();
      if (!hay.includes(q)) return false;
    }
    return true;
  });
}

// Unpriced entries sort after priced ones whichever way price is sorted.
const priceKey = (e, field) => {
  if (e.quotaType === 1) return e.perCall;
  return e[field];
};

export const SORTS = ['popular', 'name', 'input_asc', 'output_asc', 'fastest'];

export function sortCatalog(entries, sort = 'popular') {
  const out = entries.slice();
  const byName = (a, b) => a.id.localeCompare(b.id);
  const byPrice = (field) => (a, b) => {
    const pa = priceKey(a, field);
    const pb = priceKey(b, field);
    if (pa == null && pb == null) return byName(a, b);
    if (pa == null) return 1;
    if (pb == null) return -1;
    return pa - pb || byName(a, b);
  };
  switch (sort) {
    case 'name':
      return out.sort(byName);
    case 'input_asc':
      return out.sort(byPrice('inputPerM'));
    case 'output_asc':
      return out.sort(byPrice('outputPerM'));
    case 'fastest':
      // Only measurements with enough samples rank; the rest follow by name.
      return out.sort((a, b) => {
        const pa = a.enoughSamples ? a.p50Ms : null;
        const pb = b.enoughSamples ? b.p50Ms : null;
        if (pa == null && pb == null) return byName(a, b);
        if (pa == null) return 1;
        if (pb == null) return -1;
        return pa - pb || byName(a, b);
      });
    default:
      // Popular: tokens, then requests, then routable before not, then name.
      return out.sort(
        (a, b) =>
          b.tokens - a.tokens ||
          b.requests - a.requests ||
          Number(b.routable) - Number(a.routable) ||
          byName(a, b),
      );
  }
}

/** "$0.27" / "$0.0004" / "$12" — enough digits to never read as $0. */
export function fmtUsd(v) {
  if (v == null) return '—';
  if (v === 0) return '$0';
  const abs = Math.abs(v);
  const digits = abs >= 100 ? 0 : abs >= 1 ? 2 : abs >= 0.01 ? 3 : 4;
  return `$${Number(v.toFixed(digits))}`;
}

/** 1234 → "1.2K", 3_400_000 → "3.4M". */
export function fmtCompact(n) {
  if (!n) return '0';
  const units = [
    [1e9, 'B'],
    [1e6, 'M'],
    [1e3, 'K'],
  ];
  for (const [d, u] of units) {
    if (n >= d) return `${Number((n / d).toFixed(1))}${u}`;
  }
  return String(n);
}

/** 850 → "850ms", 1234 → "1.2s". */
export function fmtMs(ms) {
  if (ms == null) return '—';
  return ms >= 1000 ? `${Number((ms / 1000).toFixed(1))}s` : `${ms}ms`;
}

/** 0.0213 → "2.1%". */
export function fmtPct(r) {
  if (r == null) return '—';
  return `${Number((r * 100).toFixed(1))}%`;
}
