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
import { describe, expect, it } from 'vitest'

import type { TenantInvite } from '../types'
import {
  inviteDisplayStatus,
  inviteLink,
  inviteStatusName,
  isRevocable,
  parseTtlHours,
} from './invite'

const invite = (over: Partial<TenantInvite> = {}): TenantInvite => ({
  id: 1,
  code_prefix: 'abcd1234',
  status: 1,
  expired_time: 0,
  consumed_by_account_id: null,
  created_at: '2026-09-19T00:00:00Z',
  ...over,
})

describe('invite helpers', () => {
  it('inviteLink builds the /login?invite= delivery URL', () => {
    expect(inviteLink('https://hub.test', 'abc123')).toBe(
      'https://hub.test/login?invite=abc123'
    )
  })

  it('inviteLink percent-encodes special characters in the code', () => {
    expect(inviteLink('https://hub.test', 'a b/c')).toBe(
      `https://hub.test/login?invite=${encodeURIComponent('a b/c')}`
    )
  })

  it('inviteStatusName maps the three known statuses plus unknown', () => {
    expect(inviteStatusName(1)).toBe('pending')
    expect(inviteStatusName(2)).toBe('consumed')
    expect(inviteStatusName(3)).toBe('revoked')
    expect(inviteStatusName(99)).toBe('unknown')
  })

  it('a pending code past its expiry reads as expired; a spent one does not', () => {
    expect(inviteDisplayStatus(invite({ expired: true }))).toBe('expired')
    expect(inviteDisplayStatus(invite())).toBe('pending')
    expect(inviteDisplayStatus(invite({ status: 2, expired: true }))).toBe(
      'consumed'
    )
  })

  it('isRevocable is true only for pending invites', () => {
    expect(isRevocable(invite())).toBe(true)
    expect(isRevocable(invite({ status: 2 }))).toBe(false)
    expect(isRevocable(invite({ status: 3 }))).toBe(false)
    expect(isRevocable(null)).toBe(false)
  })

  it('parseTtlHours: blank and 0 mean the server default', () => {
    expect(parseTtlHours('')).toEqual({ ok: true, hours: 0 })
    expect(parseTtlHours('  ')).toEqual({ ok: true, hours: 0 })
    expect(parseTtlHours('0')).toEqual({ ok: true, hours: 0 })
    expect(parseTtlHours('24')).toEqual({ ok: true, hours: 24 })
  })

  it('parseTtlHours enforces the server ceiling of 30 days', () => {
    expect(parseTtlHours('720')).toEqual({ ok: true, hours: 720 })
    expect(parseTtlHours('721')).toEqual({ ok: false, error: 'too-long' })
    expect(parseTtlHours('-1')).toEqual({ ok: false, error: 'not-integer' })
    expect(parseTtlHours('1.5')).toEqual({ ok: false, error: 'not-integer' })
    expect(parseTtlHours('abc')).toEqual({ ok: false, error: 'not-integer' })
  })
})
