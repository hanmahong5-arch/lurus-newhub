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

import { ApiError } from '@/lib/api-error'

import * as api from './api'
import { UsageLogsPage } from './index'
import type { LogStat, UsageLog } from './lib/logs'

vi.mock('./api', () => ({
  fetchLogs: vi.fn(),
  fetchLogStat: vi.fn(),
  fetchLogBody: vi.fn(),
  exportLogsCsv: vi.fn(),
}))
vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))
vi.mock('@/lib/status', () => ({
  useMoneyConfig: () => ({
    quotaPerUnit: 500000,
    displayType: 'USD',
    symbol: '$',
    rate: 1,
  }),
}))

let currentUser: Record<string, unknown> = { role: 1 }
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

const row = (over: Partial<UsageLog> = {}): UsageLog => ({
  id: 1,
  user_id: 1,
  created_at: 1_800_000_000,
  type: 2,
  content: '',
  username: 'u',
  token_name: 'tok',
  model_name: 'm-1',
  quota: 500000,
  prompt_tokens: 10,
  completion_tokens: 20,
  use_time: 1,
  is_stream: false,
  channel: 1,
  channel_name: '',
  token_id: 1,
  group: '',
  total_latency_ms: 150,
  ...over,
})

const stat: LogStat = {
  total_requests: 42,
  total_quota: 1_000_000,
  prompt_tokens: 1500,
  completion_tokens: 2500,
  cache_read_tokens: 0,
  cache_write_tokens: 0,
  rpm: 3,
  tpm: 99,
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <UsageLogsPage />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  currentUser = { role: 1 }
  vi.mocked(api.fetchLogStat).mockResolvedValue(stat)
})

const page = (logs: UsageLog[] | null, total: number) => ({
  logs,
  total,
  page: 1,
  page_size: 20,
})

describe('UsageLogsPage', () => {
  it('shows an error, not an empty list, when the read fails', async () => {
    vi.mocked(api.fetchLogs).mockRejectedValue(
      new ApiError('boom upstream', { status: 500 })
    )
    renderPage()
    expect(await screen.findByText('boom upstream')).toBeInTheDocument()
    expect(screen.queryByText('No logs in this window')).toBeNull()
  })

  it('shows the empty state only for a successful empty read', async () => {
    vi.mocked(api.fetchLogs).mockResolvedValue(page(null, 0))
    renderPage()
    expect(
      await screen.findByText('No logs in this window')
    ).toBeInTheDocument()
  })

  it('renders converted money, the wallet debit and the stat header', async () => {
    vi.mocked(api.fetchLogs).mockResolvedValue(
      page([row({ id: 1, model_name: 'plain-model', charged_cny4: 1234 })], 1)
    )
    renderPage()
    expect(await screen.findByText('plain-model')).toBeInTheDocument()
    // 500000 quota at 500000/unit is $1 (per-request precision is 6 digits).
    expect(screen.getAllByText('$1.000000').length).toBe(1)
    expect(screen.getByText(/¥0\.1234/)).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.getByTestId('log-stat-requests')).toHaveTextContent('42')
    )
    expect(screen.getByTestId('log-stat-quota')).toHaveTextContent('$2.00')
  })

  it('a failed stat read says so instead of showing zeros', async () => {
    vi.mocked(api.fetchLogs).mockResolvedValue(page([row()], 1))
    vi.mocked(api.fetchLogStat).mockRejectedValue(new ApiError('nope'))
    renderPage()
    expect(
      await screen.findByText('Failed to load statistics')
    ).toBeInTheDocument()
    expect(screen.getByTestId('log-stat-requests')).toHaveTextContent('--')
  })

  it('hides the all-users scope and the channel column from members', async () => {
    vi.mocked(api.fetchLogs).mockResolvedValue(page([row()], 1))
    renderPage()
    await screen.findByText('m-1')
    expect(screen.queryByText('All users')).toBeNull()
    expect(
      screen.queryByRole('columnheader', { name: 'Channel' })
    ).toBeNull()
  })

  it('a model filter is sent only after Search', async () => {
    vi.mocked(api.fetchLogs).mockResolvedValue(page([row()], 1))
    renderPage()
    await screen.findByText('m-1')
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('Model'), 'model-x')
    const before = vi.mocked(api.fetchLogs).mock.calls.length
    expect(
      vi.mocked(api.fetchLogs).mock.calls.at(-1)?.[0].model
    ).toBe('')
    await user.click(screen.getByRole('button', { name: 'Search' }))
    await waitFor(() =>
      expect(vi.mocked(api.fetchLogs).mock.calls.length).toBeGreaterThan(before)
    )
    expect(vi.mocked(api.fetchLogs).mock.calls.at(-1)?.[0].model).toBe(
      'model-x'
    )
  })

  it('platform staff see the channel column and the all-users scope', async () => {
    currentUser = { role: 10 }
    vi.mocked(api.fetchLogs).mockResolvedValue(
      page([row({ channel: 7, channel_name: 'vendor-a' })], 1)
    )
    renderPage()
    expect(await screen.findByText('vendor-a')).toBeInTheDocument()
    expect(screen.getByText('All users')).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: 'Channel' })).toBeInTheDocument()
  })

  it('opens the detail sheet with the routing trace for staff', async () => {
    currentUser = { role: 10 }
    vi.mocked(api.fetchLogs).mockResolvedValue(
      page(
        [
          row({
            other: {
              request_id: 'req-9',
              admin_info: {
                route_attempts: [
                  {
                    channel_id: 3,
                    outcome: 'upstream_error',
                    status_code: 502,
                    duration_ms: 40,
                  },
                  { channel_id: 4, outcome: 'success', duration_ms: 90 },
                ],
              },
            },
          }),
        ],
        1
      )
    )
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByTestId('log-row'))
    const trace = await screen.findByTestId('log-route-attempts')
    expect(within(trace).getByTestId('route-attempt-0')).toHaveTextContent('#3')
    expect(within(trace).getByTestId('route-attempt-1')).toHaveTextContent(
      'success'
    )
    expect(screen.getByTestId('log-detail-request-id')).toHaveTextContent(
      'req-9'
    )
  })

  it('a failed export is reported, not downloaded', async () => {
    const { toast } = await import('sonner')
    vi.mocked(api.fetchLogs).mockResolvedValue(page([row()], 1))
    vi.mocked(api.exportLogsCsv).mockRejectedValue(
      new ApiError('export denied', { status: 403 })
    )
    renderPage()
    await screen.findByText('m-1')
    await userEvent.setup().click(
      screen.getByRole('button', { name: 'Export CSV' })
    )
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('export denied')
    )
    expect(toast.success).not.toHaveBeenCalled()
  })
})
