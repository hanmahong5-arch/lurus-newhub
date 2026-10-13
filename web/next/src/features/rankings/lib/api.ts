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

import { tenantApi } from '@/lib/api';

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
 * GET ~/analytics/rankings. Tenant admins only (a lower role is refused with
 * 403, which surfaces as an error here, not as an empty board). The server
 * caches each (tenant, by, hours) for five minutes per pod.
 */
export async function fetchRankings(
  by: RankingDimension,
  hours: RankingHours,
): Promise<RankingsData> {
  const d = await tenantApi.get<Partial<RankingsData> | null>(
    '/analytics/rankings',
    { params: { by, hours } },
  );
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

export const rankingsQueryOptions = (
  by: RankingDimension,
  hours: RankingHours,
) =>
  queryOptions({
    queryKey: ['rankings', by, hours],
    queryFn: () => fetchRankings(by, hours),
    // The server answer is cached for 5 minutes; asking sooner returns the
    // same snapshot and spends the critical rate limit.
    staleTime: 5 * 60 * 1000,
    refetchOnWindowFocus: false,
    retry: false,
  });
