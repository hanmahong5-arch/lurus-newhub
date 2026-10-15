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
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { TenantPoolSheet } from './components/tenant-pool-sheet'
import { draftKey, DRAFT_SCHEMA_VERSION } from './lib/use-form-draft'
import {
  renderWithClient,
  serveGet,
  tenantFixture,
  USER,
  type Routes,
} from './test-helpers'
import type { CreditPool } from './types'

const { apiGet, apiPost, tenantGet, toast, setDialog, refresh } = vi.hoisted(
  () => ({
    apiGet: vi.fn(),
    apiPost: vi.fn(),
    tenantGet: vi.fn(),
    toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
    setDialog: vi.fn(),
    refresh: vi.fn(),
  })
)

vi.mock('sonner', () => ({ toast }))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: { get: apiGet, post: apiPost, put: vi.fn(), delete: vi.fn() },
    tenantApi: {
      get: tenantGet,
      post: vi.fn(),
      put: vi.fn(),
      delete: vi.fn(),
    },
  }
})

vi.mock('./components/tenants-provider', () => ({
  useTenants: () => ({ dialog: null, setDialog, refresh }),
}))

const TENANT = tenantFixture({ id: 't-acme', name: 'Acme' })
const POOL_URL = '/api/v2/admin/tenants/t-acme/credit-pool'
const USAGE_URL = `${POOL_URL}/usage`

// 4,000,000 of 5,000,000 units: $8.00 of $10.00 at 500,000 units per USD.
const FINITE_POOL: CreditPool = {
  id: 1,
  tenant_id: 't-acme',
  current_balance: 4_000_000,
  max_balance: 5_000_000,
  reset_period: 'monthly',
  alert_threshold_pct: 80,
}

const NO_POOL = new ApiError('Credit pool not found', {
  status: 404,
  code: 'POOL_NOT_FOUND',
})

function renderSheet(routes: Routes) {
  const live = serveGet(apiGet, tenantGet, routes)
  return { live, ...renderWithClient(<TenantPoolSheet tenant={TENANT} />) }
}

async function readyTopup() {
  const amount = await screen.findByTestId('topup-amount')
  await waitFor(() => expect(amount).toBeEnabled())
  return amount
}

const keyOf = (call: unknown[]) =>
  (call[2] as { headers: Record<string, string> }).headers['Idempotency-Key']

beforeEach(() => {
  vi.clearAllMocks()
  window.localStorage.clear()
  apiPost.mockResolvedValue({})
})

