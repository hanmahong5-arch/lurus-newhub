import { quotaToUsd, type MoneyConfig } from '@/lib/money';
import {
  WIRE_CHAT,
  WIRE_GENERATE,
  WIRE_MESSAGES,
  WIRE_RESPONSES,
  WIRE_VIDEO,
} from './wire';

// The model marketplace's data model: pure functions, no React, no fetch.
//
// A model is known to the console three ways:
//   routable  GET ~/models/routable  what this caller can actually call
//   pricing   GET ~/pricing          what it costs (ratios, per-call price)
//   catalogue GET ~/models           the admin-maintained metadata table
// The marketplace is the union keyed by model name, with routability shown
// per entry (the catalogue table alone can be empty while models are live).

/**
 * USD per 1M tokens for one unit of model_ratio, derived from the server's
 * quota_per_unit via lib/money.ts (quota = tokens x model_ratio x group_ratio,
 * so 1M tokens at ratio 1 is 1e6 quota). Callers pass the result to
 * buildCatalog; this module carries no unit-price constant of its own.
 */
export function usdPerRatio(config: MoneyConfig): number {
  return quotaToUsd(1_000_000, config);
}

/** supported_endpoint_types -> label (English source text) and relay path. */
export const CAPABILITIES: Record<string, { label: string; path: string }> = {
  [WIRE_CHAT]: { label: 'Chat', path: 'POST /v1/chat/completions' },
  [WIRE_RESPONSES]: {
    label: 'Responses API',
    path: 'POST /v1/responses',
  },
  [WIRE_MESSAGES]: { label: 'Messages API', path: 'POST /v1/messages' },
  [WIRE_GENERATE]: {
    label: 'Generate-content API',
    path: 'POST /v1beta/models/{model}:generateContent',
  },
  embeddings: { label: 'Embeddings', path: 'POST /v1/embeddings' },
  'image-generation': {
    label: 'Image',
    path: 'POST /v1/images/generations',
  },
  'jina-rerank': { label: 'Rerank', path: 'POST /v1/rerank' },
  [WIRE_VIDEO]: { label: 'Video', path: 'POST /v1/video/generations' },
  systemone: { label: 'System One', path: 'POST /v1/systemone' },
};

export interface RoutableRow {
  id: string;
  owned_by?: string;
  supported_endpoint_types?: string[];
}

export interface PricingRow {
  model_name: string;
  vendor?: string;
  description?: string;
  tags?: string;
  quota_type?: number;
  model_ratio?: number | null;
  model_price?: number | null;
  completion_ratio?: number | null;
  cache_ratio?: number | null;
  supported_endpoint_types?: string[] | null;
}

export interface CatalogueRow {
  id?: number;
  model_name: string;
  vendor?: string;
  description?: string;
  status?: number;
}

export interface UsageRow {
  name: string;
  total_tokens?: number;
  requests?: number;
}

export interface PerformanceRow {
  model_name: string;
  p50_latency_ms?: number;
  p95_latency_ms?: number;
  error_rate?: number;
  enough_samples?: boolean;
}

export interface ModelEntry {
  id: string;
  vendor: string;
  description: string;
  tags: string[];
  capabilities: string[];
  routable: boolean;
  priced: boolean;
  /** 1 = billed per call; anything else = per token. */
  quotaType: number | null;
  inputPerM: number | null;
  /** null = not configured. See inputOnly for models that bill no output. */
  outputPerM: number | null;
  /** True when the model bills input tokens only (output is really free). */
  inputOnly: boolean;
  cacheReadPerM: number | null;
  perCall: number | null;
  /** 7d usage; null = unknown (no permission / read failed), not zero. */
  tokens: number | null;
  requests: number | null;
  catalogueId: number | null;
  status: number | null;
  p50Ms: number | null;
  p95Ms: number | null;
  errorRate: number | null;
  enoughSamples: boolean;
}

export interface BuildCatalogInput {
  routable?: RoutableRow[];
  pricing?: PricingRow[];
  catalogue?: CatalogueRow[];
  /** null = usage could not be read; every entry then has unknown usage. */
  usage?: UsageRow[] | null;
  performance?: PerformanceRow[];
  groupRatio?: number;
  /** USD per 1M tokens per unit of model_ratio; see usdPerRatio(). */
  usdPerRatio?: number;
}

