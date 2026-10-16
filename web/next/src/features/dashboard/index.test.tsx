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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

import { DashboardPage } from './index'

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

const ME = {
  id: 1,
  username: 'u',
  role: 1,
  quota: 0,
  used_quota: 500000,
  remaining_quota: 1500000,
  request_count: 42,
  token_count: 2,
  tenant_role: 'admin',
}

function wire(opts: { me?: Record<string, unknown>; status?: Record<string, unknown> } = {}) {
  const me = opts.me ?? ME
  const status = {
    quota_per_unit: 500000,
    quota_display_type: 'USD',
    enable_data_export: true,
    ...opts.status,
  }
  apiGet.mockImplementation((url: string) =>
    Promise.resolve(url === '/api/status' ? status : '')
  )
  tenantGet.mockImplementation((path: string) => {
    if (path === '/user/me') return Promise.resolve(me)
    if (path === '/logs') return Promise.resolve({ logs: [], total: 0 })
    if (path === '/analytics/rankings') return Promise.resolve({ rows: [] })
    if (path === '/models/performance') return Promise.resolve({ items: [] })
    return Promise.resolve(null)
  })
  httpGet.mockImplementation(() =>
    Promise.resolve({
      status: 200,
      data: {
        success: true,
        data: [
          {
            model_name: 'model-a',
            created_at: Math.floor(Date.now() / 1000) - 3600,
            quota: 500000,
            count: 5,
            token_used: 10,
          },
        ],
      },
    })
  )
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <DashboardPage />
    </QueryClientProvider>
  )
}

const callsTo = (path: string) =>
  tenantGet.mock.calls.filter((c) => c[0] === path).length

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
})

describe('DashboardPage', () => {
  it('opens on the overview with the account KPIs in currency', async () => {
    wire()
    renderPage()
    await waitFor(() =>
      expect(screen.getByTestId('kpi-balance')).toHaveTextContent('$3.00')
    )
    expect(screen.getByTestId('kpi-spent')).toHaveTextContent('$1.00')
    expect(screen.getByTestId('dashboard-live')).toBeInTheDocument()
    expect(screen.getByTestId('dashboard-tab-overview')).toHaveAttribute('aria-selected', 'true')
    expect(screen.queryByTestId('dashboard-models')).toBeNull()
  })

  it('tenant admins see the usage breakdown, plain members do not', async () => {
    wire()
    const { unmount } = renderPage()
    expect(await screen.findByTestId('dashboard-rankings')).toBeInTheDocument()
    unmount()
    wire({ me: { ...ME, tenant_role: '' } })
    renderPage()
    await screen.findByTestId('dashboard-live')
    await waitFor(() =>
      expect(screen.getByTestId('kpi-balance')).toHaveTextContent('$3.00')
    )
    expect(screen.queryByTestId('dashboard-rankings')).toBeNull()
  })

  it('hides the trend panels when data export is off', async () => {
    wire({ status: { enable_data_export: false } })
    renderPage()
    await screen.findByTestId('dashboard-live')
    await waitFor(() => expect(screen.queryByTestId('dashboard-trend')).toBeNull())
    expect(httpGet).not.toHaveBeenCalled()
  })

  it('the models tab loads its charts on demand', async () => {
    wire()
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('dashboard-live')
    await user.click(screen.getByTestId('dashboard-tab-models'))
    // The first dynamic import of the chart code is slow under vitest.
    expect(
      await screen.findByTestId('dashboard-models', {}, { timeout: 30_000 })
    ).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.getByTestId('stat-count')).toHaveTextContent('5')
    )
    expect(screen.getByTestId('dashboard-consumption')).toBeInTheDocument()
    expect(screen.queryByTestId('kpi-balance')).toBeNull()
  }, 60_000)

  it('shows the service-status panel from the configured monitors and hides it when none', async () => {
    wire({ status: { uptime_kuma_enabled: true } })
    const status = {
      quota_per_unit: 500000,
      quota_display_type: 'USD',
      enable_data_export: true,
      uptime_kuma_enabled: true,
    }
    const serve = (groups: unknown) =>
      apiGet.mockImplementation((url: string) => {
        if (url === '/api/status') return Promise.resolve(status)
        if (url === '/api/uptime/status') return Promise.resolve(groups)
        return Promise.resolve('')
      })
    serve([
      {
        categoryName: 'Core',
        monitors: [{ name: 'Relay API', uptime: 1, status: 1 }],
      },
    ])
    const { unmount } = renderPage()
    expect(await screen.findByTestId('dashboard-uptime')).toHaveTextContent(
      'Relay API'
    )
    unmount()
    serve([])
    renderPage()
    await screen.findByTestId('dashboard-live')
    await waitFor(() =>
      expect(
        apiGet.mock.calls.filter((c) => c[0] === '/api/uptime/status').length
      ).toBeGreaterThan(1)
    )
    expect(screen.queryByTestId('dashboard-uptime')).toBeNull()
  })

  it('refresh re-reads the live window', async () => {
    wire()
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('dashboard-live')
    await waitFor(() => expect(callsTo('/logs')).toBe(1))
    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(callsTo('/logs')).toBe(2))
  })

  it('a failed account read is flagged, not shown as zero', async () => {
    wire()
    tenantGet.mockImplementation((path: string) => {
      if (path === '/user/me') return Promise.reject(new Error('down'))
      return Promise.resolve(path === '/logs' ? { logs: [], total: 0 } : null)
    })
    renderPage()
    await waitFor(() =>
      expect(screen.getByTestId('kpi-balance')).toHaveTextContent('Unable to load')
    )
    expect(screen.getByTestId('kpi-spent')).not.toHaveTextContent('$0')
  })
})
