import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api-error'

import * as redeemApi from '../org-billing/api'
import * as api from './api'
import { WalletPage } from './index'
import {
  IntentKey,
  amountError,
  checkoutErrorMessage,
  orderPhase,
  parseAmount,
} from './lib/checkout'
import { formatCny, formatSignedCny } from './lib/format'
import * as navigate from './lib/navigate'
import { withReturnTo } from './lib/return-to'

const toast = vi.hoisted(() => ({ error: vi.fn(), success: vi.fn() }))
vi.mock('sonner', () => ({ toast }))

const MONEY = vi.hoisted(() => ({
  quotaPerUnit: 500000,
  displayType: 'USD' as const,
  symbol: '$',
  rate: 1,
}))
vi.mock('@/lib/status', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/status')>()),
  useMoneyConfig: () => MONEY,
}))
vi.mock('@/lib/user', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/user')>()),
  currentUserQueryOptions: {
    queryKey: ['current-user'],
    queryFn: () =>
      Promise.resolve({
        role: 1,
        remaining_quota: 3_000_000,
        used_quota: 1_000_000,
        request_count: 12,
      }),
  },
}))
vi.mock('../org-billing/api', () => ({ redeemCode: vi.fn() }))
vi.mock('./lib/navigate', () => ({ goToPayment: vi.fn() }))
vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  fetchWalletInfo: vi.fn(),
  fetchPaymentMethods: vi.fn(),
  createCheckout: vi.fn(),
  fetchCheckoutStatus: vi.fn(),
  fetchTopups: vi.fn(),
  fetchWalletTransactions: vi.fn(),
}))
vi.mock('qrcode.react', () => ({
  QRCodeSVG: (p: { value: string }) => (
    <svg data-testid='qr' data-value={p.value} />
  ),
}))

const platformWallet = {
  source: 'platform' as const,
  balance: 123.45,
  frozen: 3,
  available: 120.45,
  lifetime_topup: 500,
  lifetime_spend: 376.55,
  pending_orders: 0,
  topup_url: 'https://id.example.com/topup',
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <WalletPage />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.mocked(api.fetchWalletInfo).mockResolvedValue(platformWallet)
  vi.mocked(api.fetchPaymentMethods).mockResolvedValue([
    {
      id: 'pay-redirect',
      name: 'Pay Redirect',
      provider: 'p1',
      type: 'redirect',
    },
    { id: 'pay-qr', name: 'Pay QR', provider: 'p2', type: 'qr' },
  ])
  vi.mocked(api.fetchTopups).mockResolvedValue({
    items: [
      {
        id: 1,
        content: 'Redeemed code ****1',
        quota: 1_000_000,
        created_at: 1_700_000_000,
      },
    ],
    total: 1,
    page: 1,
    page_size: 10,
  })
  vi.mocked(api.fetchWalletTransactions).mockResolvedValue({
    items: [
      {
        id: 9,
        type: 'topup',
        amount: 50,
        balance_after: 123.45,
        description: 'Wallet top-up',
        reference_id: 'ORD1',
        created_at: '2026-10-01T08:30:00Z',
      },
      {
        id: 8,
        type: 'debit',
        amount: -2.5,
        balance_after: 73.45,
        description: 'Usage',
        reference_id: '',
        created_at: '2026-09-30T08:30:00Z',
      },
    ],
    total: 2,
    page: 1,
    page_size: 10,
  })
})

describe('withReturnTo', () => {
  it("adds return_to and keeps the platform's own query", () => {
    const out = new URL(
      withReturnTo(
        'https://id.example.com/topup?amount=50',
        'https://hub.example.com/wallet'
      )
    )
    expect(out.searchParams.get('amount')).toBe('50')
    expect(out.searchParams.get('return_to')).toBe(
      'https://hub.example.com/wallet'
    )
  })

  it('keeps a return_to the platform already set', () => {
    const out = new URL(
      withReturnTo(
        'https://id.example.com/topup?return_to=x',
        'https://hub.example.com/wallet'
      )
    )
    expect(out.searchParams.get('return_to')).toBe('x')
  })
})