const num = (v: unknown): number | null =>
  typeof v === 'number' && Number.isFinite(v) ? v : null;

/** Whether the chat wire can serve this entry (unknown counts as yes). */
export const entryCanChat = (e: Pick<ModelEntry, 'capabilities'>): boolean =>
  e.capabilities.length === 0 || e.capabilities.includes(WIRE_CHAT);

export function buildCatalog({
  routable = [],
  pricing = [],
  catalogue = [],
  usage = [],
  performance = [],
  groupRatio = 1,
  usdPerRatio: usdPerRatioArg = 0,
}: BuildCatalogInput = {}): ModelEntry[] {
  const byName = new Map<string, ModelEntry>();
  const entry = (name: string): ModelEntry => {
    let e = byName.get(name);
    if (!e) {
      e = {
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
        inputOnly: false,
        cacheReadPerM: null,
        perCall: null,
        tokens: usage === null ? null : 0,
        requests: usage === null ? null : 0,
        catalogueId: null,
        status: null,
        p50Ms: null,
        p95Ms: null,
        errorRate: null,
        enoughSamples: false,
      };
      byName.set(name, e);
    }
    return e;
  };
  const addCaps = (e: ModelEntry, types?: string[] | null) => {
    for (const t of types ?? []) {
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
        e.inputPerM = ratio * usdPerRatioArg * gr;
        const cr = num(p.completion_ratio);
        // completion_ratio 0/absent means "not configured" upstream, where
        // billing falls back to 1. System One bills input tokens only.
        e.inputOnly = (p.supported_endpoint_types ?? []).includes('systemone');
        e.outputPerM = e.inputOnly
          ? null
          : e.inputPerM * (cr && cr > 0 ? cr : 1);
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

  for (const u of usage ?? []) {
    const e = u?.name ? byName.get(u.name) : undefined;
    if (!e) continue;
    e.tokens = num(u.total_tokens) ?? 0;
    e.requests = num(u.requests) ?? 0;
  }

  for (const pf of performance) {
    const e = pf?.model_name ? byName.get(pf.model_name) : undefined;
    if (!e) continue;
    e.p50Ms = num(pf.p50_latency_ms) || null;
    e.p95Ms = num(pf.p95_latency_ms) || null;
    e.errorRate = num(pf.error_rate);
    e.enoughSamples = !!pf.enough_samples;
  }

  return [...byName.values()];
}

/** A usable price exists: a positive per-call price, or a positive input price. */
export function hasPrice(e: ModelEntry): boolean {
  if (e.quotaType === 1) return (e.perCall ?? 0) > 0;
  return (e.inputPerM ?? 0) > 0;
}

export function vendorFacets(entries: ModelEntry[]) {
  const counts = new Map<string, number>();
  for (const e of entries) {
    counts.set(e.vendor, (counts.get(e.vendor) ?? 0) + 1);
  }
  return Array.from(counts, ([vendor, count]) => ({ vendor, count })).sort(
    (a, b) => b.count - a.count || a.vendor.localeCompare(b.vendor),
  );
}

export function capabilityFacets(entries: ModelEntry[]) {
  const counts = new Map<string, number>();
  for (const e of entries) {
    for (const c of e.capabilities) counts.set(c, (counts.get(c) ?? 0) + 1);
  }
  const order = Object.keys(CAPABILITIES);
  const rank = (c: string) => {
    const i = order.indexOf(c);
    return i < 0 ? 99 : i;
  };
  return Array.from(counts, ([capability, count]) => ({
    capability,
    count,
  })).sort((a, b) => rank(a.capability) - rank(b.capability));
}

export interface CatalogFilter {
  q?: string;
  vendors?: string[];
  capabilities?: string[];
  routableOnly?: boolean;
}

export function filterCatalog(
  entries: ModelEntry[],
  f: CatalogFilter = {},
): ModelEntry[] {
  const q = (f.q ?? '').trim().toLowerCase();
  const vendors = f.vendors ?? [];
  const caps = f.capabilities ?? [];
  return entries.filter((e) => {
    if (f.routableOnly && !e.routable) return false;
    if (vendors.length && !vendors.includes(e.vendor)) return false;
    if (caps.length && !caps.every((c) => e.capabilities.includes(c))) {
      return false;
    }
    if (q) {
      const hay =
        `${e.id} ${e.vendor} ${e.description} ${e.tags.join(' ')}`.toLowerCase();
      if (!hay.includes(q)) return false;
    }
    return true;
  });
}

export const SORTS = [
  'popular',
  'name',
  'input_asc',
  'output_asc',
  'fastest',
] as const;
export type SortKey = (typeof SORTS)[number];

// Unpriced entries (null or 0) sort after priced ones whichever way price
// is sorted.
const priceKey = (e: ModelEntry, field: 'inputPerM' | 'outputPerM') => {
  const v = e.quotaType === 1 ? e.perCall : e[field];
  return v == null || v <= 0 ? null : v;
};

export function sortCatalog(
  entries: ModelEntry[],
  sort: SortKey = 'popular',
): ModelEntry[] {
  const out = [...entries];
  const byName = (a: ModelEntry, b: ModelEntry) => a.id.localeCompare(b.id);
  const byKey =
    (key: (e: ModelEntry) => number | null) =>
    (a: ModelEntry, b: ModelEntry) => {
      const pa = key(a);
      const pb = key(b);
      if (pa == null && pb == null) return byName(a, b);
      if (pa == null) return 1;
      if (pb == null) return -1;
      return pa - pb || byName(a, b);
    };
  switch (sort) {
    case 'name':
      return out.sort(byName);
    case 'input_asc':
      return out.sort(byKey((e) => priceKey(e, 'inputPerM')));
    case 'output_asc':
      return out.sort(byKey((e) => priceKey(e, 'outputPerM')));
    case 'fastest':
      return out.sort(byKey((e) => (e.enoughSamples ? e.p50Ms : null)));
    default:
      return out.sort(
        (a, b) =>
          (b.tokens ?? 0) - (a.tokens ?? 0) ||
          (b.requests ?? 0) - (a.requests ?? 0) ||
          Number(b.routable) - Number(a.routable) ||
          byName(a, b),
      );
  }
}

/**
 * "$0.27" / "$0.0004" / "$12" in the operator's display currency (amount x
 * money.rate, symbol from money.ts config): enough digits never to read as 0.
 * Returns null for null, zero or non-finite input; the caller renders
 * "Unpriced". With the TOKENS display type prices stay in USD.
 */
export function formatPrice(
  usd: number | null | undefined,
  config: MoneyConfig | null,
): string | null {
  if (usd == null || !Number.isFinite(usd) || usd <= 0) return null;
  const tokens = config?.displayType === 'TOKENS';
  const rate = config && !tokens ? config.rate : 1;
  const symbol = config && !tokens ? config.symbol : '$';
  const v = usd * rate;
  let digits = 4;
  if (v >= 100) digits = 0;
  else if (v >= 1) digits = 2;
  else if (v >= 0.01) digits = 3;
  const s = Number(v.toFixed(digits));
  // A positive price below the last digit must not collapse to zero.
  return s === 0
    ? `<${symbol}${(10 ** -digits).toFixed(digits)}`
    : `${symbol}${s}`;
}

/** 1234 -> "1.2K", 3_400_000 -> "3.4M". */
export function formatCompact(n: number): string {
  if (!n) return '0';
  const units: Array<[number, string]> = [
    [1e9, 'B'],
    [1e6, 'M'],
    [1e3, 'K'],
  ];
  for (const [d, u] of units) {
    if (n >= d) return `${Number((n / d).toFixed(1))}${u}`;
  }
  return String(n);
}

/** 850 -> "850ms", 1234 -> "1.2s". */
export function formatMs(ms: number | null): string {
  if (ms == null) return '--';
  return ms >= 1000 ? `${Number((ms / 1000).toFixed(1))}s` : `${ms}ms`;
}

/** 0.0213 -> "2.1%". */
export function formatPct(r: number | null): string {
  if (r == null) return '--';
  return `${Number((r * 100).toFixed(1))}%`;
}

/** i18n key (English source text) for a capability; unknown ones show raw. */
export const capabilityLabelKey = (c: string): string =>
  CAPABILITIES[c]?.label ?? c;
