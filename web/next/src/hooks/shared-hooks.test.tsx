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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useIsAdmin, useUserAccess } from './use-admin'
import { useCountdown } from './use-countdown'
import { useHiddenClickUnlock } from './use-hidden-click-unlock'
import { useQuota } from './use-quota'
import { useStatus } from './use-status'

const { apiGet, tenantGet } = vi.hoisted(() => ({
  apiGet: vi.fn(),
  tenantGet: vi.fn(),
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: { get: apiGet, post: vi.fn(), put: vi.fn(), delete: vi.fn() },
    tenantApi: { get: tenantGet, post: vi.fn(), put: vi.fn(), delete: vi.fn() },
  }
})

function wrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

describe('useStatus / useQuota', () => {
  beforeEach(() => {
    apiGet.mockReset()
    tenantGet.mockReset()
  })

  it('exposes the /api/status payload', async () => {
    apiGet.mockResolvedValue({ system_name: 'Console' })
    const { result } = renderHook(() => useStatus(), { wrapper: wrapper() })
    expect(result.current.status).toBeNull()
    await waitFor(() =>
      expect(result.current.status).toMatchObject({ system_name: 'Console' })
    )
  })

  it('never guesses an amount before the config has loaded', async () => {
    apiGet.mockReturnValue(new Promise(() => {}))
    const { result } = renderHook(() => useQuota(), { wrapper: wrapper() })
    expect(result.current.ready).toBe(false)
    expect(result.current.formatQuota(500000)).toBe('--')
    expect(result.current.toDisplayAmount(500000)).toBeNull()
    expect(result.current.fromDisplayAmount(1)).toBeNull()
  })

  it('formats and converts through the configured unit price', async () => {
    apiGet.mockResolvedValue({
      quota_per_unit: 500000,
      quota_display_type: 'CNY',
      usd_exchange_rate: 7,
    })
    const { result } = renderHook(() => useQuota(), { wrapper: wrapper() })
    await waitFor(() => expect(result.current.ready).toBe(true))
    expect(result.current.formatQuota(500000)).toBe('¥7.00')
    expect(result.current.toDisplayAmount(500000)).toBe(7)
    expect(result.current.fromDisplayAmount(7)).toBe(500000)
    expect(result.current.formatLogQuota(1)).toBe('¥0.000014')
  })

  it('shows raw units in tokens mode', async () => {
    apiGet.mockResolvedValue({
      quota_per_unit: 500000,
      quota_display_type: 'TOKENS',
    })
    const { result } = renderHook(() => useQuota(), { wrapper: wrapper() })
    await waitFor(() => expect(result.current.ready).toBe(true))
    expect(result.current.isTokens).toBe(true)
    expect(result.current.formatQuota(1234567)).toBe('1,234,567')
    expect(result.current.fromDisplayAmount(12.4)).toBe(12)
  })
})

describe('useIsAdmin / useUserAccess', () => {
  beforeEach(() => {
    tenantGet.mockReset()
  })

  it('is true for platform staff and false for a plain user', async () => {
    tenantGet.mockResolvedValue({ id: 1, role: 10, quota: 0, used_quota: 0 })
    const staff = renderHook(() => useIsAdmin(), { wrapper: wrapper() })
    await waitFor(() => expect(staff.result.current).toBe(true))

    tenantGet.mockResolvedValue({ id: 2, role: 1, quota: 0, used_quota: 0 })
    const plain = renderHook(
      () => ({ admin: useIsAdmin(), access: useUserAccess() }),
      { wrapper: wrapper() }
    )
    await waitFor(() => expect(tenantGet).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(plain.result.current.admin).toBe(false))
    expect(plain.result.current.access.isPlatformStaff).toBe(false)
  })

  it('treats tenant_role admin as tenant admin without platform staff', async () => {
    tenantGet.mockResolvedValue({
      id: 3,
      role: 1,
      tenant_role: 'admin',
      quota: 0,
      used_quota: 0,
    })
    const { result } = renderHook(() => useUserAccess(), {
      wrapper: wrapper(),
    })
    await waitFor(() => expect(result.current.isTenantAdmin).toBe(true))
    expect(result.current.isPlatformStaff).toBe(false)
  })
})

describe('useCountdown', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('counts down once per second and resets when done', () => {
    const { result } = renderHook(() => useCountdown({ initialSeconds: 3 }))
    act(() => result.current.start())
    expect(result.current.isActive).toBe(true)
    act(() => {
      vi.advanceTimersByTime(1000)
    })
    expect(result.current.secondsLeft).toBe(2)
    act(() => {
      vi.advanceTimersByTime(2000)
    })
    expect(result.current.isActive).toBe(false)
    expect(result.current.secondsLeft).toBe(3)
  })
})

describe('useHiddenClickUnlock', () => {
  it('unlocks on the required number of clicks and fires once', () => {
    const onUnlock = vi.fn()
    const { result } = renderHook(() =>
      useHiddenClickUnlock({ requiredClicks: 3, onUnlock })
    )
    act(() => result.current.handleClick())
    act(() => result.current.handleClick())
    expect(result.current.unlocked).toBe(false)
    act(() => result.current.handleClick())
    expect(result.current.unlocked).toBe(true)
    expect(onUnlock).toHaveBeenCalledTimes(1)
    act(() => result.current.reset())
    expect(result.current.unlocked).toBe(false)
  })
})
