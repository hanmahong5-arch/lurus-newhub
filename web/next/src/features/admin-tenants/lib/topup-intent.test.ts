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

import { ApiError } from '@/lib/api'

import {
  failureIsDefinitive,
  intentFor,
  newTopupKey,
  type TopupIntent,
} from './topup-intent'

describe('top-up idempotency intent', () => {
  it('mints a key per call', () => {
    expect(newTopupKey()).not.toBe(newTopupKey())
    expect(newTopupKey().length).toBeLessThan(128)
  })

  it('a retry of the same amount and reason keeps its key', () => {
    let n = 0
    const make = () => `k${++n}`
    const first = intentFor(null, 500, 'promo', make)
    const again = intentFor(first, 500, 'promo', make)
    expect(again).toBe(first)
    expect(n).toBe(1)
  })

  it('a different amount or reason is a new intent with a new key', () => {
    let n = 0
    const make = () => `k${++n}`
    const first: TopupIntent = intentFor(null, 500, 'promo', make)
    expect(intentFor(first, 600, 'promo', make).key).toBe('k2')
    expect(intentFor(first, 500, 'other', make).key).toBe('k3')
  })

  it('a refusal from the server spends the key; an unanswered request does not', () => {
    expect(failureIsDefinitive(new ApiError('no', { status: 402 }))).toBe(true)
    expect(failureIsDefinitive(new ApiError('no', { status: 409 }))).toBe(true)
    expect(failureIsDefinitive(new ApiError('no', { status: 400 }))).toBe(true)
    // Outcome unknown: the debit may have gone through. Keep the key.
    expect(failureIsDefinitive(new ApiError('boom', { status: 500 }))).toBe(
      false
    )
    expect(failureIsDefinitive(new ApiError('boom', { status: 503 }))).toBe(
      false
    )
    expect(failureIsDefinitive(new ApiError('timeout', { status: 408 }))).toBe(
      false
    )
    expect(failureIsDefinitive(new ApiError('offline'))).toBe(false)
    expect(failureIsDefinitive(new Error('network'))).toBe(false)
  })
})
