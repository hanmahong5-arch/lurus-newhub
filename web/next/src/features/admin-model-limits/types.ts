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
/** One `(tenant, model)` rate-limit row (entity.ModelRateLimit JSON tags). */
export interface ModelLimit {
  id: number
  tenant_id: string
  model: string
  /** Requests per minute; 0 = unlimited. */
  rate_limit_rpm: number
  /** Tokens per minute; 0 = unlimited. */
  rate_limit_tpm: number
  updated_at?: string
}

export interface TenantOption {
  id: string
  name: string
}

export interface ModelLimitBody {
  model: string
  rate_limit_rpm: number
  rate_limit_tpm: number
}

/**
 * Platform-wide allow-list mode (TENANT_MODEL_ALLOWLIST_MODE). `observe`
 * counts a miss, `enforce` refuses it with HTTP 403. Anything else the server
 * might report is treated as observe by the UI but kept verbatim here.
 */
export type AllowlistMode = 'observe' | 'enforce' | (string & {})

export interface Allowlist {
  /** False = no row: the tenant is unrestricted. */
  configured: boolean
  allowed_models: string[]
  mode: AllowlistMode
}
