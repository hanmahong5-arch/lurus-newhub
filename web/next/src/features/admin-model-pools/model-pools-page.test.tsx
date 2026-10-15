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

import { AdminModelPoolsPage } from './index'
import { filterPools, summarize } from './lib/health'
import type { Pool } from './types'

const { get, signIn } = vi.hoisted(() => ({
  get: vi.fn(),
  signIn: vi.fn(),
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: { get, post: vi.fn(), put: vi.fn(), delete: vi.fn() },
  }
})

vi.mock('@/lib/auth', async (orig) => {
  const actual = await orig<typeof import('@/lib/auth')>()
  return { ...actual, redirectToLogin: signIn }
})

const PROBE_AT = 1_699_999_900

const CHANNEL = {
  id: 1,
  name: 'chan-a',
  type: 1,
  status: 'enabled',
  priority: 10,
  weight: 5,
  tenant_id: 'default',
  test_time: 1_699_999_000,
  response_time: 120,
  balance: 3.5,
  keys: { total: 3, enabled: 1, cooling: 1, disabled: 1 },
  models: [
    {
      model: 'm1',
      requests_24h: 2,
      errors_24h: 1,
      health: {
        ok: true,
        last_probe_at: PROBE_AT,
        latency_ms: 50,
        last_error: '',
        consecutive_failures: 0,
        auto_disabled: false,
      },
    },
    {
      model: 'm2',
      requests_24h: 0,
      errors_24h: 0,
      health: {
        ok: false,
        last_probe_at: 1_699_999_950,
        latency_ms: 0,
        last_error: 'boom upstream',
        consecutive_failures: 5,
        auto_disabled: true,
      },
    },
    { model: 'm3', requests_24h: 5, errors_24h: 0, health: null },
    {
      model: 'm4',
      requests_24h: 1_200,
      errors_24h: 1,
      health: {
        ok: false,
        last_probe_at: 1_699_999_960,
        latency_ms: 900,
        last_error:
          'timeout while waiting for the upstream vendor to answer the probe request',
        consecutive_failures: 2,
        auto_disabled: false,
      },
    },
  ],
}

const POOLS = [{ group: 'free', channels: [CHANNEL] }]

