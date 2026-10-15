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

import { AdminCostPage } from './index'

const money = vi.hoisted(() => ({
  current: null as MoneyConfig | null,
}))
vi.mock('@/lib/status', async (orig) => {
  const mod = await orig<typeof import('@/lib/status')>()
  return { ...mod, useMoneyConfig: () => money.current }
})

/*
 * Contracts carried over from the legacy savings page:
 *  - the headline is derived from the payload (USD = quota / quota_per_unit),
 *    never a literal;
 *  - a failed read is its own state, with a retry; "returned no data" is only
 *    for a successful reply that is empty;
 *  - 403 (role short) and 401 (session gone) are different panels.
 */

const USD_500K: MoneyConfig = {
  quotaPerUnit: 500000,
  displayType: 'USD',
  symbol: '$',
  rate: 1,
}

const SAVINGS_URL = '/api/v2/admin/governance/savings?hours=168'

function payload(over: Record<string, unknown> = {}) {
  return {
    window_hours: 168,
    quota_per_usd: 500000,
    heuristic: true,
    caveat: 'aggregate estimate, conservative on cache; tiers heuristic',
    total_spend_quota: 50000000,
    spend_by_model: [
      { model_name: 'model-a', count: 10, total_quota: 30000000 },
      { model_name: 'model-b', count: 3, total_quota: 20000000 },
    ],
    spend_by_product: [
      { product: 'product-x', count: 8, total_quota: 25000000 },
      { product: 'product-y', count: 2, total_quota: 5000000 },
    ],
    scenarios: {
      conservative: {
        total_savings_quota: 2500000,
        savings_pct: 0.05,
        top_opportunities: [
          {
            model: 'model-a',
            alt_model: 'model-a-mini',
            current_quota: 30000000,
            savings_quota: 2500000,
            savings_pct: 0.08,
          },
        ],
      },
      aggressive: {
        total_savings_quota: 4000000,
        savings_pct: 0.08,
        top_opportunities: [
          {
            model: 'model-a',
            alt_model: 'vendor-a-chat',
            current_quota: 30000000,
            savings_quota: 4000000,
            savings_pct: 0.13,
          },
        ],
      },
    },
    excluded: [
      { model: 'image-model-a', quota: 100, reason: 'per_call_priced' },
    ],
    ...over,
  }
}

type Outcome = { ok: unknown } | { fail: unknown }

let request: MockInstance<typeof http.request>

function serve(next: () => Outcome) {
  request.mockImplementation(async () => {
    const out = next()
    if ('fail' in out) throw out.fail
    return { status: 200, data: out.ok } as never
  })
}

const ok = (data: unknown): Outcome => ({
  ok: { success: true, data },
})

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <AdminCostPage />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  money.current = USD_500K
  request = vi.spyOn(http, 'request')
})

