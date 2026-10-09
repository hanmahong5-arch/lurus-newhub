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
import {
  TOKEN_STATUS_DISABLED,
  TOKEN_STATUS_ENABLED,
  TOKEN_STATUS_EXHAUSTED,
  TOKEN_STATUS_EXPIRED,
  type ApiToken,
  type IssuedKey,
  type ProjectOption,
  type RoutableModel,
  type TokenListResult,
} from '../types'

function num(value: unknown, fallback = 0): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function record(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null
    ? (value as Record<string, unknown>)
    : {}
}

/** One element of GET /api/v2/~/tokens `items`. Null-safe on every field. */
export function mapToken(raw: unknown): ApiToken {
  const r = record(raw)
  return {
    id: num(r.id),
    name: str(r.name),
    key: str(r.key),
    status: num(r.status, TOKEN_STATUS_ENABLED),
    created_time: num(r.created_time),
    accessed_time: num(r.accessed_time),
    expired_time: num(r.expired_time, -1),
    remain_quota: num(r.remain_quota),
    used_quota: num(r.used_quota),
    unlimited_quota: r.unlimited_quota === true,
    model_limits_enabled: r.model_limits_enabled === true,
    model_limits: str(r.model_limits),
    // allow_ips is a nullable *string on the wire.
    allow_ips: str(r.allow_ips),
    project_id: num(r.project_id),
  }
}

/** `{items,total,page,page_size}`; a missing `items` is an empty page. */
export function mapTokenList(raw: unknown): TokenListResult {
  const r = record(raw)
  const items = Array.isArray(r.items) ? r.items.map(mapToken) : []
  return {
    items,
    total: num(r.total, items.length),
    page: num(r.page, 1),
    pageSize: num(r.page_size, items.length),
  }
}

export function mapIssuedKey(raw: unknown): IssuedKey {
  const r = record(raw)
  return { id: num(r.id), key: str(r.key), name: str(r.name) || undefined }
}

export function mapProjects(raw: unknown): ProjectOption[] {
  const items = record(raw).items
  if (!Array.isArray(items)) return []
  return items
    .map((p) => {
      const r = record(p)
      return { id: num(r.id), name: str(r.name), deleted: r.deleted === true }
    })
    .filter((p) => p.id > 0 && !p.deleted)
    .map(({ id, name }) => ({ id, name }))
}

export function mapRoutableModels(raw: unknown): RoutableModel[] {
  const items = record(raw).items
  if (!Array.isArray(items)) return []
  return items
    .map((m) => {
      const r = record(m)
      return {
        id: str(r.id),
        supported_endpoint_types: Array.isArray(r.supported_endpoint_types)
          ? r.supported_endpoint_types.filter(
              (x): x is string => typeof x === 'string'
            )
          : [],
      }
    })
    .filter((m) => m.id !== '')
}

export type TokenState =
  | 'enabled'
  | 'near-cap'
  | 'disabled'
  | 'expired'
  | 'exhausted'

/**
 * Display state. The server stores 1/2/3/4 but only flips 3/4 lazily, so an
 * "enabled" row can already be past its expiry or out of quota; derive that
 * here the way the legacy console did.
 */
export function tokenState(token: ApiToken, nowSec: number): TokenState {
  if (token.status === TOKEN_STATUS_DISABLED) return 'disabled'
  if (token.status === TOKEN_STATUS_EXPIRED) return 'expired'
  if (token.status === TOKEN_STATUS_EXHAUSTED) return 'exhausted'
  if (token.expired_time > 0 && token.expired_time < nowSec) return 'expired'
  if (!token.unlimited_quota) {
    if (token.remain_quota <= 0) return 'exhausted'
    const total = token.used_quota + token.remain_quota
    if (total > 0 && token.used_quota / total >= 0.9) return 'near-cap'
  }
  return 'enabled'
}

/** Stored status is "enabled" (the only state a person can switch off). */
export function isStoredEnabled(token: ApiToken): boolean {
  return token.status === TOKEN_STATUS_ENABLED
}
