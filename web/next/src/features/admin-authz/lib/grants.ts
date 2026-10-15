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
import type { CreateGrantInput, Grant, GrantStatus } from '../types'

/** Longest grant the server accepts (90 days), in seconds. */
export const MAX_TTL_SECONDS = 7_776_000

/**
 * Withdrawn beats timed out beats live. `expired` is the server's reading of
 * expires_at against ITS clock, so it is taken as given.
 */
export function grantStatus(grant: Grant): GrantStatus {
  if (grant.revoked_at) return 'revoked'
  if (grant.expired) return 'expired'
  return 'active'
}

export interface GrantFormValues {
  userId: string
  resource: string
  action: string
  ttlSeconds: string
}

export const EMPTY_FORM: GrantFormValues = {
  userId: '',
  resource: '',
  action: '',
  ttlSeconds: '',
}

/**
 * Turn the raw form into a request, or null when it is not submittable.
 *
 * An empty (trimmed) ttl field is the ONLY thing that means "permanent" and
 * omits the key. A parsed 0 is still sent, so the server's own 400
 * GRANT_INVALID is what the operator sees rather than a never-expiring grant.
 */
export function parseGrantForm(form: GrantFormValues): CreateGrantInput | null {
  const userId = Number.parseInt(form.userId, 10)
  if (!userId || userId <= 0 || !form.resource || !form.action) return null
  const raw = form.ttlSeconds.trim()
  const ttl = raw === '' ? undefined : Number.parseInt(raw, 10)
  return {
    userId,
    resource: form.resource,
    action: form.action,
    ...(ttl === undefined || Number.isNaN(ttl) ? {} : { ttlSeconds: ttl }),
  }
}