describe('AdminCostPage savings', () => {
  it('headline reflects the payload, not a literal', async () => {
    serve(() => ok(payload()))
    renderPage()
    // 2_500_000 / 500_000 = $5.00 (conservative is the default scenario).
    await waitFor(() =>
      expect(screen.getByTestId('savings-headline')).toHaveTextContent('$5.00')
    )
    expect(screen.getByTestId('savings-headline').textContent).toBe('$5.00')
  })

  it('asks the savings endpoint for 7 days and does not bounce on 401', async () => {
    serve(() => ok(payload()))
    renderPage()
    await screen.findByTestId('savings-headline')
    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: 'GET',
        url: SAVINGS_URL,
        skipAuthRedirect: true,
      })
    )
  })

  it('renders the heuristic caveat banner from the server verbatim', async () => {
    serve(() => ok(payload()))
    renderPage()
    const caveat = await screen.findByTestId('caveat')
    expect(caveat).toHaveTextContent(
      'aggregate estimate, conservative on cache; tiers heuristic'
    )
  })

  it('still shows a caveat when the server sent none', async () => {
    serve(() => ok(payload({ caveat: '' })))
    renderPage()
    expect((await screen.findByTestId('caveat')).textContent).toMatch(
      /heuristic/i
    )
  })

  it('toggling the scenario switches headline, label and opportunities', async () => {
    serve(() => ok(payload()))
    renderPage()
    const aggressive = await screen.findByTestId('scenario-aggressive')
    expect(screen.getByTestId('scenario-conservative')).toHaveAttribute(
      'aria-pressed',
      'true'
    )
    expect(
      within(screen.getByTestId('top-opportunities')).getByText('model-a-mini')
    ).toBeInTheDocument()

    fireEvent.click(aggressive)

    await waitFor(() =>
      // 4_000_000 / 500_000 = $8.00
      expect(screen.getByTestId('savings-headline').textContent).toBe('$8.00')
    )
    expect(aggressive).toHaveAttribute('aria-pressed', 'true')
    const opp = within(screen.getByTestId('top-opportunities'))
    expect(opp.getByText('vendor-a-chat')).toBeInTheDocument()
    expect(opp.queryByText('model-a-mini')).toBeNull()
    expect(opp.getByText('13%')).toBeInTheDocument()
    expect(
      screen.getByText('of $100.00 total spend · 8.0% potential')
    ).toBeInTheDocument()
  })

  it('zero savings renders the explicit no-eligible-savings message', async () => {
    const zero = {
      total_savings_quota: 0,
      savings_pct: 0,
      top_opportunities: [],
    }
    serve(() =>
      ok(
        payload({
          total_spend_quota: 0,
          spend_by_model: [],
          spend_by_product: [],
          scenarios: { conservative: zero, aggressive: zero },
          excluded: [],
        })
      )
    )
    renderPage()
    await waitFor(() =>
      expect(screen.getByTestId('savings-headline').textContent).toBe(
        '$0.00 — no eligible savings'
      )
    )
    expect(
      screen.getByText('No eligible savings opportunities in this window')
    ).toBeInTheDocument()
    expect(screen.queryByTestId('excluded')).toBeNull()
  })

  it('shows spend by model and by product as money', async () => {
    serve(() => ok(payload()))
    renderPage()
    const byModel = within(await screen.findByTestId('spend-by-model'))
    expect(byModel.getByText('model-a')).toBeInTheDocument()
    expect(byModel.getByText('$60.00')).toBeInTheDocument()
    expect(byModel.getByText('$40.00')).toBeInTheDocument()
    const byProduct = within(screen.getByTestId('spend-by-product'))
    expect(byProduct.getByText('product-x')).toBeInTheDocument()
    expect(byProduct.getByText('$50.00')).toBeInTheDocument()
    expect(byProduct.getByText('$10.00')).toBeInTheDocument()
  })

  it('lists the opportunity with alternative, current spend and percent', async () => {
    serve(() => ok(payload()))
    renderPage()
    const opp = within(await screen.findByTestId('top-opportunities'))
    expect(opp.getByText('model-a')).toBeInTheDocument()
    expect(opp.getByText('model-a-mini')).toBeInTheDocument()
    expect(opp.getByText('$60.00')).toBeInTheDocument()
    expect(opp.getByText('$5.00')).toBeInTheDocument()
    expect(opp.getByText('8%')).toBeInTheDocument()
  })

  it('lists the models left out of re-pricing with their reason', async () => {
    serve(() => ok(payload()))
    renderPage()
    expect(await screen.findByTestId('excluded')).toHaveTextContent(
      'image-model-a (per_call_priced)'
    )
  })

  it('never guesses an amount while the money config is unknown', async () => {
    money.current = null
    serve(() => ok(payload()))
    renderPage()
    await waitFor(() =>
      expect(screen.getByTestId('savings-headline').textContent).toBe('--')
    )
    expect(screen.queryByText(/\$/)).toBeNull()
  })

  it('follows a token-denominated display instead of forcing dollars', async () => {
    money.current = {
      quotaPerUnit: 500000,
      displayType: 'TOKENS',
      symbol: '',
      rate: 1,
    }
    serve(() => ok(payload()))
    renderPage()
    await waitFor(() =>
      expect(screen.getByTestId('savings-headline').textContent).toBe(
        '2,500,000'
      )
    )
  })

  /*
   * A failed read is not "no savings". A 502 once rendered the same state as
   * an empty answer, which tells an operator there is nothing to save when
   * nobody asked the question.
   */
  it('shows an error state with a retry, not "returned no data", on a 502', async () => {
    serve(() => ({ fail: new ApiError('Bad gateway', { status: 502 }) }))
    renderPage()
    await screen.findByTestId('cost-error')
    expect(screen.queryByTestId('cost-no-data')).toBeNull()
    expect(screen.queryByTestId('savings-headline')).toBeNull()
    expect(
      screen.getAllByRole('button', { name: 'Retry' }).length
    ).toBeGreaterThan(0)
  })

  it('shows an error state when the call rejects with no response at all', async () => {
    serve(() => ({ fail: new Error('Network Error') }))
    renderPage()
    await screen.findByTestId('cost-error')
    expect(screen.queryByTestId('cost-no-data')).toBeNull()
  })

  it('shows an error state, with the message, on a 200 carrying success:false', async () => {
    serve(() => ({
      ok: { success: false, message: 'savings job has not run yet' },
    }))
    renderPage()
    const err = await screen.findByTestId('cost-error')
    expect(err).toHaveTextContent('savings job has not run yet')
    expect(screen.queryByTestId('savings-headline')).toBeNull()
  })

  it('retry refetches and clears the error state', async () => {
    let fail = true
    serve(() =>
      fail ? { fail: new ApiError('boom', { status: 502 }) } : ok(payload())
    )
    renderPage()
    await screen.findByTestId('cost-error')

    fail = false
    fireEvent.click(screen.getAllByRole('button', { name: 'Retry' })[0])

    await waitFor(() =>
      expect(screen.getByTestId('savings-headline').textContent).toBe('$5.00')
    )
    expect(screen.queryByTestId('cost-error')).toBeNull()
  })

  it('shows the permission panel on 403', async () => {
    serve(() => ({ fail: new ApiError('forbidden', { status: 403 }) }))
    renderPage()
    expect(
      await screen.findByText(
        'You do not have permission to view platform cost intelligence.'
      )
    ).toBeInTheDocument()
    expect(screen.queryByTestId('cost-error')).toBeNull()
    expect(screen.queryByTestId('cost-signed-out')).toBeNull()
  })

  /*
   * 401 is its own state: the v2 admin group answers 401 for a missing session
   * and 403 for a role shortfall, so an expired cookie must not be reported as
   * a permission the operator has to go and ask for.
   */
  it('shows a sign-in-again state on 401, not the permission panel', async () => {
    serve(() => ({ fail: new ApiError('no session', { status: 401 }) }))
    renderPage()
    await screen.findByTestId('cost-signed-out')
    expect(screen.getByTestId('cost-sign-in').getAttribute('href')).toMatch(
      /^\/login(\?|$)/
    )
    expect(
      screen.queryByText(
        'You do not have permission to view platform cost intelligence.'
      )
    ).toBeNull()
    expect(screen.queryByTestId('cost-error')).toBeNull()
    expect(screen.queryByTestId('savings-headline')).toBeNull()
  })

  // A 200 with a null payload is a real answer and must keep reading as one.
  it('keeps the no-data notice for a successful response with no payload', async () => {
    serve(() => ok(null))
    renderPage()
    const notice = await screen.findByTestId('cost-no-data')
    expect(notice).toHaveTextContent('Savings endpoint returned no data')
    expect(screen.queryByTestId('cost-error')).toBeNull()
  })

  it('tolerates a payload missing the optional collections', async () => {
    serve(() => ok({ total_spend_quota: 1000000 }))
    renderPage()
    await waitFor(() =>
      expect(screen.getByTestId('savings-headline').textContent).toBe(
        '$0.00 — no eligible savings'
      )
    )
    expect(screen.getAllByText('No data').length).toBe(2)
  })
})
