import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api-error'

import * as api from './api'
import { UsageLogsPage } from './index'
import type { LogStat, UsageLog } from './lib/logs'

vi.mock('./api', () => ({ fetchLogs: vi.fn(), fetchLogStat: vi.fn() }))
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
    queryFn: () => Promise.resolve({ role: 1 }),
  },
}))

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

  it('renders converted money and the stat header', async () => {
    vi.mocked(api.fetchLogs).mockResolvedValue(
      page([row({ id: 1, model_name: 'plain-model' })], 1)
    )
    renderPage()
    expect(await screen.findByText('plain-model')).toBeInTheDocument()
    expect(screen.getAllByText('$1.0000').length).toBe(1)
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

  it('hides the all-users scope from non-admins', async () => {
    vi.mocked(api.fetchLogs).mockResolvedValue(page([], 0))
    renderPage()
    await screen.findByText('No logs in this window')
    expect(screen.queryByText('All users')).toBeNull()
  })
})