function overview(pools: unknown[]) {
  return { generated_at: 1_700_000_000, pools }
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <AdminModelPoolsPage />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('pool overview', () => {
  it('renders pool groups, the channel header and per-model health', async () => {
    get.mockResolvedValue(overview(POOLS))
    renderPage()

    expect(await screen.findByText('Pool · free')).toBeInTheDocument()
    expect(screen.getByText('chan-a')).toBeInTheDocument()
    expect(screen.getByText('OpenAI')).toBeInTheDocument()
    expect(screen.getByText('Enabled')).toBeInTheDocument()
    expect(
      screen.getByText('1 enabled / 1 cooling / 1 disabled')
    ).toBeInTheDocument()
    for (const m of ['m1', 'm2', 'm3', 'm4']) {
      expect(screen.getByText(m)).toBeInTheDocument()
    }

    expect(
      within(screen.getByTestId('pool-health-1-m1')).getByText('ok')
    ).toBeInTheDocument()
    expect(
      within(screen.getByTestId('pool-health-1-m2')).getByText('auto-disabled')
    ).toBeInTheDocument()
    expect(
      within(screen.getByTestId('pool-health-1-m3')).getByText('never probed')
    ).toBeInTheDocument()
    expect(
      within(screen.getByTestId('pool-health-1-m4')).getByText('failing (2)')
    ).toBeInTheDocument()
  })

  it('reads the overview from the admin endpoint without bouncing a 401 away', async () => {
    get.mockResolvedValue(overview(POOLS))
    renderPage()
    await screen.findByText('chan-a')
    expect(get).toHaveBeenCalledWith('/api/v2/admin/model-pools', {
      skipAuthRedirect: true,
    })
  })

  it('keeps the full error text reachable beyond the truncated cell', async () => {
    get.mockResolvedValue(overview(POOLS))
    renderPage()
    await screen.findByText('chan-a')

    expect(screen.getByTestId('pool-last-error-1-m2')).toHaveAttribute(
      'title',
      'boom upstream'
    )
    const long = screen.getByTestId('pool-last-error-1-m4')
    expect(long).toHaveAttribute(
      'title',
      'timeout while waiting for the upstream vendor to answer the probe request'
    )
    expect(long.textContent).toBe(
      `${'timeout while waiting for the upstream vendor to answer the probe request'.slice(0, 40)}…`
    )
  })

  it('right-aligns the latency/requests/errors columns with tabular figures', async () => {
    get.mockResolvedValue(overview(POOLS))
    renderPage()
    await screen.findByText('chan-a')

    const numeric = screen
      .getAllByRole('columnheader')
      .filter((th) => th.className.includes('text-right'))
    expect(numeric.map((th) => th.textContent)).toEqual([
      'Latency',
      'Requests (24h)',
      'Errors (24h)',
    ])

    const cells = screen
      .getByText('m1')
      .closest('tr')
      ?.querySelectorAll('td.text-right')
    expect(cells).toHaveLength(3)
    cells?.forEach((td) => expect(td.className).toContain('tabular-nums'))
    // m1: 50ms latency, 2 requests, 1 error. m2 has no latency -> dash.
    expect([...(cells ?? [])].map((td) => td.textContent)).toEqual([
      '50ms',
      '2',
      '1',
    ])
    const m2 = screen
      .getByText('m2')
      .closest('tr')
      ?.querySelectorAll('td.text-right')
    expect(m2?.[0]?.textContent).toBe('—')
    const m4 = screen
      .getByText('m4')
      .closest('tr')
      ?.querySelectorAll('td.text-right')
    expect(m4?.[1]?.textContent).toBe('1,200')
  })

  it('makes a never-probed model look different from a passing one', async () => {
    get.mockResolvedValue(overview(POOLS))
    renderPage()
    await screen.findByText('chan-a')

    const never = screen
      .getByTestId('pool-health-1-m3')
      .querySelector('[data-health]')
    const ok = screen
      .getByTestId('pool-health-1-m1')
      .querySelector('[data-health]')

    expect(never).toHaveAttribute('data-health', 'never_probed')
    expect(never).toHaveAttribute(
      'title',
      'Not probed yet — health is unknown, not healthy'
    )
    expect(never?.className).toContain('border-dashed')
    expect(ok).toHaveAttribute('data-health', 'ok')
    expect(ok).not.toHaveAttribute(
      'title',
      'Not probed yet — health is unknown, not healthy'
    )
    expect(ok?.className).not.toContain('border-dashed')
  })

  it('shows an unknown channel type and status rather than hiding them', async () => {
    get.mockResolvedValue(
      overview([
        {
          group: 'g',
          channels: [{ ...CHANNEL, type: 9999, status: 'mystery', models: [] }],
        },
      ])
    )
    renderPage()

    expect(await screen.findByText('Type #9999')).toBeInTheDocument()
    expect(screen.getByText('mystery')).toBeInTheDocument()
    expect(screen.getByText('No models configured')).toBeInTheDocument()
  })

  it('lists a channel under every routing group it belongs to', async () => {
    get.mockResolvedValue(
      overview([
        { group: 'default', channels: [CHANNEL] },
        { group: 'free', channels: [CHANNEL] },
      ])
    )
    renderPage()

    expect(await screen.findByText('Pool · default')).toBeInTheDocument()
    expect(screen.getByText('Pool · free')).toBeInTheDocument()
    expect(screen.getAllByText('chan-a')).toHaveLength(2)
  })

  it('shows an empty state when there are no pools', async () => {
    get.mockResolvedValue(overview([]))
    renderPage()

    expect(await screen.findByTestId('pools-empty')).toBeInTheDocument()
    expect(screen.queryByTestId('pools-error')).toBeNull()
  })

  it('treats a null pools field as no pools', async () => {
    get.mockResolvedValue({ generated_at: 1, pools: null })
    renderPage()
    expect(await screen.findByTestId('pools-empty')).toBeInTheDocument()
  })
})

describe('failure taxonomy', () => {
  it('shows an error state - not an empty pool list - when the endpoint 502s', async () => {
    get.mockRejectedValue(new ApiError('bad gateway', { status: 502 }))
    renderPage()

    expect(await screen.findByTestId('pools-error')).toBeInTheDocument()
    expect(screen.getByText('Model pools unknown')).toBeInTheDocument()
    expect(screen.getByText(/bad gateway/)).toBeInTheDocument()
    expect(screen.queryByTestId('pools-empty')).toBeNull()
    expect(screen.queryByText('chan-a')).toBeNull()
  })

  it('treats a 200 carrying success:false as an error, not as no pools', async () => {
    get.mockRejectedValue(new ApiError('failed to load model health'))
    renderPage()

    expect(await screen.findByTestId('pools-error')).toBeInTheDocument()
    expect(screen.queryByTestId('pools-empty')).toBeNull()
  })

  it('recovers when the retry succeeds', async () => {
    get.mockRejectedValueOnce(new ApiError('bad gateway', { status: 502 }))
    get.mockResolvedValue(overview(POOLS))
    const user = userEvent.setup()
    renderPage()

    await screen.findByTestId('pools-error')
    await user.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByText('chan-a')).toBeInTheDocument()
    expect(screen.queryByTestId('pools-error')).toBeNull()
  })

  it('shows the permission panel on 403, distinct from the error panel', async () => {
    get.mockRejectedValue(new ApiError('nope', { status: 403 }))
    renderPage()

    expect(await screen.findByTestId('pools-forbidden')).toBeInTheDocument()
    expect(screen.queryByTestId('pools-error')).toBeNull()
    expect(screen.queryByTestId('pools-signed-out')).toBeNull()
  })

  it('shows a sign-in-again state on 401 and sends the operator to sign in', async () => {
    get.mockRejectedValue(new ApiError('expired', { status: 401 }))
    const user = userEvent.setup()
    renderPage()

    expect(await screen.findByTestId('pools-signed-out')).toBeInTheDocument()
    expect(screen.queryByTestId('pools-error')).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Sign in again' }))
    expect(signIn).toHaveBeenCalledTimes(1)
  })
})

