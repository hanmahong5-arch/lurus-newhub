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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { CHART_PREFERENCES_STORAGE_KEY } from '../../lib/constants'

import { ModelsTab } from './models-tab'

const { httpGet, tenantGet, apiGet } = vi.hoisted(() => ({
  httpGet: vi.fn(),
  tenantGet: vi.fn(),
  apiGet: vi.fn(),
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    http: { get: httpGet },
    tenantApi: { get: tenantGet },
    api: { get: apiGet },
  }
})

const STATUS = { quota_per_unit: 500000, quota_display_type: 'USD' }
const nowSec = () => Math.floor(Date.now() / 1000)

function usageRows() {
  const t = nowSec() - 7200
  return [
    { model_name: 'model-a', created_at: t, quota: 500000, count: 3, token_used: 1200 },
    { model_name: 'model-b', created_at: t + 3600, quota: 250000, count: 4, token_used: 800 },
  ]
}

function ok(rows: unknown) {
  return Promise.resolve({ status: 200, data: { success: true, data: rows } })
}

function renderTab() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <ModelsTab />
    </QueryClientProvider>
  )
}

/** Span (seconds) of the n-th /api/data/self/ request. */
function spanOfCall(n: number): number {
  const params = httpGet.mock.calls[n][1].params as {
    start_timestamp: number
    end_timestamp: number
  }
  return params.end_timestamp - params.start_timestamp
}

// jsdom has no Web Animations API; the scroll-area viewport asks for it.
beforeAll(() => {
  if (!('getAnimations' in Element.prototype)) {
    Object.defineProperty(Element.prototype, 'getAnimations', {
      configurable: true,
      value: () => [],
    })
  }
})

beforeEach(() => {
  vi.clearAllMocks()
  window.localStorage.clear()
  apiGet.mockImplementation((url: string) =>
    url === '/api/status' ? Promise.resolve(STATUS) : Promise.resolve(null)
  )
  tenantGet.mockResolvedValue({ hours: 24, min_samples: 20, items: [] })
  httpGet.mockImplementation(() => ok(usageRows()))
})

describe('ModelsTab', () => {
  it('totals the rows and prices the quota through the unit price', async () => {
    renderTab()
    await waitFor(() =>
      expect(screen.getByTestId('stat-count')).toHaveTextContent('7')
    )
    await waitFor(() =>
      expect(screen.getByTestId('stat-quota')).toHaveTextContent('$1.50')
    )
    expect(screen.getByTestId('stat-tokens')).toHaveTextContent('2,000')
    expect(httpGet).toHaveBeenCalledWith(
      '/api/data/self/',
      expect.objectContaining({
        params: expect.objectContaining({
          start_timestamp: expect.any(Number),
          end_timestamp: expect.any(Number),
        }),
      })
    )
    // The default window is one rolling day.
    expect(spanOfCall(0)).toBe(86_400)
    const legend = await screen.findAllByTestId('series-legend')
    expect(legend[0]).toHaveTextContent('model-a')
    expect(legend[0]).toHaveTextContent('model-b')
  })

  it('a failed read is an alert and placeholders, never zeros', async () => {
    httpGet.mockImplementation(() =>
      Promise.reject(new ApiError('boom', { status: 500 }))
    )
    renderTab()
    expect((await screen.findAllByTestId('dashboard-load-failed')).length).toBeGreaterThan(0)
    expect(screen.getByTestId('stat-quota')).toHaveTextContent('--')
    expect(screen.getByTestId('stat-quota')).not.toHaveTextContent('$0')
    expect(screen.queryByTestId('dashboard-consumption-empty')).toBeNull()
  })

  it('a server-side success:false is also a failure', async () => {
    httpGet.mockImplementation(() =>
      Promise.resolve({
        status: 200,
        data: { success: false, message: 'span too long' },
      })
    )
    renderTab()
    expect((await screen.findAllByTestId('dashboard-load-failed')).length).toBeGreaterThan(0)
  })

  it('an empty answer says so in both charts', async () => {
    httpGet.mockImplementation(() => ok([]))
    renderTab()
    expect(await screen.findByTestId('dashboard-consumption-empty')).toBeInTheDocument()
    expect(screen.getByTestId('dashboard-model-charts-empty')).toBeInTheDocument()
  })

  it('the quick range filter re-queries with the chosen span', async () => {
    const user = userEvent.setup()
    renderTab()
    await screen.findByTestId('stat-count')
    await user.click(screen.getByRole('button', { name: 'Filter' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '7 Days' }))
    await user.click(within(dialog).getByRole('button', { name: 'Apply Filters' }))
    await waitFor(() => expect(httpGet).toHaveBeenCalledTimes(2))
    expect(spanOfCall(1)).toBe(7 * 86_400)
  })

  it('reset returns to the saved default range', async () => {
    window.localStorage.setItem(
      CHART_PREFERENCES_STORAGE_KEY,
      JSON.stringify({ defaultTimeRangeDays: 14, defaultTimeGranularity: 'day' })
    )
    const user = userEvent.setup()
    renderTab()
    await screen.findByTestId('stat-count')
    expect(spanOfCall(0)).toBe(14 * 86_400)
    await user.click(screen.getByRole('button', { name: 'Filter' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '7 Days' }))
    await user.click(within(dialog).getByRole('button', { name: 'Apply Filters' }))
    await waitFor(() => expect(httpGet).toHaveBeenCalledTimes(2))
    await user.click(screen.getByRole('button', { name: 'Filter' }))
    const again = await screen.findByRole('dialog')
    await user.click(within(again).getByRole('button', { name: 'Reset' }))
    await waitFor(() => expect(httpGet).toHaveBeenCalledTimes(3))
    expect(spanOfCall(2)).toBe(14 * 86_400)
  })

  it('opens on the saved chart types and switches between them', async () => {
    window.localStorage.setItem(
      CHART_PREFERENCES_STORAGE_KEY,
      JSON.stringify({
        consumptionDistributionChart: 'area',
        modelAnalyticsChart: 'top',
      })
    )
    const user = userEvent.setup()
    renderTab()
    await screen.findByTestId('stat-count')
    expect(screen.getByTestId('consumption-type-area')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('model-chart-tab-top')).toHaveAttribute('aria-checked', 'true')
    await user.click(screen.getByTestId('model-chart-tab-proportion'))
    const rows = await screen.findByTestId('share-rows')
    // 4 of 7 calls belong to model-b: 57.1%.
    expect(rows).toHaveTextContent('model-b')
    expect(rows).toHaveTextContent('57.1%')
  })

  it('saving preferences persists them and applies the new default range', async () => {
    const user = userEvent.setup()
    renderTab()
    await screen.findByTestId('stat-count')
    await user.click(screen.getByRole('button', { name: 'Preferences' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('combobox', { name: 'Default range' }))
    await user.click(await screen.findByRole('option', { name: '14 Days' }))
    await user.click(within(dialog).getByRole('button', { name: 'Save Preferences' }))
    await waitFor(() => expect(httpGet).toHaveBeenCalledTimes(2))
    expect(spanOfCall(1)).toBe(14 * 86_400)
    expect(
      JSON.parse(window.localStorage.getItem(CHART_PREFERENCES_STORAGE_KEY) ?? '{}')
    ).toMatchObject({ defaultTimeRangeDays: 14 })
  })
})
