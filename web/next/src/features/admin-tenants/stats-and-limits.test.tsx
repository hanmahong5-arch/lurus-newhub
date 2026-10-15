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
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { TenantLimitsDialog } from './components/tenant-limits-dialog'
import { TenantStatsSheet } from './components/tenant-stats-sheet'
import {
  renderWithClient,
  serveGet,
  tenantFixture,
  type Routes,
} from './test-helpers'
import type { TenantStats } from './types'

const { apiGet, apiPut, tenantGet, toast, setDialog, refresh } = vi.hoisted(
  () => ({
    apiGet: vi.fn(),
    apiPut: vi.fn(),
    tenantGet: vi.fn(),
    toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
    setDialog: vi.fn(),
    refresh: vi.fn(),
  })
)

vi.mock('sonner', () => ({ toast }))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: { get: apiGet, post: vi.fn(), put: apiPut, delete: vi.fn() },
    tenantApi: {
      get: tenantGet,
      post: vi.fn(),
      put: vi.fn(),
      delete: vi.fn(),
    },
  }
})

vi.mock('./components/tenants-provider', () => ({
  useTenants: () => ({ dialog: null, setDialog, refresh }),
}))

const STATS_URL = '/api/v2/admin/tenants/t-1/stats'
const TENANT = tenantFixture({
  id: 't-1',
  name: 'Acme',
  rate_limit_rpm: 60,
  rate_limit_tpm: 0,
})
const STATS: TenantStats = {
  tenant_id: 't-1',
  user_count: 3,
  max_users: 100,
  max_quota: 0,
  token_count: 12,
  channel_count: 2,
  total_quota_used: 2_500_000,
  total_quota_remaining: 500_000,
  active_subscriptions: 0,
  total_topup_amount: 0,
  total_redemptions: 1,
  log_count: 4200,
  last_activity_at: 0,
}

beforeEach(() => {
  vi.clearAllMocks()
  apiPut.mockResolvedValue({})
})

describe('TenantStatsSheet', () => {
  function renderStats(routes: Routes) {
    const live = serveGet(apiGet, tenantGet, routes)
    return {
      live,
      ...renderWithClient(<TenantStatsSheet tenant={TENANT} />),
    }
  }

  it('fetches /stats for the tenant and labels every row in words', async () => {
    renderStats({ [STATS_URL]: STATS })
    expect(await screen.findByText('Seats used')).toBeInTheDocument()
    expect(apiGet).toHaveBeenCalledWith(STATS_URL)
    expect(screen.getByRole('dialog')).toHaveAccessibleName('Acme · stats')
    // Never the wire field names.
    expect(screen.queryByText('user_count')).not.toBeInTheDocument()
    expect(screen.queryByText('tenant_id')).not.toBeInTheDocument()
    expect(screen.getByText('3 / 100')).toBeInTheDocument()
    expect(screen.getByText('Unlimited')).toBeInTheDocument()
    expect(screen.getByText('Never')).toBeInTheDocument()
  })

  it('money comes from the status config, not a guessed unit price', async () => {
    renderStats({ [STATS_URL]: STATS })
    // 2,500,000 and 500,000 units at 500,000 per USD.
    expect(await screen.findByText('$5.00')).toBeInTheDocument()
    expect(screen.getByText('$1.00')).toBeInTheDocument()
  })

  it('does not print the stats the server never fills in', async () => {
    renderStats({ [STATS_URL]: STATS })
    await screen.findByText('Seats used')
    expect(screen.queryByText(/subscription/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/top-?up/i)).not.toBeInTheDocument()
  })

  it('a failed read shows the error panel and retry refetches', async () => {
    const { live } = renderStats({
      [STATS_URL]: new ApiError('boom', { status: 500 }),
    })
    expect(await screen.findByText('Could not load stats')).toBeInTheDocument()
    expect(screen.queryByText('Seats used')).not.toBeInTheDocument()

    live[STATS_URL] = STATS
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('Seats used')).toBeInTheDocument()
    expect(
      apiGet.mock.calls.filter(([url]) => url === STATS_URL).length
    ).toBe(2)
  })

  it('Escape closes the sheet', async () => {
    renderStats({ [STATS_URL]: new Promise(() => undefined) })
    await screen.findByRole('dialog')
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(setDialog).toHaveBeenCalledWith(null))
  })
})

describe('TenantLimitsDialog', () => {
  function renderLimits() {
    return renderWithClient(<TenantLimitsDialog tenant={TENANT} />)
  }

  it('is a modal dialog named after the tenant, prefilled from the row', () => {
    renderLimits()
    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveAccessibleName('Acme · rate limits')
    const [rpm, tpm] = screen.getAllByRole('spinbutton')
    expect(rpm).toHaveValue(60)
    // 0 means unlimited and is shown as an empty field, not a literal 0.
    expect(tpm).toHaveValue(null)
  })

  it('Escape closes the dialog', async () => {
    renderLimits()
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(setDialog).toHaveBeenCalledWith(null))
    expect(setDialog).toHaveBeenCalledTimes(1)
  })

  it('submit PUTs both caps as integers, 0 for a blank, then refreshes and closes', async () => {
    renderLimits()
    const [rpm, tpm] = screen.getAllByRole('spinbutton')
    await userEvent.clear(rpm)
    await userEvent.type(tpm, '5000')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(apiPut).toHaveBeenCalledTimes(1))
    expect(apiPut).toHaveBeenCalledWith('/api/v2/admin/tenants/t-1', {
      rate_limit_rpm: 0,
      rate_limit_tpm: 5000,
    })
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Rate limits saved')
    )
    expect(refresh).toHaveBeenCalled()
    expect(setDialog).toHaveBeenCalledWith(null)
  })

  it('refuses Escape while the PUT is in flight', async () => {
    let settle: (value: unknown) => void = () => undefined
    apiPut.mockImplementationOnce(
      () => new Promise((resolve) => (settle = resolve))
    )
    renderLimits()
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(apiPut).toHaveBeenCalled())
    await userEvent.keyboard('{Escape}')
    expect(setDialog).not.toHaveBeenCalled()
    settle({})
    await waitFor(() => expect(setDialog).toHaveBeenCalledWith(null))
  })

  it('a refused save shows the reason and keeps the dialog open', async () => {
    apiPut.mockRejectedValue(
      new ApiError('rate limit exceeds the allowed range', { status: 400 })
    )
    renderLimits()
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        'rate limit exceeds the allowed range'
      )
    )
    expect(setDialog).not.toHaveBeenCalled()
    expect(refresh).not.toHaveBeenCalled()
  })
})
