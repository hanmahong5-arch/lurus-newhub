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
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import { beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest'

import { ApiError, http } from '@/lib/api'
import type { MoneyConfig } from '@/lib/money'

import { AdminModelPerformancePage } from './index'

const money = vi.hoisted(() => ({
  current: null as MoneyConfig | null,
}))
vi.mock('@/lib/status', async (orig) => {
  const mod = await orig<typeof import('@/lib/status')>()
  return { ...mod, useMoneyConfig: () => money.current }
})

/*
 * This page is what an operator opens during an incident. Contracts carried
 * over from the legacy page: every failure is an error state (never "0
 * requests"), 403 and 401 are different panels, the tenant list is a
 * non-fatal side call, latency without samples is a dash, and the CSV export
 * follows the window and tenant actually applied.
 */

const USD_500K: MoneyConfig = {
  quotaPerUnit: 500000,
  displayType: 'USD',
  symbol: '$',
  rate: 1,
}

const PERF_PATH = '/api/v2/admin/analytics/model-performance'
const TENANTS_URL = '/api/v2/admin/tenants?page_size=100'

const NOW_SEC = 1_700_086_400

function model(over: Record<string, unknown> = {}) {
  return {
    model_name: 'model-a',
    requests: 120,
    errors: 6,
    error_rate: 0.05,
    prompt_tokens: 1000,
    completion_tokens: 500,
    quota: 500000,
    avg_latency_ms: 210,
    p50_latency_ms: 190,
    p95_latency_ms: 400,
    latency_samples: 114,
    ...over,
  }
}

const MODELS = [model()]

function perf(models: unknown[], window = {}) {
  return {
    success: true,
    data: {
      models,
      start_time: NOW_SEC - 86400,
      end_time: NOW_SEC,
      ...window,
    },
  }
}

interface Script {
  perf: () => unknown
  tenants?: () => unknown
}

let request: MockInstance<typeof http.request>

function urlsFor(prefix: string): string[] {
  return request.mock.calls
    .map((c) => String((c[0] as { url?: string }).url))
    .filter((u) => u.startsWith(prefix))
}

function lastPerfParams(): URLSearchParams {
  const urls = urlsFor(PERF_PATH)
  return new URLSearchParams((urls.at(-1) ?? '').split('?')[1])
}

/** `fail` results are thrown; anything else is the response body. */
function serve(script: Script) {
  request.mockImplementation(async (config) => {
    const url = String((config as { url?: string }).url)
    const pick = url.startsWith(PERF_PATH)
      ? script.perf
      : (script.tenants ??
        (() => ({ success: true, data: { tenants: [], total: 0 } })))
    const out = pick()
    if (out instanceof Error) throw out
    return { status: 200, data: out } as never
  })
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <AdminModelPerformancePage />
    </QueryClientProvider>
  )
}

function rowNames(): string[] {
  return screen
    .getAllByTestId('perf-row')
    .map((r) => within(r).getAllByRole('cell')[0].textContent ?? '')
}

beforeEach(() => {
  money.current = USD_500K
  request = vi.spyOn(http, 'request')
})