describe('refresh, filter and summary', () => {
  it('refetches on Refresh', async () => {
    get.mockResolvedValue(overview(POOLS))
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('chan-a')

    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  })

  it('summarises from the data it was given', async () => {
    get.mockResolvedValue(overview(POOLS))
    renderPage()
    await screen.findByText('chan-a')
    // 1 pool, 1 channel, 4 models, m2 (auto-disabled) + m4 (failing) unhealthy.
    expect(screen.getByTestId('pools-summary')).toHaveTextContent(
      '1 pools · 1 channels · 4 models · 2 unhealthy'
    )
  })

  it('filters by model name and keeps only matching rows', async () => {
    get.mockResolvedValue(overview(POOLS))
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('chan-a')

    await user.type(
      screen.getByLabelText('Filter pools, channels or models'),
      'm3'
    )
    expect(screen.getByText('m3')).toBeInTheDocument()
    expect(screen.queryByText('m1')).toBeNull()
    expect(screen.getByText('chan-a')).toBeInTheDocument()
  })

  it('a channel-name match keeps all of its models', async () => {
    get.mockResolvedValue(overview(POOLS))
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('chan-a')

    await user.type(
      screen.getByLabelText('Filter pools, channels or models'),
      'CHAN'
    )
    for (const m of ['m1', 'm2', 'm3', 'm4']) {
      expect(screen.getByText(m)).toBeInTheDocument()
    }
  })

  it('says so when nothing matches, instead of an empty page', async () => {
    get.mockResolvedValue(overview(POOLS))
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('chan-a')

    await user.type(
      screen.getByLabelText('Filter pools, channels or models'),
      'zzz-no-such'
    )
    expect(await screen.findByTestId('pools-no-match')).toBeInTheDocument()
    expect(screen.queryByTestId('pools-empty')).toBeNull()
  })

  it('"only unhealthy" hides ok and never-probed models', async () => {
    get.mockResolvedValue(overview(POOLS))
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('chan-a')

    await user.click(
      screen.getByRole('switch', { name: 'Only unhealthy models' })
    )
    expect(screen.getByText('m2')).toBeInTheDocument()
    expect(screen.getByText('m4')).toBeInTheDocument()
    expect(screen.queryByText('m1')).toBeNull()
    // Unknown health is not "unhealthy": it must not be reported as a fault.
    expect(screen.queryByText('m3')).toBeNull()
  })
})

describe('lib', () => {
  const pools: Pool[] = POOLS as unknown as Pool[]
  const label = (t: number) => (t === 1 ? 'OpenAI' : `Type #${t}`)

  it('filterPools returns the same pools untouched without a filter', () => {
    expect(filterPools(pools, '  ', false, label)).toBe(pools)
  })

  it('filterPools matches the pool name and keeps the whole pool', () => {
    const out = filterPools(pools, 'FREE', false, label)
    expect(out[0]?.channels[0]?.models).toHaveLength(4)
  })

  it('filterPools matches the channel type label', () => {
    expect(filterPools(pools, 'openai', false, label)).toHaveLength(1)
    expect(filterPools(pools, 'anthropic', false, label)).toHaveLength(0)
  })

  it('summarize counts unhealthy models only (never-probed is unknown)', () => {
    expect(summarize(pools)).toEqual({
      pools: 1,
      channels: 1,
      models: 4,
      unhealthy: 2,
    })
  })
})