describe('balances', () => {
  it('shows the wallet in CNY and the usage from the money config', async () => {
    renderPage()
    expect(await screen.findByText('¥123.45')).toBeInTheDocument()
    expect(screen.getByText('Available ¥120.45')).toBeInTheDocument()
    expect(screen.getByText('¥3.00')).toBeInTheDocument()
    // 1,000,000 quota at 500,000 per unit = $2.00
    expect(
      within(screen.getByTestId('stat-usage')).getByText('$2.00')
    ).toBeInTheDocument()
    const href = screen
      .getByRole('link', { name: /Open platform wallet/ })
      .getAttribute('href')
    const link = new URL(href ?? '')
    expect(link.origin + link.pathname).toBe('https://id.example.com/topup')
    expect(link.searchParams.get('return_to')).toBe(window.location.href)
  })

  it('reports an unavailable wallet instead of a made-up balance', async () => {
    vi.mocked(api.fetchWalletInfo).mockRejectedValue(
      new ApiError('platform billing unavailable', { status: 200 })
    )
    renderPage()
    expect(
      await screen.findByText(/platform billing unavailable/)
    ).toBeInTheDocument()
    expect(screen.queryByText('¥0.00')).not.toBeInTheDocument()
    expect(screen.getByTestId('stat-wallet')).toHaveTextContent('--')
  })

  it('explains an unlinked sign-in and offers no online payment', async () => {
    vi.mocked(api.fetchWalletInfo).mockResolvedValue({
      ...platformWallet,
      source: 'internal',
      topup_url: undefined,
    })
    renderPage()
    expect(
      await screen.findByText(/not linked to a platform wallet/)
    ).toBeInTheDocument()
    expect(
      screen.getByText(/needs a platform-linked account/)
    ).toBeInTheDocument()
    expect(api.fetchPaymentMethods).not.toHaveBeenCalled()
    expect(
      screen.queryByRole('tab', { name: 'Wallet transactions' })
    ).toBeNull()
  })

  it('shows an unlinked balance in the console currency, never CNY next to USD', async () => {
    vi.mocked(api.fetchWalletInfo).mockResolvedValue({
      ...platformWallet,
      source: 'internal',
      topup_url: undefined,
    })
    renderPage()
    await screen.findByText(/not linked to a platform wallet/)
    const balance = screen.getByTestId('stat-wallet')
    // 3,000,000 quota at 500,000 per unit, the same figure the dashboard shows.
    await waitFor(() => expect(balance).toHaveTextContent('$6.00'))
    expect(balance).not.toHaveTextContent('¥')
    // No platform wallet, so nothing can be frozen.
    expect(screen.queryByTestId('stat-frozen')).toBeNull()
  })
})

