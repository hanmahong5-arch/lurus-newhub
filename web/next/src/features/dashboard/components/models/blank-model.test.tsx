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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

import { ModelsTab } from './models-tab'
import { PerformanceOverview } from './performance-overview'

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

function renderWith(node: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>{node}</QueryClientProvider>
  )
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
    url === '/api/status'
      ? Promise.resolve({ quota_per_unit: 500000, quota_display_type: 'USD' })
      : Promise.resolve(null)
  )
  tenantGet.mockResolvedValue({ hours: 24, items: [] })
})

const member = (name: string, over: Record<string, unknown> = {}) => ({
  model_name: name,
  p50_latency_ms: 420,
  p95_latency_ms: 1800,
  error_rate: 0.004,
  enough_samples: true,
  ...over,
})

describe('PerformanceOverview rows with no model name', () => {
  it('labels a nameless row "Unknown model" and keeps it last whatever its volume', async () => {
    tenantGet.mockResolvedValue({
      hours: 24,
      items: [
        member('', { requests: 5000, errors: 50 }),
        member('model-a', { requests: 900, errors: 9 }),
        member('model-b', { requests: 100, errors: 1 }),
      ],
    })
    renderWith(<PerformanceOverview />)
    const rows = await screen.findAllByTestId('performance-row')
    expect(rows).toHaveLength(3)
    expect(rows[0]).toHaveTextContent('model-a')
    expect(rows[1]).toHaveTextContent('model-b')
    expect(rows[2]).toHaveTextContent('Unknown model')
    // Its figures are still its own, and it still counts in the pooled rate.
    expect(rows[2]).toHaveTextContent('5,000')
    expect(screen.getByTestId('performance-summary')).toHaveTextContent(
      '99.00%'
    )
  })

  it('does the same without volume, where rows are alphabetical', async () => {
    tenantGet.mockResolvedValue({
      hours: 24,
      items: [member(''), member('zeta'), member('alpha')],
    })
    renderWith(<PerformanceOverview />)
    const rows = await screen.findAllByTestId('performance-row')
    expect(rows).toHaveLength(3)
    expect(rows[0]).toHaveTextContent('alpha')
    expect(rows[1]).toHaveTextContent('zeta')
    expect(rows[2]).toHaveTextContent('Unknown model')
  })
})

describe('ModelsTab rows with no model name', () => {
  const nowSec = () => Math.floor(Date.now() / 1000)

  it('names it "Unknown model" in the legend and the share list, and lists it last', async () => {
    const t = nowSec() - 7200
    httpGet.mockImplementation(() =>
      Promise.resolve({
        status: 200,
        data: {
          success: true,
          data: [
            // More calls and spend than any named model.
            { created_at: t, quota: 900000, count: 20, token_used: 5 },
            { model_name: 'model-a', created_at: t, quota: 500000, count: 3, token_used: 1200 },
            { model_name: 'model-b', created_at: t, quota: 250000, count: 4, token_used: 800 },
          ],
        },
      })
    )
    const user = userEvent.setup()
    renderWith(<ModelsTab />)
    const legends = await screen.findAllByTestId('series-legend')
    expect(legends.length).toBeGreaterThan(0)
    // Spend chart ranks by quota (a, b), call chart by calls (b, a); the
    // nameless series, though largest in both, closes each legend.
    const orders = legends.map((legend) =>
      [...legend.querySelectorAll('li')].map((li) => li.textContent)
    )
    expect(orders).toContainEqual(['model-a', 'model-b', 'Unknown model'])
    for (const names of orders) expect(names[2]).toBe('Unknown model')
    await user.click(screen.getByTestId('model-chart-tab-proportion'))
    const rows = await screen.findByTestId('share-rows')
    const items = [...rows.querySelectorAll('li')]
    expect(items).toHaveLength(3)
    expect(items[0]).toHaveTextContent('model-b')
    expect(items[1]).toHaveTextContent('model-a')
    expect(items[2]).toHaveTextContent('Unknown model')
    // 20 of 27 calls: 74.1%.
    expect(items[2]).toHaveTextContent('74.1%')
    // Totals still include it.
    expect(screen.getByTestId('stat-count')).toHaveTextContent('27')
  })
})
