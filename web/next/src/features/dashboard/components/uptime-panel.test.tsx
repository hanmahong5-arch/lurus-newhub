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
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { UptimePanel } from './uptime-panel'

const { apiGet } = vi.hoisted(() => ({ apiGet: vi.fn() }))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return { ...actual, api: { get: apiGet } }
})

function renderPanel() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <UptimePanel />
    </QueryClientProvider>
  )
}

const UPTIME_URL = '/api/uptime/status'

function route(over: {
  status?: Record<string, unknown>
  uptime?: unknown
}) {
  apiGet.mockImplementation((url: string) => {
    if (url === '/api/status') return Promise.resolve(over.status ?? {})
    if (url === UPTIME_URL) {
      return over.uptime instanceof Error
        ? Promise.reject(over.uptime)
        : Promise.resolve(over.uptime ?? [])
    }
    return Promise.resolve(null)
  })
}

const uptimeCalls = () => apiGet.mock.calls.filter((c) => c[0] === UPTIME_URL)

const GROUPS = [
  {
    categoryName: 'Core services',
    monitors: [
      { name: 'Relay API', uptime: 0.9995, status: 1, group: 'Edge' },
      { name: 'Billing', uptime: 0.5, status: 0, group: 'Edge' },
      { name: 'Search', uptime: 0.97, status: 2 },
      { name: 'Backups', uptime: 1, status: 3 },
    ],
  },
  { categoryName: 'Unreachable page', monitors: [] },
]

beforeEach(() => {
  vi.clearAllMocks()
})

describe('UptimePanel', () => {
  it('renders each monitor with its state and 24h availability', async () => {
    route({ status: { uptime_kuma_enabled: true }, uptime: GROUPS })
    renderPanel()
    const panel = await screen.findByTestId('dashboard-uptime')
    expect(within(panel).getByText('Service status')).toBeInTheDocument()
    expect(panel).toHaveTextContent('1 of 4 monitors up')

    const rows = within(panel).getAllByTestId('uptime-monitor')
    expect(rows.map((r) => r.getAttribute('data-state'))).toEqual([
      'up',
      'down',
      'degraded',
      'maintenance',
    ])
    expect(rows[0]).toHaveTextContent('Relay API')
    expect(rows[0]).toHaveTextContent('Up')
    expect(rows[0]).toHaveTextContent('99.95%')
    expect(rows[1]).toHaveTextContent('Down')
    expect(rows[1]).toHaveTextContent('50.00%')
    expect(rows[2]).toHaveTextContent('Degraded')
    expect(rows[3]).toHaveTextContent('Maintenance')
    // Kuma sub-group and category headings are shown; a group with no
    // monitors is not drawn as an empty block.
    expect(panel).toHaveTextContent('Core services')
    expect(panel).toHaveTextContent('Edge')
    expect(within(panel).getAllByTestId('uptime-group')).toHaveLength(1)
    expect(panel).not.toHaveTextContent('Unreachable page')
  })

  it('maps an unrecognised heartbeat code to Unknown, not Up', async () => {
    route({
      status: { uptime_kuma_enabled: true },
      uptime: [
        { categoryName: 'x', monitors: [{ name: 'M', uptime: 1, status: 9 }] },
      ],
    })
    renderPanel()
    const row = await screen.findByTestId('uptime-monitor')
    expect(row).toHaveAttribute('data-state', 'unknown')
    expect(row).toHaveTextContent('Unknown')
    expect(screen.getByTestId('dashboard-uptime')).toHaveTextContent(
      '0 of 1 monitors up'
    )
  })

  it('renders nothing at all when no monitor is configured', async () => {
    route({ status: { uptime_kuma_enabled: true }, uptime: [] })
    const { container } = renderPanel()
    await waitFor(() => expect(uptimeCalls()).toHaveLength(1))
    await new Promise((r) => setTimeout(r, 20))
    expect(container).toBeEmptyDOMElement()
  })

  it('renders nothing when every configured group came back without monitors', async () => {
    route({
      status: { uptime_kuma_enabled: true },
      uptime: [{ categoryName: 'A', monitors: [] }],
    })
    const { container } = renderPanel()
    await waitFor(() => expect(uptimeCalls()).toHaveLength(1))
    await new Promise((r) => setTimeout(r, 20))
    expect(container).toBeEmptyDOMElement()
  })

  it('does not even ask when the operator switched the panel off', async () => {
    route({ status: { uptime_kuma_enabled: false }, uptime: GROUPS })
    const { container } = renderPanel()
    await waitFor(() =>
      expect(apiGet.mock.calls.some((c) => c[0] === '/api/status')).toBe(true)
    )
    await new Promise((r) => setTimeout(r, 20))
    expect(container).toBeEmptyDOMElement()
    expect(uptimeCalls()).toHaveLength(0)
  })

  it('shows a failed read as an error with retry, never as all-clear', async () => {
    route({
      status: { uptime_kuma_enabled: true },
      uptime: new ApiError('status page down', { status: 502 }),
    })
    renderPanel()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not load service status.')
    expect(screen.queryByTestId('uptime-monitor')).not.toBeInTheDocument()
    expect(screen.queryByText(/monitors up/)).not.toBeInTheDocument()

    route({ status: { uptime_kuma_enabled: true }, uptime: GROUPS })
    await userEvent.setup().click(within(alert).getByRole('button'))
    expect(await screen.findAllByTestId('uptime-monitor')).toHaveLength(4)
  })
})
