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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api-error'

import * as auditApi from './audit-api'
import { AuditLogPage } from './audit'
import type { AuditPage, ChainReport } from './lib/audit'

vi.mock('./audit-api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./audit-api')>()
  return {
    ...actual,
    auditQueryOptions: vi.fn(),
    exportAuditCsv: vi.fn(),
    verifyAuditChain: vi.fn(),
  }
})
vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))

let currentUser: Record<string, unknown> = { role: 1, tenant_role: 'admin' }
vi.mock('@/lib/user', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/user')>()
  return {
    ...actual,
    currentUserQueryOptions: {
      queryKey: ['current-user'],
      queryFn: () => Promise.resolve(currentUser),
    },
  }
})

const event = (over: Record<string, unknown> = {}) => ({
  id: 1,
  tenantId: 'default',
  timestamp: 1_800_000_000,
  actorType: 'user',
  actorId: 5,
  action: 'token.created',
  resource: 'token',
  resourceId: 9,
  ip: '10.0.0.1',
  requestId: 'req-1',
  details: JSON.stringify({ name: 'k1', channel_key: '[redacted]' }),
  rowHash: '',
  prevHash: '',
  ...over,
})

function mockFeed(pageOrError: AuditPage | Error) {
  vi.mocked(auditApi.auditQueryOptions).mockImplementation(
    (scope, filters, p) =>
      ({
        queryKey: ['t-audit', scope, filters, p],
        queryFn: () =>
          pageOrError instanceof Error
            ? Promise.reject(pageOrError)
            : Promise.resolve(pageOrError),
        retry: false,
      }) as never
  )
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <AuditLogPage />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  currentUser = { role: 1, tenant_role: 'admin' }
})

describe('AuditLogPage', () => {
  it('lists tenant events and opens the detail dialog', async () => {
    mockFeed({ items: [event()], total: 1 })
    renderPage()
    expect(await screen.findByText('token.created')).toBeInTheDocument()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('audit-row'))
    const dialog = await screen.findByTestId('audit-detail')
    expect(dialog).toHaveTextContent('k1')
    expect(dialog).toHaveTextContent('[redacted]')
  })

  it('a failed read is an error, never an empty list', async () => {
    mockFeed(new ApiError('audit down', { status: 500 }))
    renderPage()
    expect(await screen.findByText('audit down')).toBeInTheDocument()
    expect(screen.queryByText('No audit events match')).toBeNull()
  })

  it('shows the empty state for a successful empty read', async () => {
    mockFeed({ items: [], total: 0 })
    renderPage()
    expect(await screen.findByText('No audit events match')).toBeInTheDocument()
  })

  it('refuses a malformed operator filter without querying', async () => {
    mockFeed({ items: [event()], total: 1 })
    renderPage()
    await screen.findByText('token.created')
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('Operator (user id)'), 'abc')
    await user.click(screen.getByRole('button', { name: 'Search' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The operator must be a positive whole number (user id).'
    )
  })

  it('a failed export surfaces the server message', async () => {
    const { toast } = await import('sonner')
    mockFeed({ items: [event()], total: 1 })
    vi.mocked(auditApi.exportAuditCsv).mockRejectedValue(
      new ApiError('rate limited', { status: 429 })
    )
    renderPage()
    await screen.findByText('token.created')
    await userEvent.setup().click(
      screen.getByRole('button', { name: 'Export CSV' })
    )
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('rate limited'))
  })

  it('tenant admins get no platform tab or chain verifier', async () => {
    mockFeed({ items: [event()], total: 1 })
    renderPage()
    await screen.findByText('token.created')
    expect(screen.queryByText('Platform-wide')).toBeNull()
    expect(screen.queryByTestId('chain-verify-panel')).toBeNull()
  })

  it('platform staff verify the chain and see a break', async () => {
    currentUser = { role: 10 }
    mockFeed({ items: [event()], total: 1 })
    const report: ChainReport = {
      checked: 10,
      legacyRows: 2,
      hashBreaks: 1,
      firstBreak: { id: 4, expected: 'a'.repeat(64), actual: 'b'.repeat(64) },
      linkBreaks: 0,
      firstLinkBreak: null,
      linkChecksSkipped: false,
      nextCursor: 0,
    }
    vi.mocked(auditApi.verifyAuditChain).mockResolvedValue(report)
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByText('Platform-wide'))
    await user.click(await screen.findByRole('button', { name: 'Verify chain' }))
    expect(await screen.findByTestId('chain-verdict')).toHaveTextContent(
      'Breaks found'
    )
    expect(screen.getByTestId('chain-hash-breaks')).toHaveTextContent('1')
  })
})
