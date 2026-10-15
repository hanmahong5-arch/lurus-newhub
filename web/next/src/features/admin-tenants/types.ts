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
/** Wire shapes of /api/v2/admin/tenants (internal/domain/entity/tenant*.go). */

export interface Tenant {
  id: string
  /** Upstream identity-provider organization id (wire key kept by the server). */
  zitadel_org_id: string
  slug: string
  name: string
  /** 1 enabled, 2 disabled, 3 suspended. */
  status: number
  plan_type: string
  max_users: number
  /** Monthly quota cap in quota units; <= 0 is "no cap". */
  max_quota: number
  /** Aggregate per-minute caps over all of the tenant's keys; 0 = unlimited. */
  rate_limit_rpm: number
  rate_limit_tpm: number
  wallet_authoritative?: boolean
  created_at?: string
  updated_at?: string
}

/** GET /api/v2/admin/tenants payload (the older shape used `items`). */
export interface TenantListPayload {
  tenants?: Tenant[]
  items?: Tenant[]
  total?: number
  page?: number
  page_size?: number
}

export interface TenantList {
  tenants: Tenant[]
  /** The server's count; larger than `tenants.length` when the fetch was capped. */
  total: number
}

export interface TenantStats {
  tenant_id: string
  user_count: number
  max_users: number
  max_quota: number
  token_count: number
  channel_count: number
  total_quota_used: number
  total_quota_remaining: number
  /** Declared by the server but never filled in: always 0. */
  active_subscriptions?: number
  /** Declared by the server but never filled in: always 0. */
  total_topup_amount?: number
  total_redemptions: number
  log_count: number
  /** Unix seconds; 0 = no activity yet. */
  last_activity_at: number
}

export interface CreditPool {
  id: number
  tenant_id: string
  current_balance: number
  /** -1 = unlimited. */
  max_balance: number
  reset_period: string
  next_reset_at?: string | null
  alert_threshold_pct?: number
}

export interface PoolDraw {
  id: number
  /** 1 = debit (money out of the pool), -1 = credit. */
  direction: number
  amount: number
  reason: string
  created_at: string
}

export interface TopupResult {
  tenant_id: string
  new_balance: number
  max_balance: number
  /** The server recognised the Idempotency-Key and did not credit twice. */
  replayed?: boolean
  /** A previously stranded debit was settled by this request. */
  reconciled?: boolean
}

export interface TenantInvite {
  id: number
  code_prefix: string
  /** 1 pending, 2 consumed, 3 revoked. */
  status: number
  /** Unix seconds; 0 = never expires. */
  expired_time: number
  consumed_by_account_id: number | null
  created_at: string
  member_role?: string
  expired?: boolean
}

export interface TenantInviteList {
  invites: TenantInvite[]
  total: number
}

/** POST .../invites: the one response that carries the full code. */
export interface IssuedInvite {
  id: number
  code: string
  status: number
}

export type TenantAction = 'enable' | 'disable' | 'suspend'

export type TenantDialog =
  | null
  | { kind: 'create' }
  | { kind: 'edit'; tenant: Tenant }
  | { kind: 'stats'; tenant: Tenant }
  | { kind: 'pool'; tenant: Tenant }
  | { kind: 'invites'; tenant: Tenant }
  | { kind: 'limits'; tenant: Tenant }
  | { kind: 'status'; tenant: Tenant; action: TenantAction }
