import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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
  healthSummary?: unknown
  models?: unknown
  /** Session user; defaults to platform staff below root. */
  user?: Record<string, unknown>
  /** GET /api/v2/admin/gateway/health body (or the error it rejects with). */
  gateway?: unknown
}

const STAFF_USER = { id: 1, username: 'staff', role: 10 }
const ROOT_USER = { id: 2, username: 'root', role: 100 }
const GATEWAY_URL = '/api/v2/admin/gateway/health'

function serve(s: Setup) {
  tGet.mockImplementation(async (path: string) => {
    if (path === '/user/me') return s.user ?? STAFF_USER
    if (path === '/channels/usage-summary') {
      if (s.summary instanceof Error) throw s.summary
      return s.summary
    }
    if (path === '/channels/health-summary') {
      if (s.healthSummary instanceof Error) throw s.healthSummary
      return s.healthSummary
    }
    throw new ApiError('not found', { status: 404 })
  })
  aGet.mockImplementation(async (url: string) => {
    if (url === GATEWAY_URL) {
      if (s.gateway instanceof Error) throw s.gateway
      return s.gateway
    }
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
      healthSummary: {
        total: 4,
        truncated: false,
        channels: [
          {
            id: 1,
            name: 'chan-1',
            routable: true,
            reasons: [],
            expires_at: NOW_SEC + 3600,
            window_max_used_pct: 95,
          },
          {
            id: 2,
            name: 'chan-2',
            routable: true,
            reasons: [],
            expires_at: 0,
            window_max_used_pct: null,
          },
          {
            id: 3,
            name: 'chan-3',
            routable: false,
            reasons: ['cooling_429'],
            expires_at: 0,
            window_max_used_pct: null,
            last_error: 'rate limited',
          },
          {
            id: 4,
            name: 'chan-4',
            routable: false,
            reasons: ['disabled_manual'],
            expires_at: 0,
            window_max_used_pct: null,
          },
        ],
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
      healthSummary: { channels: [], total: 0, truncated: false },
      models: new ApiError('status down', { status: 503 }),
    })
    renderPage()
    expect(await screen.findByTestId('model-status-error')).toHaveTextContent(
      'status down'
    )
    expect(screen.getByTestId('usage-section')).toBeInTheDocument()
  })

  it('reads health with ONE request, not one per channel', async () => {
    serve({
      summary: {
        window: '30d',
        since: 0,
        truncated: false,
        channels: Array.from({ length: 40 }, (_, i) => ch(i + 1)),
      },
      healthSummary: { channels: [], total: 0, truncated: false },
      models: okModels,
    })
    renderPage()
    await screen.findByTestId('usage-section')
    await waitFor(() =>
      expect(tGet).toHaveBeenCalledWith('/channels/health-summary')
    )
    const paths = tGet.mock.calls.map((c) => c[0] as string)
    expect(
      paths.filter(
        (p) =>
          p.startsWith('/channels/') &&
          p !== '/channels/health-summary' &&
          p !== '/channels/usage-summary'
      )
    ).toEqual([])
    expect(paths.filter((p) => p === '/channels/health-summary')).toHaveLength(
      1
    )
  })

  it('shows an error, not "all routable", when the roll-up fails', async () => {
    serve({
      summary: { window: '30d', since: 0, truncated: false, channels: [ch(1)] },
      healthSummary: new ApiError('denied', { status: 403 }),
      models: okModels,
    })
    renderPage()
    const errs = await screen.findAllByTestId('probes-error')
    expect(errs[0]).toHaveTextContent('denied')
    expect(
      screen.queryByText('Every channel is routable.')
    ).not.toBeInTheDocument()
  })

  it('warns when the server capped the roll-up', async () => {
    serve({
      summary: { window: '30d', since: 0, truncated: false, channels: [ch(1)] },
      healthSummary: { channels: [], total: 0, truncated: true },
      models: okModels,
    })
    renderPage()
    expect(await screen.findByTestId('probes-partial')).toBeInTheDocument()
  })

  describe('gateway health (platform root only)', () => {
    const gateway = {
      routes: [
        {
          channel_id: 7,
          state: 'closed',
          consecutive_fails: 0,
          threshold: 5,
          last_fail_unix: 0,
          channel_name: 'chan-closed',
          provider: 'openai',
        },
        {
          channel_id: 9,
          state: 'open',
          consecutive_fails: 5,
          threshold: 5,
          last_fail_unix: NOW_SEC - 60,
          probe_eligible_unix: NOW_SEC + 30,
          channel_name: 'chan-open',
          provider: 'claude',
        },
        {
          channel_id: 8,
          state: 'half_open',
          consecutive_fails: 3,
          threshold: 5,
          last_fail_unix: NOW_SEC - 10,
          probe_eligible_unix: NOW_SEC + 5,
        },
      ],
      tracked: 3,
      open: 1,
      half_open: 1,
      replica_scoped: true,
      lazy_registered: true,
    }
    const base: Setup = {
      summary: { window: '30d', since: 0, truncated: false, channels: [ch(1)] },
      healthSummary: { channels: [], total: 0, truncated: false },
      models: okModels,
    }
    const gatewayCalls = () =>
      aGet.mock.calls.filter((c) => c[0] === GATEWAY_URL)

    it('renders breaker counts, caveats and the routes, worst first', async () => {
      serve({ ...base, user: ROOT_USER, gateway })
      renderPage()
      await screen.findByTestId('gateway-health')
      expect(screen.getByTestId('gateway-tracked')).toHaveTextContent('3')
      expect(screen.getByTestId('gateway-open')).toHaveTextContent('1')
      expect(screen.getByTestId('gateway-half-open')).toHaveTextContent('1')
      expect(screen.getByTestId('gateway-caveats')).toHaveTextContent(
        'per replica'
      )
      expect(screen.getByTestId('gateway-caveats')).toHaveTextContent(
        'unknown, not healthy'
      )
      const rows = within(screen.getByTestId('gateway-routes'))
        .getAllByRole('row')
        .slice(1)
      expect(rows.map((r) => r.getAttribute('data-testid'))).toEqual([
        'gateway-route-9',
        'gateway-route-8',
        'gateway-route-7',
      ])
      const open = screen.getByTestId('gateway-route-9')
      expect(open).toHaveTextContent('chan-open')
      expect(open).toHaveTextContent('claude')
      expect(open).toHaveTextContent('Breaker open')
      expect(open).toHaveTextContent('5 / 5')
      const closed = screen.getByTestId('gateway-route-7')
      expect(closed).toHaveTextContent('Breaker closed')
      // never failed / no probe pending: a dash, not 1970
      expect(closed).toHaveTextContent('--')
      expect(closed).not.toHaveTextContent('1970')
    })

    it('says breaker state is unknown, not healthy, when no route is tracked', async () => {
      serve({
        ...base,
        user: ROOT_USER,
        gateway: { ...gateway, routes: [], tracked: 0, open: 0, half_open: 0 },
      })
      renderPage()
      expect(await screen.findByTestId('gateway-empty')).toHaveTextContent(
        'no breaker state is known'
      )
      expect(screen.queryByTestId('gateway-routes')).not.toBeInTheDocument()
    })

    it('shows a failed read as an error with retry, never as healthy', async () => {
      serve({
        ...base,
        user: ROOT_USER,
        gateway: new ApiError('gateway down', { status: 502 }),
      })
      renderPage()
      const err = await screen.findByTestId('gateway-error')
      expect(err).toHaveTextContent('Failed to load gateway health')
      expect(err).toHaveTextContent('gateway down')
      expect(screen.queryByTestId('gateway-health')).not.toBeInTheDocument()
      expect(screen.queryByTestId('gateway-open')).not.toBeInTheDocument()
      // retry re-reads and recovers
      serve({ ...base, user: ROOT_USER, gateway })
      await userEvent.setup().click(within(err).getByRole('button'))
      expect(await screen.findByTestId('gateway-health')).toBeInTheDocument()
    })

    it('rejects a body without routes instead of reading it as no open breakers', async () => {
      serve({ ...base, user: ROOT_USER, gateway: { tracked: 0 } })
      renderPage()
      expect(await screen.findByTestId('gateway-error')).toBeInTheDocument()
      expect(screen.queryByTestId('gateway-empty')).not.toBeInTheDocument()
    })

    it('renders nothing and makes no call for platform staff below root', async () => {
      serve({ ...base, user: STAFF_USER, gateway })
      renderPage()
      await screen.findByTestId('usage-section')
      await screen.findByTestId('model-status-list')
      expect(screen.queryByText('Gateway health')).not.toBeInTheDocument()
      expect(screen.queryByTestId('gateway-health')).not.toBeInTheDocument()
      expect(gatewayCalls()).toHaveLength(0)
    })
  })
})
