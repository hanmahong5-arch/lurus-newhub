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
import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  DRAFT_SCHEMA_VERSION,
  draftKey,
  useFormDraft,
} from './use-form-draft'

const INITIAL = { amount: '', reason: '' }

beforeEach(() => {
  vi.useFakeTimers()
  window.localStorage.clear()
})

afterEach(() => {
  vi.useRealTimers()
})

function stored(key: string) {
  const raw = window.localStorage.getItem(key)
  return raw ? (JSON.parse(raw) as { v: number; d: unknown; t: number }) : null
}

describe('useFormDraft', () => {
  const key = draftKey(7, 'topup:t-1')

  it('starts from the defaults and is not marked restored', () => {
    const { result } = renderHook(() =>
      useFormDraft('topup:t-1', INITIAL, { userId: 7 })
    )
    expect(result.current.draft).toEqual(INITIAL)
    expect(result.current.restored).toBe(false)
  })

  it('persists after the debounce window, namespaced by person and key', () => {
    const { result } = renderHook(() =>
      useFormDraft('topup:t-1', INITIAL, { userId: 7 })
    )
    act(() => result.current.setDraft({ amount: '5', reason: 'promo' }))
    expect(stored(key)).toBeNull()
    act(() => {
      vi.advanceTimersByTime(400)
    })
    expect(stored(key)?.d).toEqual({ amount: '5', reason: 'promo' })
    // Another person's slot is untouched.
    expect(stored(draftKey(8, 'topup:t-1'))).toBeNull()
  })

  it('restores a saved draft and says so', () => {
    window.localStorage.setItem(
      key,
      JSON.stringify({
        v: DRAFT_SCHEMA_VERSION,
        d: { amount: '9', reason: '' },
        t: Date.now(),
      })
    )
    const { result } = renderHook(() =>
      useFormDraft('topup:t-1', INITIAL, { userId: 7 })
    )
    expect(result.current.draft).toEqual({ amount: '9', reason: '' })
    expect(result.current.restored).toBe(true)
  })

  it('discards a stale or version-mismatched draft', () => {
    window.localStorage.setItem(
      key,
      JSON.stringify({
        v: DRAFT_SCHEMA_VERSION,
        d: { amount: '9', reason: '' },
        t: Date.now() - 8 * 24 * 60 * 60 * 1000,
      })
    )
    const stale = renderHook(() =>
      useFormDraft('topup:t-1', INITIAL, { userId: 7 })
    )
    expect(stale.result.current.draft).toEqual(INITIAL)
    expect(stale.result.current.restored).toBe(false)
    expect(stored(key)).toBeNull()

    window.localStorage.setItem(
      key,
      JSON.stringify({ v: 999, d: { amount: '9', reason: '' }, t: Date.now() })
    )
    const other = renderHook(() =>
      useFormDraft('topup:t-1', INITIAL, { userId: 7 })
    )
    expect(other.result.current.restored).toBe(false)
  })

  it('clear removes the saved draft and resets the form', () => {
    window.localStorage.setItem(
      key,
      JSON.stringify({
        v: DRAFT_SCHEMA_VERSION,
        d: { amount: '9', reason: '' },
        t: Date.now(),
      })
    )
    const { result } = renderHook(() =>
      useFormDraft('topup:t-1', INITIAL, { userId: 7 })
    )
    act(() => result.current.clear())
    expect(result.current.draft).toEqual(INITIAL)
    expect(result.current.restored).toBe(false)
    expect(stored(key)).toBeNull()
  })

  it('flushes a pending edit when the form unmounts', () => {
    const { result, unmount } = renderHook(() =>
      useFormDraft('topup:t-1', INITIAL, { userId: 7 })
    )
    act(() => result.current.setDraft({ amount: '3', reason: '' }))
    unmount()
    expect(stored(key)?.d).toEqual({ amount: '3', reason: '' })
  })

  it('supports functional updates', () => {
    const { result } = renderHook(() =>
      useFormDraft('topup:t-1', INITIAL, { userId: 7 })
    )
    act(() => result.current.setDraft((d) => ({ ...d, amount: '1' })))
    act(() => result.current.setDraft((d) => ({ ...d, reason: 'x' })))
    expect(result.current.draft).toEqual({ amount: '1', reason: 'x' })
  })
})
