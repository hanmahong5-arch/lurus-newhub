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

import { PerformanceOverview } from './performance-overview'

const { tenantGet } = vi.hoisted(() => ({ tenantGet: vi.fn() }))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return { ...actual, tenantApi: { get: tenantGet } }
})

function renderPanel() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <PerformanceOverview />
    </QueryClientProvider>
  )
}

const member = (name: string, over: Record<string, unknown> = {}) => ({
  model_name: name,
  p50_latency_ms: 420,
  p95_latency_ms: 1800,
  error_rate: 0.004,
  enough_samples: true,
  ...over,
})

beforeEach(() => {
  vi.clearAllMocks()
})

describe('PerformanceOverview', () => {
  it('lists latency and error rate per model for a member (no volume)', async () => {
    tenantGet.mockResolvedValue({
      hours: 24,
      min_samples: 20,
      items: [
        member('model-b', { error_rate: 0.08 }),
        member('model-a', { enough_samples: false, error_rate: 0.5 }),
      ],
    })
    renderPanel()
    const rows = await screen.findAllByTestId('performance-row')
    // No volume: alphabetical.
    expect(rows[0]).toHaveTextContent('model-a')
    expect(rows[1]).toHaveTextContent('model-b')
    expect(rows[1]).toHaveTextContent('420ms')
    expect(rows[1]).toHaveTextContent('1.80s')
    expect(rows[1]).toHaveTextContent('8.0%')
    // A thin sample is labelled and not judged.
    expect(rows[0]).toHaveTextContent('Low sample')
    expect(screen.queryByTestId('performance-summary')).toBeNull()
    expect(tenantGet).toHaveBeenCalledWith('/models/performance', {
      params: { hours: 24 },
    })
  })

  it('tenant admins also get volume and a pooled success rate', async () => {
    tenantGet.mockResolvedValue({
      hours: 24,
      min_samples: 20,
      items: [
        member('model-a', { requests: 900, errors: 9 }),
        member('model-b', { requests: 100, errors: 1 }),
      ],
    })
    renderPanel()
    const summary = await screen.findByTestId('performance-summary')
    // (1000 - 10) / 1000
    expect(summary).toHaveTextContent('99.00%')
    expect(summary).toHaveTextContent('1,000')
    const rows = screen.getAllByTestId('performance-row')
    expect(rows[0]).toHaveTextContent('model-a')
    expect(rows[0]).toHaveTextContent('900')
  })

  it('switching the window asks the server for that many hours', async () => {
    tenantGet.mockResolvedValue({ hours: 24, items: [member('model-a')] })
    const user = userEvent.setup()
    renderPanel()
    await screen.findByTestId('performance-rows')
    await user.click(screen.getByTestId('performance-window-168'))
    await waitFor(() =>
      expect(tenantGet).toHaveBeenLastCalledWith('/models/performance', {
        params: { hours: 168 },
      })
    )
  })

  it('collapses past eight rows and expands on demand', async () => {
    tenantGet.mockResolvedValue({
      hours: 24,
      items: Array.from({ length: 11 }, (_, i) => member(`model-${String(i).padStart(2, '0')}`)),
    })
    const user = userEvent.setup()
    renderPanel()
    await screen.findByTestId('performance-rows')
    expect(screen.getAllByTestId('performance-row')).toHaveLength(8)
    await user.click(screen.getByRole('button', { name: /Show all/ }))
    expect(screen.getAllByTestId('performance-row')).toHaveLength(11)
  })

  it('no rows is stated, not drawn as a healthy table', async () => {
    tenantGet.mockResolvedValue({ hours: 24, items: [] })
    renderPanel()
    expect(await screen.findByTestId('dashboard-performance-empty')).toBeInTheDocument()
  })

  it('a failed read is an alert with a working retry', async () => {
    tenantGet.mockRejectedValueOnce(new ApiError('down', { status: 500 }))
    tenantGet.mockResolvedValue({ hours: 24, items: [member('model-a')] })
    const user = userEvent.setup()
    renderPanel()
    const alert = await screen.findByTestId('dashboard-load-failed')
    expect(screen.queryByTestId('dashboard-performance-empty')).toBeNull()
    await user.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('performance-rows')).toBeInTheDocument()
  })
})