describe('top-up', () => {
  async function pick(amount: string, method: string) {
    await userEvent.click(await screen.findByRole('button', { name: amount }))
    await userEvent.click(
      await screen.findByRole('radio', { name: new RegExp(method) })
    )
  }

  it('opens one order and sends the browser to the pay page', async () => {
    vi.mocked(api.createCheckout).mockResolvedValue({
      order_no: 'ORD1',
      pay_url: 'https://pay.example.com/ORD1',
      status: 'pending',
      expires_at: null,
    })
    renderPage()
    await pick('¥100', 'Pay Redirect')
    await userEvent.click(screen.getByRole('button', { name: 'Pay ¥100.00' }))
    await waitFor(() => expect(api.createCheckout).toHaveBeenCalledTimes(1))
    const [amount, method, key] = vi.mocked(api.createCheckout).mock.calls[0]
    expect([amount, method]).toEqual([100, 'pay-redirect'])
    expect(key).toMatch(/\S{8,}/)
    await waitFor(() =>
      expect(navigate.goToPayment).toHaveBeenCalledWith(
        'https://pay.example.com/ORD1'
      )
    )
  })

  it('shows a scan code for QR methods and reflects the paid order', async () => {
    vi.mocked(api.createCheckout).mockResolvedValue({
      order_no: 'ORD2',
      pay_url: 'https://pay.example.com/qr/ORD2',
      status: 'pending',
      expires_at: null,
    })
    vi.mocked(api.fetchCheckoutStatus).mockResolvedValue({
      order_no: 'ORD2',
      status: 'paid',
      amount_cny: 50,
      pay_url: 'https://pay.example.com/qr/ORD2',
      paid_at: '2026-10-01T08:30:00Z',
      expires_at: null,
    })
    renderPage()
    await pick('¥50', 'Pay QR')
    await userEvent.click(screen.getByRole('button', { name: 'Pay ¥50.00' }))
    expect(await screen.findByText('Payment received')).toBeInTheDocument()
    expect(navigate.goToPayment).not.toHaveBeenCalled()
    expect(api.fetchCheckoutStatus).toHaveBeenCalledWith('ORD2')
  })

  it('keeps showing the code while the order is still pending', async () => {
    vi.mocked(api.createCheckout).mockResolvedValue({
      order_no: 'ORD3',
      pay_url: 'https://pay.example.com/qr/ORD3',
      status: 'pending',
      expires_at: null,
    })
    vi.mocked(api.fetchCheckoutStatus).mockResolvedValue({
      order_no: 'ORD3',
      status: 'pending',
      amount_cny: 10,
      pay_url: 'https://pay.example.com/qr/ORD3',
      paid_at: null,
      expires_at: null,
    })
    renderPage()
    await pick('¥10', 'Pay QR')
    await userEvent.click(screen.getByRole('button', { name: 'Pay ¥10.00' }))
    expect(await screen.findByTestId('qr')).toHaveAttribute(
      'data-value',
      'https://pay.example.com/qr/ORD3'
    )
    expect(screen.getByText('Waiting for payment...')).toBeInTheDocument()
    const closers = screen.getAllByRole('button', { name: 'Close' })
    await userEvent.click(closers.at(-1) as HTMLElement)
    await waitFor(() => expect(screen.queryByTestId('qr')).toBeNull())
  })

  it('shows the platform message when it rejects the order', async () => {
    vi.mocked(api.createCheckout).mockRejectedValue(
      new ApiError('payment method not configured', { status: 400 })
    )
    renderPage()
    await pick('¥10', 'Pay Redirect')
    await userEvent.click(screen.getByRole('button', { name: 'Pay ¥10.00' }))
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('payment method not configured')
    )
    expect(navigate.goToPayment).not.toHaveBeenCalled()
  })

  it('refuses an amount out of range before any request', async () => {
    renderPage()
    const input = await screen.findByLabelText('Custom Amount')
    await userEvent.type(input, '999999')
    expect(
      screen.getByText('Enter an amount between 1 and 100000 CNY.')
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Pay' })).toBeDisabled()
    expect(api.createCheckout).not.toHaveBeenCalled()
  })

  it('tells the person when no payment method is configured', async () => {
    vi.mocked(api.fetchPaymentMethods).mockResolvedValue([])
    renderPage()
    expect(
      await screen.findByText(/No payment method is configured yet/)
    ).toBeInTheDocument()
  })

  it('lets a failed method list be retried', async () => {
    vi.mocked(api.fetchPaymentMethods).mockRejectedValueOnce(
      new ApiError('down', { status: 503 })
    )
    renderPage()
    expect(
      await screen.findByText('Payment methods could not be loaded.')
    ).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(
      await screen.findByRole('radio', { name: /Pay QR/ })
    ).toBeInTheDocument()
  })
})

describe('redeem', () => {
  it('credits the code and reports the quota added', async () => {
    vi.mocked(redeemApi.redeemCode).mockResolvedValue({ quota_added: 500_000 })
    renderPage()
    const code = 'a'.repeat(32)
    await userEvent.type(await screen.findByLabelText('Redemption code'), code)
    await userEvent.click(screen.getByRole('button', { name: 'Redeem' }))
    await waitFor(() => expect(redeemApi.redeemCode).toHaveBeenCalledWith(code))
    expect(await screen.findByText(/Redeemed. Quota added/)).toHaveTextContent(
      '$1.00'
    )
  })

  it('maps a rejected code to its sentence', async () => {
    vi.mocked(redeemApi.redeemCode).mockRejectedValue(
      new ApiError('x', { status: 400, code: 'REDEMPTION_USED' })
    )
    renderPage()
    await userEvent.type(
      await screen.findByLabelText('Redemption code'),
      'b'.repeat(32)
    )
    await userEvent.click(screen.getByRole('button', { name: 'Redeem' }))
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        'That redemption code has already been used.'
      )
    )
    expect(screen.queryByText(/Redeemed. Quota added/)).toBeNull()
  })

  it('does not enable Redeem until the code has the full length', async () => {
    renderPage()
    await userEvent.type(
      await screen.findByLabelText('Redemption code'),
      'short'
    )
    expect(screen.getByRole('button', { name: 'Redeem' })).toBeDisabled()
  })
})

