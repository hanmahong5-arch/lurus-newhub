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

/**
 * Wire shapes of the tenant token endpoints (handler/v2_token.go, tokenView).
 * The list never carries the plaintext secret: `key` is masked. The full key
 * is returned exactly once, by create and rotate.
 */

/** common.TokenStatus* */
export const TOKEN_STATUS_ENABLED = 1
export const TOKEN_STATUS_DISABLED = 2
export const TOKEN_STATUS_EXPIRED = 3
export const TOKEN_STATUS_EXHAUSTED = 4

export interface ApiToken {
  id: number
  name: string
  /** Masked by the server (first4****last4). */
  key: string
  status: number
  created_time: number
  accessed_time: number
  /** Unix seconds; -1 = never expires. */
  expired_time: number
  /** Remaining quota in internal units (ignored when unlimited). */
  remain_quota: number
  used_quota: number
  unlimited_quota: boolean
  model_limits_enabled: boolean
  /** Comma-separated allowlist. */
  model_limits: string
  allow_ips: string
  /** Cost-attribution project; 0 = unassigned. */
  project_id: number
}

export interface TokenListResult {
  items: ApiToken[]
  total: number
  page: number
  pageSize: number
}

export interface ProjectOption {
  id: number
  name: string
}

export interface RoutableModel {
  id: string
  supported_endpoint_types: string[]
}

/** Result of create / rotate: the only moment the plaintext key exists. */
export interface IssuedKey {
  id: number
  key: string
  name?: string
}

/** Body shared by create and update (update sends all of it). */
export interface TokenWriteBody {
  name: string
  expired_time: number
  unlimited_quota: boolean
  remain_quota: number
  model_limits_enabled: boolean
  model_limits: string
  allow_ips: string
  project_id: number
}
