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
/** One resource of the static catalogue and the actions a grant may name. */
export interface CatalogResource {
  resource: string
  actions: string[]
}

/** A fixed role and the integer role it needs (authz.Roles). */
export interface CatalogRole {
  name: string
  min_role: number
}

/** GET /api/v2/admin/authz/catalog. */
export interface AuthzCatalog {
  resources: CatalogResource[]
  roles: CatalogRole[]
}

/** One row of GET /api/v2/admin/authz/grants (entity + derived `expired`). */
export interface Grant {
  id: number
  user_id: number
  resource: string
  action: string
  created_at: number
  /** Unix seconds; null / absent means the grant never expires. */
  expires_at?: number | null
  /** Server-derived (expires_at <= now); never recompute it client-side. */
  expired?: boolean
  /** Unix seconds; set once the grant has been withdrawn. */
  revoked_at?: number | null
}

export type GrantStatus = 'active' | 'expired' | 'revoked'

export interface CreateGrantInput {
  userId: number
  resource: string
  action: string
  /** undefined omits `ttl_seconds` (permanent grant); 0 is still sent. */
  ttlSeconds?: number
}
