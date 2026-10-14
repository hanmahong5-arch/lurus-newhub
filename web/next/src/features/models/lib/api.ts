import { queryOptions } from '@tanstack/react-query';

import { tenantApi } from '@/lib/api';

import type {
  CatalogueRow,
  PerformanceRow,
  PricingRow,
  RoutableRow,
  UsageRow,
} from './catalog';
import type { PerfWindow } from './perf';

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

/**
 * p50/p95 and error rate for this tenant over the last `hours` (the server
 * accepts 1, 24 or 168); decorative, so a failed read yields no rows rather
 * than an error.
 */
async function fetchPerformance(hours: PerfWindow): Promise<PerformanceRow[]> {
  try {
    const d = await tenantApi.get<{ items?: PerformanceRow[] }>(
      `/models/performance?hours=${hours}`,
      { skipAuthRedirect: true },
    );
    return Array.isArray(d?.items) ? d.items : [];
  } catch {
    return [];
  }
}

/** Everything the catalogue is built from except latency, un-merged. */
export interface ModelSources {
  routable: RoutableRow[];
  pricing: PricingRow[];
  /** Multiplier per group the caller may use. */
  groupRatio: Record<string, number>;
  catalogue: CatalogueRow[];
  /** null = usage could not be read (no permission or failed), not zero. */
  usage: UsageRow[] | null;
}

/**
 * Load the sources. A failed required read rejects. The merge into entries is
 * left to the caller (buildCatalog) so changing the selected group, the
 * latency window or the display currency never refetches.
 */
export async function loadModelSources(
  canReadUsage = false,
): Promise<ModelSources> {
  const [routable, pricing, catalogue, usage] = await Promise.all([
    fetchRoutable(),
    fetchPricing(),
    fetchCatalogue(),
    canReadUsage ? fetchUsage() : Promise.resolve(null),
  ]);
  return {
    routable,
    pricing: pricing.pricing ?? [],
    groupRatio: pricing.group_ratio ?? {},
    catalogue,
    usage,
  };
}

export const modelSourcesQueryOptions = (canReadUsage = false) =>
  queryOptions({
    queryKey: ['models', 'sources', canReadUsage],
    queryFn: () => loadModelSources(canReadUsage),
    staleTime: 30_000,
    retry: false,
  });

export const modelPerformanceQueryOptions = (hours: PerfWindow) =>
  queryOptions({
    queryKey: ['models', 'performance', hours],
    queryFn: () => fetchPerformance(hours),
    staleTime: 30_000,
    retry: false,
  });