describe('AdminModelPerformancePage data', () => {
  it('renders the summary and a per-model row on a successful fetch', async () => {
    serve({ perf: () => perf(MODELS) })
    renderPage()

    await screen.findByTestId('perf-row')
    expect(screen.getByText('model-a')).toBeInTheDocument()
    // 120 requests reaches the summary card, so the "no zeroes" assertions in
    // the failure tests are about the failure branch and not an empty fixture.
    const total = screen
      .getByText('Total requests')
      .closest('[data-slot="card"]')
    expect(total).toHaveTextContent('120')
    expect(screen.queryByTestId('perf-error')).toBeNull()
  })

  it('asks for the last 24h by default, with no tenant filter, and without bouncing on 401', async () => {
    serve({ perf: () => perf(MODELS) })
    renderPage()
    await screen.findByTestId('perf-row')

    const params = lastPerfParams()
    expect(Number(params.get('end')) - Number(params.get('start'))).toBe(86400)
    expect(params.has('tenant_id')).toBe(false)
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({ skipAuthRedirect: true })
    )
    expect(screen.getByTestId('perf-range-24h')).toHaveAttribute(
      'aria-pressed',
      'true'
    )
  })

  it('computes the summary cards: error rate, sample-weighted latency, spend', async () => {
    serve({
      perf: () =>
        perf([
          model({ model_name: 'model-a' }),
          model({
            model_name: 'model-b',
            requests: 80,
            errors: 4,
            error_rate: 0.05,
            quota: 1000000,
            avg_latency_ms: 100,
            latency_samples: 76,
          }),
        ]),
    })
    renderPage()
    await screen.findAllByTestId('perf-row')

    const card = (label: string) =>
      screen.getByText(label).closest('[data-slot="card"]') as HTMLElement
    expect(card('Total requests')).toHaveTextContent('200')
    // 10 errors of 200 requests.
    expect(card('Total error rate')).toHaveTextContent('5.00%')
    expect(card('Total error rate')).toHaveTextContent('10 errors')
    // (210*114 + 100*76) / 190 = 166 ms: weighted by samples, not requests.
    expect(card('Weighted avg latency')).toHaveTextContent('166ms')
    expect(card('Weighted avg latency')).toHaveTextContent('190 samples')
    // (500000 + 1000000) / 500000 = $3.00
    expect(card('Total spend')).toHaveTextContent('$3.00')
  })

  it('formats the row: tokens, spend, error count and latency percentiles', async () => {
    serve({ perf: () => perf(MODELS) })
    renderPage()
    const row = within(await screen.findByTestId('perf-row'))
    expect(row.getByText('1,000 / 500')).toBeInTheDocument()
    expect(row.getByText('$1.00')).toBeInTheDocument()
    expect(row.getByText('5.00%')).toBeInTheDocument()
    expect(row.getByText('(6)')).toBeInTheDocument()
    expect(row.getByText('210ms')).toBeInTheDocument()
    expect(row.getByText('190ms')).toBeInTheDocument()
    expect(row.getByText('400ms')).toBeInTheDocument()
  })

  it('shows a dash, not 0ms, for rows with no latency samples', async () => {
    serve({
      perf: () =>
        perf([
          model({
            latency_samples: 0,
            avg_latency_ms: 0,
            p50_latency_ms: 0,
            p95_latency_ms: 0,
          }),
        ]),
    })
    renderPage()
    const row = within(await screen.findByTestId('perf-row'))
    expect(row.queryByText('0ms')).toBeNull()
    expect(row.getAllByText('—')).toHaveLength(3)
    const lat = screen
      .getByText('Weighted avg latency')
      .closest('[data-slot="card"]')
    expect(lat).toHaveTextContent('—')
    expect(lat).toHaveTextContent('0 samples')
  })

  it('does not invent a rate when there were no requests', async () => {
    serve({ perf: () => perf([]) })
    renderPage()
    expect(
      await screen.findByText('No traffic in this window')
    ).toBeInTheDocument()
    const err = screen
      .getByText('Total error rate')
      .closest('[data-slot="card"]')
    expect(err).toHaveTextContent('—')
    expect(err).not.toHaveTextContent('%')
    expect(screen.queryByTestId('perf-row')).toBeNull()
  })

  it('never guesses an amount while the money config is unknown', async () => {
    money.current = null
    serve({ perf: () => perf(MODELS) })
    renderPage()
    const row = within(await screen.findByTestId('perf-row'))
    expect(row.getByText('--')).toBeInTheDocument()
    expect(row.queryByText(/\$/)).toBeNull()
  })
})

