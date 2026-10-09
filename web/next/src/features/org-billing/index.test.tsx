import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api-error'

import * as api from './api'
import { OrgBillingPage } from './index'
import { redeemErrorMessage, REDEEM_CODE_LENGTH } from './lib/redeem'
import {
  findMismatches,
  formatCny4,
  normalizeInvoices,
  normalizeStatement,
  normalizeSummary,
  statementQuery,
  currentMonth,
  type StatementData,
} from './lib/statement'

const toastError = vi.hoisted(() => vi.fn())
vi.mock('sonner', () => ({ toast: { error: toastError, success: vi.fn() } }))
vi.mock('./api', () => ({
  fetchStatement: vi.fn(),
  fetchInvoices: vi.fn(),
  fetchWallet: vi.fn(),
  redeemCode: vi.fn(),
  downloadStatementCsv: vi.fn(),
}))
vi.mock('@/lib/status', () => ({
  useMoneyConfig: () => ({
    quotaPerUnit: 500000,
    displayType: 'USD',
    symbol: '$',
    rate: 1,
  }),
}))
vi.mock('@/lib/user', () => ({
  currentUserQueryOptions: {
    queryKey: ['current-user'],
    queryFn: () => Promise.resolve({ role: 1, remaining_quota: 1_000_000 }),
  },
}))

const row = (key: string, n: number) => ({
  key,
  label: `name-${key}`,
  requests: n,
  quota: n * 500000,
  charged_cny4: n * 10000,
  priced_cny4: 0,
})

const statement = (over: Partial<StatementData> = {}): StatementData => ({
  month: '2026-10',
  group_by: 'project',
  rows: [row('1', 1), row('2', 2)],
  totals: row('*', 3),
  ...over,
})

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <OrgBillingPage />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchWallet).mockResolvedValue({
    balance: 12.5,
    frozen: 0,
    available: 12.5,
  })
  vi.mocked(api.fetchInvoices).mockResolvedValue([])
  vi.mocked(api.fetchStatement).mockResolvedValue(statement())
})

describe('statement mapping', () => {
  it('tolerates null rows and missing numbers', () => {
    const d = normalizeStatement({ rows: null, totals: { requests: 'x' } })
    expect(d.rows).toEqual([])
    expect(d.totals.requests).toBe(0)
  })

  it('detects totals that differ from the row sum', () => {
    expect(findMismatches(statement())).toEqual([])
    const bad = statement({ totals: { ...row('*', 3), quota: 1 } })
    expect(findMismatches(bad).map((m) => m.field)).toEqual(['quota'])
  })

  it('formats 0.0001 CNY units', () => {
    expect(formatCny4(123456)).toBe('¥12.3456')
    expect(formatCny4(undefined)).toBe('--')
  })

  it('builds the query and maps invoices / wallet', () => {
    expect(statementQuery('2026-10', 'model', 'csv')).toBe(
      'month=2026-10&tz=Asia%2FShanghai&group_by=model&format=csv'
    )
    expect(normalizeInvoices({ items: [{ month: '2026-09', estimated: true }] }))
      .toMatchObject([{ month: '2026-09', estimated: true, quota: 0 }])
    expect(normalizeInvoices(null)).toEqual([])
    expect(normalizeSummary({ balance: 3 }).balance).toBe(3)
    expect(statementQuery('2026-10', 'project')).toContain('tz=Asia%2FShanghai')
  })

  it('current month is computed in Asia/Shanghai across month boundaries', () => {
    // 2026-09-30 17:00 UTC is already 2026-10-01 01:00 in Shanghai
    expect(currentMonth(new Date('2026-09-30T17:00:00Z'))).toBe('2026-10')
    // 2026-09-30 15:59 UTC is still 2026-09-30 23:59 in Shanghai
    expect(currentMonth(new Date('2026-09-30T15:59:00Z'))).toBe('2026-09')
    // year rollover
    expect(currentMonth(new Date('2026-12-31T16:00:00Z'))).toBe('2027-01')
  })
})

describe('redeemErrorMessage', () => {
  const t = ((k: string) => k) as never
  it('maps coded failures, then message, then generic', () => {
    expect(
      redeemErrorMessage(new ApiError('中文', { code: 'REDEMPTION_USED' }), t)
    ).toBe('That redemption code has already been used.')
    expect(redeemErrorMessage(new ApiError('rate limited'), t)).toBe(
      'rate limited'
    )
    expect(redeemErrorMessage('boom', t)).toBe('Redemption failed')
  })
})

