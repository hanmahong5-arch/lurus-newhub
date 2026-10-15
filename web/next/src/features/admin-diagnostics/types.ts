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
/** GET /api/v2/admin/routing/affinity `data` (app.AffinityStats). */
export interface AffinityStats {
  enabled: boolean
  ttl_seconds: number
  hit: number
  miss: number
  stale: number
  /** "redis" or "memory": which store is live on the answering replica. */
  backend: string
  /** In-process fallback map size; reads 0 while the backend is redis. */
  mem_entries: number
}

/** The affinity read: the stats plus the response's top-level `scope`. */
export interface AffinitySnapshot {
  stats: AffinityStats
  /** "replica": the counters are per-process, not cluster-wide. */
  scope: string | null
}

/** GET /api/v2/admin/security/totp-stats `data`. */
export interface TotpStats {
  enrolled: number
  pending: number
  total_users: number
  adoption_pct: number
  backup_codes_exhausted: number
  no_codes_issued: number
}

/** DELETE /api/v2/admin/routing/affinity?all=true `data`. */
export interface PurgeAllResult {
  purged: number
  /** false: the SCAN loop hit its round cap, bindings may remain. */
  complete: boolean
}