describe('AdminModelPerformancePage failures', () => {
  /*
   * Until cycle 12 the legacy catch recognised 403 and nothing else, so a 502
   * rendered "0 requests" and an empty table. Each case asserts both halves:
   * the error state is visible AND the zeroes are not.
   */
  it('shows an error state, not 0 requests, when the endpoint 502s', async () => {
    serve({ perf: () => new ApiError('Bad gateway', { status: 502 }) })
    renderPage()

    await screen.findByTestId('perf-error')
    expect(screen.getByRole('heading', { level: 1 }).textContent).toContain(
      'unknown'
    )
    expect(screen.queryByTestId('perf-row')).toBeNull()
    expect(screen.queryByText('No traffic in this window')).toBeNull()
    expect(screen.queryByText('Total requests')).toBeNull()
    expect(
      screen.getAllByRole('button', { name: 'Retry' }).length
    ).toBeGreaterThan(0)
    // The CSV export would download the same unreadable window.
    expect(screen.queryByTestId('perf-export-btn')).toBeNull()
  })

  it('shows an error state when the network call rejects with no response', async () => {
    serve({ perf: () => new Error('Network Error') })
    renderPage()
    await screen.findByTestId('perf-error')
    expect(screen.queryByText('Total requests')).toBeNull()
  })

  it('shows an error state, with the message, on a 200 carrying success:false', async () => {
    serve({
      perf: () => ({ success: false, message: 'analytics store unavailable' }),
    })
    renderPage()
    expect(await screen.findByTestId('perf-error')).toHaveTextContent(
      'analytics store unavailable'
    )
    expect(screen.queryByText('Total requests')).toBeNull()
  })

  it('shows the permission panel on 403 and no error state', async () => {
    serve({ perf: () => new ApiError('forbidden', { status: 403 }) })
    renderPage()
    expect(
      await screen.findByText(
        'You do not have permission to view model performance analytics.'
      )
    ).toBeInTheDocument()
    expect(screen.queryByTestId('perf-error')).toBeNull()
    expect(screen.queryByTestId('perf-signed-out')).toBeNull()
    expect(screen.queryByTestId('perf-export-btn')).toBeNull()
    expect(screen.queryByTestId('perf-tenant-filter')).toBeNull()
  })

  // An expired cookie is not a permission problem: telling the operator to ask
  // for access sends them after something they already have.
  it('shows a sign-in-again state on 401, not the permission panel', async () => {
    serve({ perf: () => new ApiError('no session', { status: 401 }) })
    renderPage()
    await screen.findByTestId('perf-signed-out')
    expect(screen.getByTestId('perf-sign-in').getAttribute('href')).toMatch(
      /^\/login(\?|$)/
    )
    expect(
      screen.queryByText(
        'You do not have permission to view model performance analytics.'
      )
    ).toBeNull()
    expect(screen.queryByText('Total requests')).toBeNull()
  })

  it('retry re-runs the fetch and leaves the error state once it succeeds', async () => {
    let fail = true
    serve({
      perf: () =>
        fail ? new ApiError('Bad gateway', { status: 502 }) : perf(MODELS),
    })
    renderPage()
    await screen.findByTestId('perf-error')

    fail = false
    fireEvent.click(screen.getAllByRole('button', { name: 'Retry' })[0])

    await screen.findByTestId('perf-row')
    expect(screen.queryByTestId('perf-error')).toBeNull()
    expect(screen.getByText('model-a')).toBeInTheDocument()
  })

  it('lets the operator switch range after a failure', async () => {
    let fail = true
    serve({
      perf: () =>
        fail ? new ApiError('Bad gateway', { status: 502 }) : perf(MODELS),
    })
    renderPage()
    await screen.findByTestId('perf-error')

    fail = false
    fireEvent.click(screen.getByTestId('perf-range-1h'))

    await screen.findByTestId('perf-row')
    expect(screen.queryByTestId('perf-error')).toBeNull()
  })
})

describe('AdminModelPerformancePage filters', () => {
  it('range presets refetch with the matching window', async () => {
    serve({ perf: () => perf(MODELS) })
    renderPage()
    await screen.findByTestId('perf-row')

    fireEvent.click(screen.getByTestId('perf-range-7d'))
    await waitFor(() => {
      const p = lastPerfParams()
      expect(Number(p.get('end')) - Number(p.get('start'))).toBe(7 * 86400)
    })
    expect(screen.getByTestId('perf-range-7d')).toHaveAttribute(
      'aria-pressed',
      'true'
    )

    fireEvent.click(screen.getByTestId('perf-range-1h'))
    await waitFor(() => {
      const p = lastPerfParams()
      expect(Number(p.get('end')) - Number(p.get('start'))).toBe(3600)
    })
  })

  it('lists tenants by name, falling back to slug then id, and filters by id', async () => {
    serve({
      perf: () => perf(MODELS),
      tenants: () => ({
        success: true,
        data: {
          tenants: [
            { id: 't-1', name: 'Tenant One', slug: 'one' },
            { id: 't-2', name: '', slug: 'two' },
            { id: 't-3' },
          ],
          total: 3,
        },
      }),
    })
    renderPage()
    await screen.findByTestId('perf-row')
    await screen.findByRole('option', { name: 'Tenant One' })
    expect(screen.getByRole('option', { name: 'two' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 't-3' })).toBeInTheDocument()
    expect(urlsFor('/api/v2/admin/tenants')).toEqual([TENANTS_URL])

    fireEvent.change(screen.getByTestId('perf-tenant-filter'), {
      target: { value: 't-1' },
    })
    await waitFor(() => expect(lastPerfParams().get('tenant_id')).toBe('t-1'))

    fireEvent.change(screen.getByTestId('perf-tenant-filter'), {
      target: { value: '' },
    })
    await waitFor(() => expect(lastPerfParams().has('tenant_id')).toBe(false))
  })

  it('keeps working when the tenant list cannot be read', async () => {
    serve({
      perf: () => perf(MODELS),
      tenants: () => new ApiError('boom', { status: 500 }),
    })
    renderPage()
    await screen.findByTestId('perf-row')
    expect(screen.queryByTestId('perf-error')).toBeNull()
    expect(
      screen.getByRole('option', { name: 'All tenants' })
    ).toBeInTheDocument()
  })

  it('reads further tenant pages when there are more than one page', async () => {
    const page = (ids: string[], total: number) => ({
      success: true,
      data: { tenants: ids.map((id) => ({ id, name: `name-${id}` })), total },
    })
    request.mockImplementation(async (config) => {
      const url = String((config as { url?: string }).url)
      if (url.startsWith(PERF_PATH)) {
        return { status: 200, data: perf(MODELS) } as never
      }
      return {
        status: 200,
        data: url.includes('&page=2') ? page(['b'], 2) : page(['a'], 2),
      } as never
    })
    renderPage()
    await screen.findByRole('option', { name: 'name-b' })
    expect(screen.getByRole('option', { name: 'name-a' })).toBeInTheDocument()
  })
})