describe('CreditPoolSheet read states', () => {
  it('no pool (404): says so, offers the ceiling form, disables top-up, asks for no usage', async () => {
    renderSheet({ [POOL_URL]: NO_POOL })

    expect(await screen.findByTestId('ceiling-form')).toBeInTheDocument()
    expect(screen.getByText('No credit pool configured')).toBeInTheDocument()
    expect(screen.getByTestId('pool-health')).toHaveTextContent('No pool')
    expect(screen.getByTestId('topup-submit')).toBeDisabled()
    expect(apiGet.mock.calls.some(([u]) => u === USAGE_URL)).toBe(false)
  })

  it('a finite pool shows balance over ceiling in money, healthy, with top-up enabled', async () => {
    renderSheet({
      [POOL_URL]: FINITE_POOL,
      [USAGE_URL]: {
        draws: [
          {
            id: 9,
            created_at: '2026-10-01T08:30:00Z',
            direction: 1,
            amount: 250_000,
            reason: 'relay_debit',
          },
        ],
      },
    })

    await waitFor(() =>
      expect(screen.getByTestId('topup-submit')).toBeEnabled()
    )
    expect(screen.getByTestId('pool-health')).toHaveTextContent('Healthy')
    const summary = screen.getByTestId('pool-summary')
    expect(summary).toHaveTextContent('$8.00')
    expect(summary).toHaveTextContent('$10.00')
    expect(screen.queryByTestId('ceiling-form')).not.toBeInTheDocument()

    const row = await screen.findByTestId('pool-draw-9')
    expect(row).toHaveTextContent('Debit')
    expect(row).toHaveTextContent('$0.50')
    expect(row).toHaveTextContent('Relay usage')
    expect(apiGet).toHaveBeenCalledWith(USAGE_URL, { params: { limit: 20 } })
  })

  it.each([
    ['low', 500_000, 5_000_000, 'Low'],
    ['exhausted', 0, 5_000_000, 'Exhausted'],
  ])('a %s pool is labelled as such', async (_name, current, max, label) => {
    renderSheet({
      [POOL_URL]: { ...FINITE_POOL, current_balance: current, max_balance: max },
      [USAGE_URL]: { draws: [] },
    })
    expect(await screen.findByTestId('pool-health')).toHaveTextContent(label)
  })

  it('an unlimited pool says so instead of printing -1', async () => {
    renderSheet({
      [POOL_URL]: { ...FINITE_POOL, max_balance: -1 },
      [USAGE_URL]: { draws: [] },
    })
    expect(await screen.findByTestId('pool-health')).toHaveTextContent(
      'Unlimited'
    )
    expect(screen.getByTestId('pool-summary')).toHaveTextContent(
      '∞ (unlimited)'
    )
    expect(screen.getByTestId('pool-summary')).not.toHaveTextContent('-1')
  })

  it('a failed pool read is an error with retry, not "no pool" and not a ceiling form', async () => {
    const { live } = renderSheet({
      [POOL_URL]: new ApiError('boom', { status: 500 }),
    })
    expect(
      await screen.findByText('Could not load the credit pool')
    ).toBeInTheDocument()
    expect(screen.queryByTestId('ceiling-form')).not.toBeInTheDocument()
    expect(
      screen.queryByText('No credit pool configured')
    ).not.toBeInTheDocument()

    live[POOL_URL] = FINITE_POOL
    live[USAGE_URL] = { draws: [] }
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('pool-health')).toHaveTextContent(
      'Healthy'
    )
  })

  it('a failed usage read is a distinct message, not "No draws yet."', async () => {
    renderSheet({
      [POOL_URL]: FINITE_POOL,
      [USAGE_URL]: new ApiError('boom', { status: 500 }),
    })
    expect(await screen.findByTestId('usage-load-failed')).toBeInTheDocument()
    expect(screen.queryByText('No draws yet.')).not.toBeInTheDocument()
  })

  it('an empty usage list says no draws yet', async () => {
    renderSheet({ [POOL_URL]: FINITE_POOL, [USAGE_URL]: { draws: [] } })
    expect(await screen.findByText('No draws yet.')).toBeInTheDocument()
  })

  it('is a modal dialog named after the tenant', async () => {
    renderSheet({ [POOL_URL]: FINITE_POOL, [USAGE_URL]: { draws: [] } })
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveAccessibleName('Acme · credit pool')
  })
})

