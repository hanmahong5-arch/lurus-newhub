import { queryOptions } from '@tanstack/react-query';

import { tenantApi } from '@/lib/api';

import {
  buildCatalog,
  type CatalogueRow,
  type ModelEntry,
  type PerformanceRow,
  type PricingRow,
  type RoutableRow,
  type UsageRow,
} from './catalog';

interface PricingPayload {
  pricing?: PricingRow[];
  group_ratio?: Record<string, number>;
}

const PAGE = 100;
const MAX_PAGES = 20;

/**
 * GET ~/models answers `{items,total,limit,offset}` (ListModelsV2). Page
 * through it so a catalogue larger than one page is not silently cut.
 */
export async function fetchCatalogue(): Promise<CatalogueRow[]> {
  const all: CatalogueRow[] = [];
  for (let i = 0; i < MAX_PAGES; i++) {
    const d = await tenantApi.get<{ items?: CatalogueRow[]; total?: number }>(
      `/models?limit=${PAGE}&offset=${all.length}`,
    );
    const items = Array.isArray(d?.items) ? d.items : [];
    all.push(...items);
    if (items.length < PAGE || all.length >= (d?.total ?? 0)) break;
  }
  return all;
}

async function fetchRoutable(): Promise<RoutableRow[]> {
  const d = await tenantApi.get<{ items?: RoutableRow[] }>('/models/routable');
  return Array.isArray(d?.items) ? d.items : [];
}

async function fetchPricing(): Promise<PricingPayload> {
  const d = await tenantApi.get<PricingPayload>('/pricing');
  return {
    pricing: Array.isArray(d?.pricing) ? d.pricing : [],
    group_ratio: d?.group_ratio ?? {},
  };
}

/**
 * 7-day tokens/requests per model. The endpoint is tenant-admin gated
 * server-side: callers below tenant admin never ask, and a failed read yields
 * null (unknown), never an invented 0. Together with performance
 * this is the only read allowed to fail quietly.
 */
async function fetchUsage(): Promise<UsageRow[] | null> {
  try {
    const d = await tenantApi.get<{ rows?: UsageRow[] }>(
      '/analytics/rankings?by=model&hours=168',
      { skipAuthRedirect: true },
    );
    return Array.isArray(d?.rows) ? d.rows : [];
  } catch {
    return null;
  }
}

/** 24h p50/p95 and error rate for this tenant; decorative, fails quietly. */
async function fetchPerformance(): Promise<PerformanceRow[]> {
  try {
    const d = await tenantApi.get<{ items?: PerformanceRow[] }>(
      '/models/performance?hours=24',
      { skipAuthRedirect: true },
    );
    return Array.isArray(d?.items) ? d.items : [];
  } catch {
    return [];
  }
}

/** Load the sources and merge them. A failed required read rejects. */
export async function loadModelCatalog(
  group: string,
  usdPerRatio: number,
  canReadUsage = false,
): Promise<ModelEntry[]> {
  const [routable, pricing, catalogue, usage, performance] = await Promise.all([
    fetchRoutable(),
    fetchPricing(),
    fetchCatalogue(),
    canReadUsage ? fetchUsage() : Promise.resolve(null),
    fetchPerformance(),
  ]);
  return buildCatalog({
    routable,
    pricing: pricing.pricing,
    catalogue,
    usage,
    performance,
    groupRatio: pricing.group_ratio?.[group] ?? 1,
    usdPerRatio,
  });
}

export const modelCatalogQueryOptions = (
  group: string,
  usdPerRatio: number,
  canReadUsage = false,
) =>
  queryOptions({
    queryKey: ['models', 'catalog', group, usdPerRatio, canReadUsage],
    queryFn: () => loadModelCatalog(group, usdPerRatio, canReadUsage),
    staleTime: 30_000,
    retry: false,
  });
