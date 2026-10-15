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
import { INVITE_MAX_TTL_HOURS, INVITE_STATUS } from '../constants'
import type { TenantInvite } from '../types'

/**
 * The delivery URL an operator sends the invitee: the sign-in page with the
 * code as `invite`, which the sign-in redirect carries through the identity
 * round trip.
 */
export function inviteLink(origin: string, code: string): string {
  return `${origin}/login?invite=${encodeURIComponent(code)}`
}

export type InviteStatusName = 'pending' | 'consumed' | 'revoked' | 'unknown'

/** Status 1 pending (usable) / 2 consumed / 3 revoked. */
export function inviteStatusName(status: number): InviteStatusName {
  if (status === INVITE_STATUS.PENDING) return 'pending'
  if (status === INVITE_STATUS.CONSUMED) return 'consumed'
  if (status === INVITE_STATUS.REVOKED) return 'revoked'
  return 'unknown'
}

/** A pending code past its expiry can no longer be used. */
export function inviteDisplayStatus(
  invite: TenantInvite
): InviteStatusName | 'expired' {
  if (invite.status === INVITE_STATUS.PENDING && invite.expired) {
    return 'expired'
  }
  return inviteStatusName(invite.status)
}

/** Only a pending code can still be revoked. */
export function isRevocable(invite: TenantInvite | null | undefined): boolean {
  return !!invite && invite.status === INVITE_STATUS.PENDING
}

export type TtlParse =
  | { ok: true; hours: number }
  | { ok: false; error: 'not-integer' | 'too-long' }

/**
 * The validity field. Blank or 0 means "the server's default" (72 hours);
 * the server refuses anything beyond 30 days, so the form does too.
 */
export function parseTtlHours(raw: string): TtlParse {
  const text = raw.trim()
  if (text === '') return { ok: true, hours: 0 }
  if (!/^\d+$/.test(text)) return { ok: false, error: 'not-integer' }
  const hours = Number.parseInt(text, 10)
  if (hours > INVITE_MAX_TTL_HOURS) return { ok: false, error: 'too-long' }
  return { ok: true, hours }
}
