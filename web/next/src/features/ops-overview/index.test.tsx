import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api-error'

const { tGet, aGet } = vi.hoisted(() => ({ tGet: vi.fn(), aGet: vi.fn() }))
vi.mock('@/lib/api', async (orig) => {
  const mod = await orig<typeof import('@/lib/api')>()
  return {
    ...mod,
    api: { ...mod.api, get: aGet },
    tenantApi: { ...mod.tenantApi, get: tGet },
  }
})
vi.mock('@/lib/status', async (orig) => {
  const mod = await orig<typeof import('@/lib/status')>()
  return {
    ...mod,
    useMoneyConfig: () => ({
      quotaPerUnit: 100,
      displayType: 'USD',
      symbol: '$',
      rate: 1,
    }),
  }
})

import { PROBE_CONCURRENCY, mapLimited } from './lib/api'
import { OpsOverviewPage } from './index'

const NOW_SEC = Math.floor(Date.now() / 1000)

function ch(id: number, over: Record<string, unknown> = {}) {
  return {
    channel_id: id,
    name: `chan-${id}`,
    status: 1,
    requests: 10,
    errors: 1,
    prompt_tokens: 0,
    completion_tokens: 0,
    quota: 200,
    cost_cny4: 50000,
    plan_monthly_fee_cny4: 0,
    ...over,
  }
}

interface Setup {
  summary?: unknown
  health?: Record<number, unknown>
  detail?: Record<number, unknown>
  models?: unknown
}

function serve(s: Setup) {
  tGet.mockImplementation(async (path: string) => {
    if (path === '/channels/usage-summary') {
      if (s.summary instanceof Error) throw s.summary
      return s.summary
    }
    const m = /^\/channels\/(\d+)(\/health)?$/.exec(path)
    if (!m) throw new ApiError('not found', { status: 404 })
    const id = Number(m[1])
    const v = (m[2] ? s.health : s.detail)?.[id]
    if (v instanceof Error) throw v
    return v
  })
  aGet.mockImplementation(async () => {
    if (s.models instanceof Error) throw s.models
    return s.models
  })
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <OpsOverviewPage />
    </QueryClientProvider>
  )
}

const okModels = {
  models: [
    { model: 'model-a', status: 'operational' },
    { model: 'model-b', status: 'down' },
  ],
  updated_at: 1,
}

beforeEach(() => {
  tGet.mockReset()
  aGet.mockReset()
})

describe('OpsOverviewPage', () => {
  it('renders usage, health lists and model status', async () => {
    serve({
      summary: {
        window: '30d',
        since: 0,
        truncated: false,
        channels: [
          ch(1, {
            utilization: 1.5,
            verdict: 'overuse',
            plan_monthly_fee_cny4: 1,
          }),
          ch(2, {
            utilization: 0.05,
            verdict: 'idle',
            plan_monthly_fee_cny4: 1,
          }),
          ch(3),
          ch(4, { status: 2 }),
        ],
      },
      health: {
        1: {
          channel_id: 1,
          routable: true,
          reasons: [],
          window: { windows: [{ used_pct: 95 }] },
        },
        2: { channel_id: 2, routable: true, reasons: [] },
        3: {
          channel_id: 3,
          routable: false,
          reasons: ['cooling_429'],
          last_error: 'rate limited',
        },
      },
      detail: {
        1: { id: 1, setting: JSON.stringify({ expires_at: NOW_SEC + 3600 }) },
        2: { id: 2 },
        3: { id: 3 },
      },
      models: okModels,
    })
    renderPage()

    // 4 x 50000 cny4 = 20.00 yuan; quota 800 / 100 per unit = $8.00
    expect(await screen.findByText('¥20.00')).toBeInTheDocument()
    expect(screen.getByText('$8.00')).toBeInTheDocument()
    expect(screen.getByText('Overused')).toBeInTheDocument()
    expect(screen.getByText('Idle')).toBeInTheDocument()

    const expiring = await screen.findByTestId('expiring-list')
    expect(expiring).toHaveTextContent('chan-1')
    expect(screen.getByTestId('window-list')).toHaveTextContent('95%')
    const bad = screen.getByTestId('unroutable-list')
    expect(bad).toHaveTextContent('chan-3')
    expect(bad).toHaveTextContent('rate limited')
    expect(bad).toHaveTextContent('chan-4')
    expect(await screen.findByTestId('model-status-list')).toHaveTextContent(
      'model-b'
    )
  })

  it('shows an error, not an empty state, when the summary read fails', async () => {
    serve({ summary: new ApiError('boom', { status: 500 }), models: okModels })
    renderPage()
    const err = await screen.findByTestId('usage-error')
    expect(err).toHaveTextContent('boom')
    expect(screen.queryByText('No channels.')).not.toBeInTheDocument()
  })

  it('shows an error when model status fails, usage still renders', async () => {
    serve({
      summary: { window: '30d', since: 0, truncated: false, channels: [ch(3)] },
      health: { 3: { channel_id: 3, routable: true, reasons: [] } },
      detail: { 3: { id: 3 } },
      models: new ApiError('status down', { status: 503 }),
    })
    renderPage()
    expect(await screen.findByTestId('model-status-error')).toHaveTextContent(
      'status down'
    )
    expect(screen.getByTestId('usage-section')).toBeInTheDocument()
  })

  it('reports channels whose health could not be read', async () => {
    serve({
      summary: {
        window: '30d',
        since: 0,
        truncated: false,
        channels: [ch(1), ch(2)],
      },
      health: {
        1: { channel_id: 1, routable: true, reasons: [] },
        2: new ApiError('denied', { status: 403 }),
      },
      detail: { 1: { id: 1 }, 2: { id: 2 } },
      models: okModels,
    })
    renderPage()
    const note = await screen.findByTestId('probes-partial')
    await waitFor(() => expect(note).toHaveTextContent('#2 (denied)'))
  })
})

describe('mapLimited', () => {
  it('never exceeds the concurrency limit and keeps order', async () => {
    let active = 0
    let peak = 0
    const items = Array.from({ length: 20 }, (_, i) => i)
    const out = await mapLimited(items, PROBE_CONCURRENCY, async (i) => {
      active++
      peak = Math.max(peak, active)
      await new Promise((r) => setTimeout(r, 2))
      active--
      return i * 2
    })
    expect(peak).toBeLessThanOrEqual(PROBE_CONCURRENCY)
    expect(out).toEqual(items.map((i) => i * 2))
  })
})