describe('AdminModelPerformancePage sorting', () => {
  const MANY = [
    model({
      model_name: 'low-traffic',
      requests: 10,
      error_rate: 0.5,
      p95_latency_ms: 900,
    }),
    model({
      model_name: 'high-traffic',
      requests: 900,
      error_rate: 0.001,
      p95_latency_ms: 100,
    }),
    model({
      model_name: 'mid-traffic',
      requests: 100,
      error_rate: 0.1,
      p95_latency_ms: 500,
    }),
  ]

  it('defaults to requests descending', async () => {
    serve({ perf: () => perf(MANY) })
    renderPage()
    await screen.findAllByTestId('perf-row')
    expect(rowNames()).toEqual(['high-traffic', 'mid-traffic', 'low-traffic'])
  })

  it('clicking a new column sorts it descending, clicking again flips to ascending', async () => {
    serve({ perf: () => perf(MANY) })
    renderPage()
    await screen.findAllByTestId('perf-row')

    fireEvent.click(screen.getByTestId('perf-sort-error_rate'))
    expect(rowNames()).toEqual(['low-traffic', 'mid-traffic', 'high-traffic'])
    expect(
      screen.getByTestId('perf-sort-error_rate').closest('th')
    ).toHaveAttribute('aria-sort', 'descending')

    fireEvent.click(screen.getByTestId('perf-sort-error_rate'))
    expect(rowNames()).toEqual(['high-traffic', 'mid-traffic', 'low-traffic'])
    expect(
      screen.getByTestId('perf-sort-error_rate').closest('th')
    ).toHaveAttribute('aria-sort', 'ascending')

    fireEvent.click(screen.getByTestId('perf-sort-p95_latency_ms'))
    expect(rowNames()).toEqual(['low-traffic', 'mid-traffic', 'high-traffic'])
    expect(
      screen.getByTestId('perf-sort-error_rate').closest('th')
    ).toHaveAttribute('aria-sort', 'none')
  })

  it('every latency column and requests are sortable', async () => {
    serve({ perf: () => perf(MANY) })
    renderPage()
    await screen.findAllByTestId('perf-row')
    for (const key of [
      'requests',
      'error_rate',
      'avg_latency_ms',
      'p50_latency_ms',
      'p95_latency_ms',
    ]) {
      expect(screen.getByTestId(`perf-sort-${key}`)).toBeInTheDocument()
    }
  })
})

describe('AdminModelPerformancePage export', () => {
  it('points the CSV export at the window the server applied', async () => {
    serve({
      perf: () =>
        perf(MODELS, { start_time: 1_700_000_000, end_time: 1_700_086_400 }),
    })
    renderPage()
    await screen.findByTestId('perf-row')
    const href =
      screen.getByTestId('perf-export-btn').getAttribute('href') ?? ''
    expect(href.startsWith('/api/v2/admin/logs/export?')).toBe(true)
    const p = new URLSearchParams(href.split('?')[1])
    expect(p.get('format')).toBe('csv')
    expect(p.get('start_time')).toBe('1700000000')
    expect(p.get('end_time')).toBe('1700086400')
    expect(p.has('tenant_id')).toBe(false)
  })

  it('carries the tenant filter into the export', async () => {
    serve({
      perf: () => perf(MODELS),
      tenants: () => ({
        success: true,
        data: { tenants: [{ id: 't-1', name: 'Tenant One' }], total: 1 },
      }),
    })
    renderPage()
    await screen.findByRole('option', { name: 'Tenant One' })
    fireEvent.change(screen.getByTestId('perf-tenant-filter'), {
      target: { value: 't-1' },
    })
    await waitFor(() => {
      const href =
        screen.getByTestId('perf-export-btn').getAttribute('href') ?? ''
      expect(new URLSearchParams(href.split('?')[1]).get('tenant_id')).toBe(
        't-1'
      )
    })
  })
})