describe('CreditPoolSheet top-up', () => {
  const finite = () =>
    renderSheet({ [POOL_URL]: FINITE_POOL, [USAGE_URL]: { draws: [] } })

  it('sends the amount in quota units with an Idempotency-Key, then reloads', async () => {
    apiPost.mockResolvedValue({
      tenant_id: 't-acme',
      new_balance: 4_500_000,
      max_balance: 5_000_000,
    })
    finite()
    await userEvent.type(await readyTopup(), '1')
    await userEvent.type(screen.getByTestId('topup-reason'), 'promo')
    await userEvent.click(screen.getByTestId('topup-submit'))

    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1))
    const call = apiPost.mock.calls[0]
    expect(call[0]).toBe(`${POOL_URL}/topup`)
    expect(call[1]).toEqual({ amount: 500_000, reason: 'promo' })
    expect(keyOf(call)).toMatch(/^console-topup-/)

    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith(
        'Top-up successful. New balance: $9.00'
      )
    )
    // Cleared for the next intent, and the pool is read again.
    expect(screen.getByTestId('topup-amount')).toHaveValue('')
    await waitFor(() =>
      expect(apiGet.mock.calls.filter(([u]) => u === POOL_URL).length).toBe(2)
    )
  })

  it('omits an empty reason', async () => {
    apiPost.mockResolvedValue({ new_balance: 4_500_000 })
    finite()
    await userEvent.type(await readyTopup(), '1')
    await userEvent.click(screen.getByTestId('topup-submit'))
    await waitFor(() => expect(apiPost).toHaveBeenCalled())
    expect(apiPost.mock.calls[0][1].reason).toBeUndefined()
  })

  it('402 reads as an insufficient platform balance', async () => {
    apiPost.mockRejectedValue(
      new ApiError('debit failed', {
        status: 402,
        code: 'WALLET_DEBIT_FAILED',
      })
    )
    finite()
    await userEvent.type(await readyTopup(), '1')
    await userEvent.click(screen.getByTestId('topup-submit'))
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        'Wallet debit failed — insufficient platform balance'
      )
    )
  })

  it('409 pool-ceiling reads as exceeding the ceiling', async () => {
    apiPost.mockRejectedValue(
      new ApiError('over', { status: 409, code: 'POOL_CEILING_EXCEEDED' })
    )
    finite()
    await userEvent.type(await readyTopup(), '1')
    await userEvent.click(screen.getByTestId('topup-submit'))
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        'Top-up would exceed the pool ceiling'
      )
    )
  })

  it('any other refusal shows the server’s own message', async () => {
    apiPost.mockRejectedValue(
      new ApiError('Actor has no platform wallet (lurus_account_id unset)', {
        status: 412,
      })
    )
    finite()
    await userEvent.type(await readyTopup(), '1')
    await userEvent.click(screen.getByTestId('topup-submit'))
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        'Actor has no platform wallet (lurus_account_id unset)'
      )
    )
  })

  it.each(['0', 'abc', '-5', ''])(
    'blocks the amount %j without calling the server',
    async (value) => {
      finite()
      const amount = await readyTopup()
      if (value) await userEvent.type(amount, value)
      await userEvent.click(screen.getByTestId('topup-submit'))
      expect(apiPost).not.toHaveBeenCalled()
      expect(await screen.findByRole('alert')).toBeInTheDocument()
    }
  )

  it('a replayed top-up says it was already applied', async () => {
    apiPost.mockResolvedValue({ new_balance: 4_500_000, replayed: true })
    finite()
    await userEvent.type(await readyTopup(), '1')
    await userEvent.click(screen.getByTestId('topup-submit'))
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith(
        'This top-up was already applied. Balance: $9.00'
      )
    )
  })

  it('a retry after an unanswered request reuses the key, so the wallet is debited once', async () => {
    apiPost
      .mockRejectedValueOnce(new ApiError('gateway timeout', { status: 504 }))
      .mockResolvedValueOnce({ new_balance: 4_500_000 })
    finite()
    await userEvent.type(await readyTopup(), '1')
    await userEvent.click(screen.getByTestId('topup-submit'))
    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    await userEvent.click(screen.getByTestId('topup-submit'))
    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(2))
    expect(keyOf(apiPost.mock.calls[1])).toBe(keyOf(apiPost.mock.calls[0]))
  })

  it('a changed amount after an unanswered request is a new intent with a new key', async () => {
    apiPost
      .mockRejectedValueOnce(new ApiError('gateway timeout', { status: 504 }))
      .mockResolvedValueOnce({ new_balance: 4_500_000 })
    finite()
    const amount = await readyTopup()
    await userEvent.type(amount, '1')
    await userEvent.click(screen.getByTestId('topup-submit'))
    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    await userEvent.clear(amount)
    await userEvent.type(amount, '2')
    await userEvent.click(screen.getByTestId('topup-submit'))
    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(2))
    expect(keyOf(apiPost.mock.calls[1])).not.toBe(keyOf(apiPost.mock.calls[0]))
  })

  it('a definitive refusal spends the key: the next attempt gets a fresh one', async () => {
    apiPost
      .mockRejectedValueOnce(
        new ApiError('over', { status: 409, code: 'POOL_CEILING_EXCEEDED' })
      )
      .mockResolvedValueOnce({ new_balance: 4_500_000 })
    finite()
    await userEvent.type(await readyTopup(), '1')
    await userEvent.click(screen.getByTestId('topup-submit'))
    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    await userEvent.click(screen.getByTestId('topup-submit'))
    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(2))
    expect(keyOf(apiPost.mock.calls[1])).not.toBe(keyOf(apiPost.mock.calls[0]))
  })

  it('a top-up in flight refuses Escape, then allows it once settled', async () => {
    let settle: (value: unknown) => void = () => undefined
    apiPost.mockImplementationOnce(
      () => new Promise((resolve) => (settle = resolve))
    )
    finite()
    await userEvent.type(await readyTopup(), '1')
    await userEvent.click(screen.getByTestId('topup-submit'))
    await waitFor(() => expect(apiPost).toHaveBeenCalled())

    await userEvent.keyboard('{Escape}')
    expect(setDialog).not.toHaveBeenCalled()

    settle({ new_balance: 4_500_000 })
    await waitFor(() => expect(toast.success).toHaveBeenCalled())
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(setDialog).toHaveBeenCalledWith(null))
  })
})

