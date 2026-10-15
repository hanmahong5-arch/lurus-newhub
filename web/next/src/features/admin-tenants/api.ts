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
import { api, isApiError } from '@/lib/api'

import {
  INVITE_LIST_PAGE_SIZE,
  LIST_FETCH_MAX_PAGES,
  LIST_FETCH_PAGE_SIZE,
  POOL_USAGE_LIMIT,
} from './constants'
import type {
  CreditPool,
  IssuedInvite,
  PoolDraw,
  Tenant,
  TenantAction,
  TenantInviteList,
  TenantList,
  TenantListPayload,
  TenantStats,
  TopupResult,
} from './types'

export const tenantsQueryKey = ['admin-tenants'] as const

const BASE = '/api/v2/admin/tenants'

function tenantPath(id: string): string {
  return `${BASE}/${encodeURIComponent(id)}`
}

/**
 * GET /api/v2/admin/tenants. The endpoint filters by status only (it ignores
 * `keyword`), so the console reads every page and filters in the browser; a
 * page that fails fails the whole read rather than yielding a partial list.
 */
export async function listTenants(): Promise<TenantList> {
  const all: Tenant[] = []
  let total = 0
  for (let page = 1; page <= LIST_FETCH_MAX_PAGES; page++) {
    const payload = await api.get<TenantListPayload>(BASE, {
      params: { page, page_size: LIST_FETCH_PAGE_SIZE },
    })
    const rows = payload?.tenants ?? payload?.items ?? []
    all.push(...rows)
    total = Math.max(payload?.total ?? 0, all.length)
    if (rows.length === 0 || all.length >= total) break
  }
  return { tenants: all, total }
}

/** Body of POST /api/v2/admin/tenants (wire names, see handler.CreateTenant). */
export interface CreateTenantBody {
  zitadel_org_id: string
  slug: string
  name: string
  plan_type: string
  max_users?: number
  max_quota?: number
  rate_limit_rpm?: number
  rate_limit_tpm?: number
}

export function createTenant(body: CreateTenantBody): Promise<Tenant> {
  return api.post<Tenant>(BASE, body)
}

/** Body of PUT /api/v2/admin/tenants/:id; only the keys present are changed. */
export interface UpdateTenantBody {
  name?: string
  plan_type?: string
  max_users?: number
  max_quota?: number
  rate_limit_rpm?: number
  rate_limit_tpm?: number
  wallet_authoritative?: boolean
}

export function updateTenant(
  id: string,
  body: UpdateTenantBody
): Promise<Tenant> {
  return api.put<Tenant>(tenantPath(id), body)
}

/** POST /:id/enable | /disable | /suspend (empty body). */
export function runTenantAction(
  id: string,
  action: TenantAction
): Promise<unknown> {
  return api.post(`${tenantPath(id)}/${action}`, {})
}

export function getTenantStats(id: string): Promise<TenantStats> {
  return api.get<TenantStats>(`${tenantPath(id)}/stats`)
}

/** The pool, or null when the tenant has none (404). Anything else throws. */
export async function getCreditPool(id: string): Promise<CreditPool | null> {
  try {
    return await api.get<CreditPool>(`${tenantPath(id)}/credit-pool`)
  } catch (error) {
    if (isApiError(error) && error.status === 404) return null
    throw error
  }
}

export interface CreatePoolBody {
  max_balance: number
  reset_period: string
  alert_threshold_pct: number
}

export function createCreditPool(
  id: string,
  body: CreatePoolBody
): Promise<CreditPool> {
  return api.post<CreditPool>(`${tenantPath(id)}/credit-pool`, body)
}

export async function listPoolDraws(id: string): Promise<PoolDraw[]> {
  const data = await api.get<{ draws?: PoolDraw[] }>(
    `${tenantPath(id)}/credit-pool/usage`,
    { params: { limit: POOL_USAGE_LIMIT } }
  )
  return data?.draws ?? []
}

/**
 * POST /:id/credit-pool/topup. The Idempotency-Key header is part of the
 * contract: without it the server mints a fresh key per request and a retried
 * request debits the wallet twice.
 */
export function topupCreditPool(
  id: string,
  body: { amount: number; reason?: string },
  idempotencyKey: string
): Promise<TopupResult> {
  return api.post<TopupResult>(`${tenantPath(id)}/credit-pool/topup`, body, {
    headers: { 'Idempotency-Key': idempotencyKey },
  })
}

export async function listInvites(id: string): Promise<TenantInviteList> {
  const data = await api.get<{
    invites?: TenantInviteList['invites']
    total?: number
  }>(`${tenantPath(id)}/invites`, {
    params: { page: 1, page_size: INVITE_LIST_PAGE_SIZE },
  })
  const invites = data?.invites ?? []
  return { invites, total: Math.max(data?.total ?? 0, invites.length) }
}

/** `ttlHours` of 0 sends an empty body: the server applies its default. */
export function issueInvite(
  id: string,
  ttlHours: number
): Promise<IssuedInvite> {
  return api.post<IssuedInvite>(
    `${tenantPath(id)}/invites`,
    ttlHours > 0 ? { ttl_hours: ttlHours } : {}
  )
}

export function revokeInvite(id: string, inviteId: number): Promise<unknown> {
  return api.delete(`${tenantPath(id)}/invites/${inviteId}`)
}
