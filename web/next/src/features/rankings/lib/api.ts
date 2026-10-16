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
import { queryOptions } from '@tanstack/react-query';

import { api, tenantApi } from '@/lib/api';

import type { RankingDimension, RankingHours } from './periods';

/** One leaderboard row (repo.RankingRow). */
export interface RankingRow {
  name: string;
  rank: number;
  /** previous rank - current rank: positive = climbed. */
  rank_delta: number;
  /** Absent from the previous window; rank_delta is then 0. */
  is_new: boolean;
  requests: number;
  total_tokens: number;
  /** Internal quota units; show through useQuota, never as dollars. */
  quota: number;
  token_share_pct: number;
  quota_share_pct: number;
  /** null = no previous window to compare with. */
  requests_growth_pct: number | null;
}

/** Hourly usage of one leaderboard name (repo.RankingSeriesPoint). */
export interface RankingSeriesPoint {
  /** Unix seconds, start of the hour. */
  t: number;
  name: string;
  tokens: number;
  requests: number;
  quota: number;
}

export interface RankingsData {
  by: string;
  hours: number;
  window: { start: number; end: number };
  cached_at: number;
  rows: RankingRow[];
  total_tokens: number;
  total_quota: number;
  series: RankingSeriesPoint[];
}

/**
 * Which traffic the board covers. Absent = the caller's own tenant (the only
 * view a non-root user has). `{ tenantId }` is the platform-root cross-tenant
 * view: '' = every tenant, otherwise that one tenant.
 */
export interface RankingScope {
  tenantId: string;
}

function normalize(
  d: Partial<RankingsData> | null,
  by: RankingDimension,
  hours: RankingHours,
): RankingsData {
  return {
    by: d?.by ?? by,
    hours: d?.hours ?? hours,
    window: d?.window ?? { start: 0, end: 0 },
    cached_at: d?.cached_at ?? 0,
    rows: Array.isArray(d?.rows) ? d.rows : [],
    total_tokens: d?.total_tokens ?? 0,
    total_quota: d?.total_quota ?? 0,
    series: Array.isArray(d?.series) ? d.series : [],
  };
}

/**
 * GET ~/analytics/rankings. Tenant admins only (a lower role is refused with
 * 403, which surfaces as an error here, not as an empty board). The server
 * caches each (tenant, by, hours) for five minutes per pod.
 *
 * With a `scope` it reads GET /api/v2/admin/analytics/rankings instead
 * (platform root only; same response, optional `tenant_id` filter).
 */
export async function fetchRankings(
  by: RankingDimension,
  hours: RankingHours,
  scope?: RankingScope,
): Promise<RankingsData> {
  if (scope) {
    const params: Record<string, string | number> = { by, hours };
    if (scope.tenantId) params.tenant_id = scope.tenantId;
    const d = await api.get<Partial<RankingsData> | null>(
      '/api/v2/admin/analytics/rankings',
      { params },
    );
    return normalize(d, by, hours);
  }
  const d = await tenantApi.get<Partial<RankingsData> | null>(
    '/analytics/rankings',
    { params: { by, hours } },
  );
  return normalize(d, by, hours);
}

export const rankingsQueryOptions = (
  by: RankingDimension,
  hours: RankingHours,
  scope?: RankingScope,
) =>
  queryOptions({
    queryKey: scope
      ? ['rankings', 'admin', scope.tenantId, by, hours]
      : ['rankings', by, hours],
    queryFn: () => fetchRankings(by, hours, scope),
    // The server answer is cached for 5 minutes; asking sooner returns the
    // same snapshot and spends the critical rate limit.
    staleTime: 5 * 60 * 1000,
    refetchOnWindowFocus: false,
    retry: false,
  });

/** One row of the root tenant picker (GET /api/v2/admin/tenants). */
export interface TenantOption {
  id: string;
  name?: string;
  slug?: string;
}

const TENANTS_PATH = '/api/v2/admin/tenants';
const TENANT_PAGE_SIZE = 100;
const MAX_TENANT_PAGES = 20;

/**
 * Tenant filter options for the root view. The list is a convenience, so a
 * failure is not fatal to the board: the picker then offers "All tenants"
 * only, with whatever pages were read before the failure.
 */
export async function fetchTenantOptions(): Promise<TenantOption[]> {
  const out: TenantOption[] = [];
  for (let page = 1; page <= MAX_TENANT_PAGES; page++) {
    let body: { tenants?: TenantOption[]; total?: number } | null;
    try {
      body = await api.get<{ tenants?: TenantOption[]; total?: number } | null>(
        TENANTS_PATH,
        {
          params: { page, page_size: TENANT_PAGE_SIZE },
          skipAuthRedirect: true,
        },
      );
    } catch {
      break;
    }
    const rows = Array.isArray(body?.tenants) ? body.tenants : [];
    out.push(...rows);
    if (rows.length === 0 || out.length >= (body?.total ?? 0)) break;
  }
  return out;
}

export const tenantOptionsQuery = queryOptions({
  queryKey: ['rankings', 'tenant-options'],
  queryFn: fetchTenantOptions,
  staleTime: 60_000,
  retry: false,
  meta: { errorToast: false },
});