describe('CreditPoolSheet create pool', () => {
  async function readyCeiling() {
    const max = await screen.findByTestId('ceiling-max-balance')
    await waitFor(() => expect(max).toBeEnabled())
    return max
  }

  it('creates a pool with the ceiling in quota units, then reloads', async () => {
    const { live } = renderSheet({ [POOL_URL]: NO_POOL })
    apiPost.mockImplementation(() => {
      live[POOL_URL] = FINITE_POOL
      live[USAGE_URL] = { draws: [] }
      return Promise.resolve(FINITE_POOL)
    })
    await userEvent.type(await readyCeiling(), '10')
    await userEvent.click(screen.getByTestId('ceiling-submit'))

    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1))
    expect(apiPost).toHaveBeenCalledWith(POOL_URL, {
      max_balance: 5_000_000,
      reset_period: 'monthly',
      alert_threshold_pct: 80,
    })
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Credit pool created')
    )
    // The pool now exists, so the ceiling form gives way to the summary.
    await waitFor(() =>
      expect(screen.queryByTestId('ceiling-form')).not.toBeInTheDocument()
    )
    expect(screen.getByTestId('pool-health')).toHaveTextContent('Healthy')
  })

  it('sends the reset period and alert threshold that were chosen', async () => {
    renderSheet({ [POOL_URL]: NO_POOL })
    await userEvent.type(await readyCeiling(), '2')
    await userEvent.selectOptions(
      screen.getByTestId('ceiling-reset-period'),
      'weekly'
    )
    const pct = screen.getByTestId('ceiling-alert-pct')
    await userEvent.clear(pct)
    await userEvent.type(pct, '90')
    await userEvent.click(screen.getByTestId('ceiling-submit'))
    await waitFor(() => expect(apiPost).toHaveBeenCalled())
    expect(apiPost.mock.calls[0][1]).toEqual({
      max_balance: 1_000_000,
      reset_period: 'weekly',
      alert_threshold_pct: 90,
    })
  })

  it('an unlimited ceiling is sent as -1, never typed as a number', async () => {
    renderSheet({ [POOL_URL]: NO_POOL })
    await readyCeiling()
    await userEvent.click(
      screen.getByRole('switch', { name: 'Unlimited ceiling' })
    )
    expect(screen.queryByTestId('ceiling-max-balance')).not.toBeInTheDocument()
    await userEvent.click(screen.getByTestId('ceiling-submit'))
    await waitFor(() => expect(apiPost).toHaveBeenCalled())
    expect(apiPost.mock.calls[0][1].max_balance).toBe(-1)
  })

  it.each(['', '-5', 'abc'])(
    'blocks the ceiling %j without calling the server',
    async (value) => {
      renderSheet({ [POOL_URL]: NO_POOL })
      const max = await readyCeiling()
      if (value) await userEvent.type(max, value)
      await userEvent.click(screen.getByTestId('ceiling-submit'))
      expect(apiPost).not.toHaveBeenCalled()
      expect(await screen.findByRole('alert')).toBeInTheDocument()
    }
  )

  it('blocks an alert threshold outside 1..100', async () => {
    renderSheet({ [POOL_URL]: NO_POOL })
    await userEvent.type(await readyCeiling(), '2')
    const pct = screen.getByTestId('ceiling-alert-pct')
    await userEvent.clear(pct)
    await userEvent.type(pct, '101')
    await userEvent.click(screen.getByTestId('ceiling-submit'))
    expect(apiPost).not.toHaveBeenCalled()
    expect(
      await screen.findByText('Enter a whole number from 1 to 100.')
    ).toBeInTheDocument()
  })

  it('a pool that already exists is said so and read again', async () => {
    apiPost.mockRejectedValue(
      new ApiError('exists', { status: 409, code: 'POOL_ALREADY_EXISTS' })
    )
    const { live } = renderSheet({ [POOL_URL]: NO_POOL })
    await userEvent.type(await readyCeiling(), '10')
    live[POOL_URL] = FINITE_POOL
    live[USAGE_URL] = { draws: [] }
    await userEvent.click(screen.getByTestId('ceiling-submit'))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        'A credit pool already exists; use top-up instead.'
      )
    )
    await waitFor(() =>
      expect(screen.queryByTestId('ceiling-form')).not.toBeInTheDocument()
    )
  })

  it('any other failure shows the server’s message', async () => {
    apiPost.mockRejectedValue(new ApiError('Failed to create pool', { status: 500 }))
    renderSheet({ [POOL_URL]: NO_POOL })
    await userEvent.type(await readyCeiling(), '10')
    await userEvent.click(screen.getByTestId('ceiling-submit'))
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Failed to create pool')
    )
  })
})