describe('history', () => {
  it('lists redemption credits with the quota in display money', async () => {
    renderPage()
    const row = (await screen.findByText('Redeemed code ****1')).closest('tr')
    expect(row).not.toBeNull()
    expect(within(row as HTMLElement).getByText('$2.00')).toBeInTheDocument()
  })

  it('switches to the wallet ledger and signs the amounts', async () => {
    renderPage()
    await userEvent.click(
      await screen.findByRole('tab', { name: 'Wallet transactions' })
    )
    expect(await screen.findByText('+¥50.00')).toBeInTheDocument()
    expect(screen.getByText('-¥2.50')).toBeInTheDocument()
    expect(api.fetchWalletTransactions).toHaveBeenCalledWith(1, 10)
  })

  it('pages through the redemption list', async () => {
    vi.mocked(api.fetchTopups).mockResolvedValue({
      items: [{ id: 1, content: 'row', quota: 0, created_at: 1_700_000_000 }],
      total: 25,
      page: 1,
      page_size: 10,
    })
    renderPage()
    await screen.findByText('row')
    await userEvent.click(screen.getByRole('button', { name: 'Next page' }))
    await waitFor(() => expect(api.fetchTopups).toHaveBeenCalledWith(2, 10))
  })

  it('shows the failure and recovers on retry', async () => {
    vi.mocked(api.fetchTopups).mockRejectedValueOnce(
      new ApiError('history down', { status: 500 })
    )
    renderPage()
    expect(await screen.findByText('history down')).toBeInTheDocument()
    const retry = screen.getAllByRole('button', { name: 'Retry' })
    await userEvent.click(retry.at(-1) as HTMLElement)
    expect(await screen.findByText('Redeemed code ****1')).toBeInTheDocument()
  })

  it('shows an empty state when there is nothing yet', async () => {
    vi.mocked(api.fetchTopups).mockResolvedValue({
      items: [],
      total: 0,
      page: 1,
      page_size: 10,
    })
    renderPage()
    expect(await screen.findByText('No top-ups yet')).toBeInTheDocument()
  })
})

describe('lib', () => {
  const t = ((k: string) => k) as never

  it('formats CNY with sign and refuses non-numbers', () => {
    expect(formatCny(1234.5)).toBe('¥1234.50')
    expect(formatCny(-2.5)).toBe('-¥2.50')
    expect(formatSignedCny(2)).toBe('+¥2.00')
    expect(formatCny(null)).toBe('--')
    expect(formatCny(Number.NaN)).toBe('--')
  })

  it('parses and bounds the typed amount', () => {
    expect(parseAmount('')).toBeNull()
    expect(parseAmount('abc')).toBeNull()
    expect(parseAmount('0.1')).toBe(0.1)
    expect(amountError(null, t)).not.toBeNull()
    expect(amountError(0.5, t)).not.toBeNull()
    expect(amountError(100001, t)).not.toBeNull()
    expect(amountError(1, t)).toBeNull()
    expect(amountError(100000, t)).toBeNull()
  })

  it('classifies order statuses and leaves unknown ones pending', () => {
    expect(orderPhase('PAID')).toBe('paid')
    expect(orderPhase('expired')).toBe('failed')
    expect(orderPhase('pending')).toBe('pending')
    expect(orderPhase('something-new')).toBe('pending')
    expect(orderPhase(undefined)).toBe('pending')
  })

  it('reuses the idempotency key for one intent and re-mints after reset', () => {
    const keys = new IntentKey()
    const a = keys.keyFor(10, 'm')
    expect(keys.keyFor(10, 'm')).toBe(a)
    expect(keys.keyFor(20, 'm')).not.toBe(a)
    const b = keys.keyFor(10, 'm')
    keys.reset()
    expect(keys.keyFor(10, 'm')).not.toBe(b)
  })

  it('explains unlinked and outage failures', () => {
    expect(
      checkoutErrorMessage(
        new ApiError('platform account not linked', { status: 503 }),
        t
      )
    ).toMatch(/not linked/)
    expect(
      checkoutErrorMessage(new ApiError('boom', { status: 503 }), t)
    ).toMatch(/unavailable/)
    expect(checkoutErrorMessage(new Error('x'), t)).toMatch(/Failed to create/)
  })
})
