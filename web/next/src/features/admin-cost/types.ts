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
/** One spend row (GET /api/v2/admin/governance/savings `spend_by_model`). */
export interface ModelSpend {
  model_name: string
  count: number
  total_quota: number
  prompt_tokens?: number
  completion_tokens?: number
}

export interface ProductSpend {
  product: string
  count: number
  total_quota: number
}

export interface Opportunity {
  model: string
  alt_model: string
  current_quota?: number
  savings_quota: number
  savings_pct: number
}

export interface Scenario {
  total_savings_quota: number
  savings_pct: number
  top_opportunities: Opportunity[]
}

export interface Excluded {
  model: string
  quota?: number
  reason: string
}

export type ScenarioId = 'conservative' | 'aggressive'

export interface SavingsReport {
  window_hours?: number
  quota_per_usd?: number
  heuristic?: boolean
  caveat?: string
  total_spend_quota: number
  spend_by_model: ModelSpend[]
  spend_by_product: ProductSpend[]
  scenarios: Partial<Record<ScenarioId, Scenario>>
  excluded: Excluded[]
}