describe('CreditPoolSheet drafts', () => {
  const seed = (name: string, value: unknown) =>
    window.localStorage.setItem(
      draftKey(USER.id, `${name}:t-acme`),
      JSON.stringify({ v: DRAFT_SCHEMA_VERSION, d: value, t: Date.now() })
    )

  it('restores a half-typed top-up, says so, and lets it be discarded', async () => {
    seed('credit-pool-topup', { amount: '3', reason: 'left over' })
    renderSheet({ [POOL_URL]: FINITE_POOL, [USAGE_URL]: { draws: [] } })

    expect(await screen.findByTestId('topup-restored-banner')).toBeInTheDocument()
    expect(screen.getByTestId('topup-amount')).toHaveValue('3')
    expect(screen.getByTestId('topup-reason')).toHaveValue('left over')

    await userEvent.click(screen.getByTestId('topup-discard-draft'))
    expect(screen.queryByTestId('topup-restored-banner')).not.toBeInTheDocument()
    expect(screen.getByTestId('topup-amount')).toHaveValue('')
  })

  it('restores a half-typed ceiling for the no-pool case', async () => {
    seed('credit-pool-ceiling', {
      unlimited: false,
      amount: '42',
      resetPeriod: 'daily',
      alertPct: '70',
    })
    renderSheet({ [POOL_URL]: NO_POOL })
    expect(
      await screen.findByTestId('ceiling-restored-banner')
    ).toBeInTheDocument()
    expect(screen.getByTestId('ceiling-max-balance')).toHaveValue('42')
    expect(screen.getByTestId('ceiling-reset-period')).toHaveValue('daily')
    expect(screen.getByTestId('ceiling-alert-pct')).toHaveValue('70')
  })

  it('keeps each tenant’s draft in its own slot', async () => {
    window.localStorage.setItem(
      draftKey(USER.id, 'credit-pool-topup:someone-else'),
      JSON.stringify({
        v: DRAFT_SCHEMA_VERSION,
        d: { amount: '99', reason: '' },
        t: Date.now(),
      })
    )
    renderSheet({ [POOL_URL]: FINITE_POOL, [USAGE_URL]: { draws: [] } })
    await readyTopup()
    expect(
      screen.queryByTestId('topup-restored-banner')
    ).not.toBeInTheDocument()
    expect(screen.getByTestId('topup-amount')).toHaveValue('')
  })

  it('saves what is typed, under this person and this tenant', async () => {
    renderSheet({ [POOL_URL]: FINITE_POOL, [USAGE_URL]: { draws: [] } })
    await userEvent.type(await readyTopup(), '7')
    const key = draftKey(USER.id, 'credit-pool-topup:t-acme')
    await waitFor(() =>
      expect(JSON.parse(window.localStorage.getItem(key) ?? 'null')?.d).toEqual({
        amount: '7',
        reason: '',
      })
    )
  })
})

describe('CreditPoolSheet closing', () => {
  it('Escape closes the sheet when nothing is in flight', async () => {
    renderSheet({ [POOL_URL]: FINITE_POOL, [USAGE_URL]: { draws: [] } })
    await readyTopup()
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(setDialog).toHaveBeenCalledWith(null))
    expect(setDialog).toHaveBeenCalledTimes(1)
  })
})
