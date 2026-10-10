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
import { tenantApi } from '@/lib/api'

/**
 * Grouping dimensions the rankings endpoint accepts (see rankingDimensions in
 * v2_analytics_rankings.go). `vendor` is left out: it is platform-staff only
 * and reveals the supply chain. relay_mode / usage_unit answer "how is
 * retrieval traffic metered".
 */
export const RANKING_DIMENSIONS = [
  'model',
  'relay_mode',
  'usage_unit',
  'group',
  'key',
  'user',
  'product',
] as const

export type RankingDimension = (typeof RANKING_DIMENSIONS)[number]

export interface RankingRow {
  name: string
  rank: number
  requests: number
  total_tokens: number
  quota: number
  token_share_pct: number
  quota_share_pct: number
}

/** Top groups for the trailing window; a failed read throws. */
export async function fetchRankings(
  by: RankingDimension,
  hours = 168
): Promise<RankingRow[]> {
  const d = await tenantApi.get<{ rows?: RankingRow[] } | null>(
    '/analytics/rankings',
    { params: { by, hours } }
  )
  return Array.isArray(d?.rows) ? d.rows : []
}