describe('OrgBillingPage', () => {
  it('shows balances and a statement whose total matches', async () => {
    renderPage()
    expect(await screen.findAllByTestId('statement-row')).toHaveLength(2)
    expect(screen.getByTestId('wallet-balance')).toHaveTextContent('¥12.50')
    await waitFor(() =>
      expect(screen.getByTestId('remaining-quota')).toHaveTextContent('$2.00')
    )
    expect(screen.getByTestId('statement-total')).toHaveTextContent('3')
    expect(screen.queryByTestId('statement-mismatch')).toBeNull()
  })

  it('warns, and keeps the rows, when totals disagree', async () => {
    vi.mocked(api.fetchStatement).mockResolvedValue(
      statement({ totals: { ...row('*', 3), requests: 99 } })
    )
    renderPage()
    expect(await screen.findByTestId('statement-mismatch')).toBeInTheDocument()
    expect(screen.getAllByTestId('statement-row')).toHaveLength(2)
  })

  it('shows an error, not an empty state, when the statement fails', async () => {
    vi.mocked(api.fetchStatement).mockRejectedValue(new ApiError('Admin role required'))
    renderPage()
    expect(await screen.findByText('Admin role required')).toBeInTheDocument()
    expect(screen.queryByText('No billable usage in this month')).toBeNull()
  })

  it('shows a wallet failure without hiding the rest', async () => {
    vi.mocked(api.fetchWallet).mockRejectedValue(new ApiError('platform billing unavailable'))
    renderPage()
    expect(await screen.findByText(/platform billing unavailable/)).toBeInTheDocument()
    expect(await screen.findAllByTestId('statement-row')).toHaveLength(2)
  })

  it('refetches with the chosen group', async () => {
    renderPage()
    await screen.findAllByTestId('statement-row')
    fireEvent.change(screen.getByLabelText('Group by'), {
      target: { value: 'model' },
    })
    await waitFor(() =>
      expect(api.fetchStatement).toHaveBeenLastCalledWith(
        expect.stringMatching(/^\d{4}-\d{2}$/),
        'model'
      )
    )
  })

  it('renders invoices', async () => {
    vi.mocked(api.fetchInvoices).mockResolvedValue(
      normalizeInvoices({
        items: [{ month: '2026-09', amount_cny: 5, estimated: true }],
      })
    )
    renderPage()
    expect(await screen.findByTestId('invoice-row')).toHaveTextContent('¥5.00')
    expect(screen.getByText('Estimated')).toBeInTheDocument()
  })

  it('redeems only a full-length code and toasts the coded failure', async () => {
    vi.mocked(api.redeemCode).mockRejectedValue(
      new ApiError('x', { code: 'REDEMPTION_EXPIRED' })
    )
    renderPage()
    const input = screen.getByLabelText('Redemption code')
    const button = screen.getByRole('button', { name: 'Redeem' })
    fireEvent.change(input, { target: { value: 'short' } })
    expect(button).toBeDisabled()
    fireEvent.change(input, { target: { value: 'a'.repeat(REDEEM_CODE_LENGTH) } })
    expect(button).toBeEnabled()
    fireEvent.click(button)
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('That redemption code has expired.')
    )
  })

  it('shows added quota after a successful redeem and re-reads the wallet', async () => {
    vi.mocked(api.redeemCode).mockResolvedValue({ quota_added: 500000 })
    renderPage()
    await screen.findAllByTestId('statement-row')
    fireEvent.change(screen.getByLabelText('Redemption code'), {
      target: { value: 'b'.repeat(REDEEM_CODE_LENGTH) },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Redeem' }))
    expect(await screen.findByRole('status')).toHaveTextContent('$1.00')
    await waitFor(() => expect(api.fetchWallet).toHaveBeenCalledTimes(2))
  })

  it('exports csv for the selected month and group, surfacing failures', async () => {
    vi.mocked(api.downloadStatementCsv).mockRejectedValue(new Error('Admin role required'))
    renderPage()
    await screen.findAllByTestId('statement-row')
    fireEvent.click(screen.getByRole('button', { name: /Export CSV/ }))
    await waitFor(() =>
      expect(api.downloadStatementCsv).toHaveBeenCalledWith(
        expect.stringMatching(/^\d{4}-\d{2}$/),
        'project'
      )
    )
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('Export failed: Admin role required')
    )
  })
})
