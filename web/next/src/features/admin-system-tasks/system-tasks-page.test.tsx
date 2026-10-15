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
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { AdminSystemTasksPage } from './index'
import { formatInterval } from './lib'

const { apiGet } = vi.hoisted(() => ({ apiGet: vi.fn() }))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: { get: apiGet, post: vi.fn(), put: vi.fn(), delete: vi.fn() },
  }
})

const URL = '/api/v2/admin/system/tasks'

function snapshot(
  tasks: Array<Record<string, unknown>>,
  extra: Record<string, unknown> = {}
) {
  return { is_leader: true, pod: 'newhub-abc123', tasks, ...extra }
}

function task(over: Record<string, unknown> = {}) {
  return {
    name: 'audit-cleanup',
    interval_seconds: 86400,
    leader_only: true,
    last_success_at: 1_700_000_000,
    state: 'ok',
    standby_reason: '',
    ...over,
  }
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <AdminSystemTasksPage />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  apiGet.mockReset()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('formatInterval', () => {
  it('renders hours, minutes and seconds, and a dash for nothing', () => {
    expect(formatInterval(86400)).toBe('24h')
    expect(formatInterval(300)).toBe('5m')
    expect(formatInterval(45)).toBe('45s')
    expect(formatInterval(0)).toBe('—')
    expect(formatInterval(undefined)).toBe('—')
  })
})

describe('AdminSystemTasksPage', () => {
  it('reads the root-only endpoint without bouncing on a 401', async () => {
    apiGet.mockResolvedValue(snapshot([]))
    renderPage()
    await screen.findByTestId('system-tasks-empty')
    expect(apiGet).toHaveBeenCalledWith(URL, { skipAuthRedirect: true })
  })

  it('renders each task row with interval, leader-only and state', async () => {
    apiGet.mockResolvedValue(snapshot([task()]))
    renderPage()
    const row = await screen.findByTestId('system-tasks-row-audit-cleanup')
    expect(row).toHaveTextContent('audit-cleanup')
    expect(row).toHaveTextContent('24h')
    expect(row).toHaveTextContent('Yes')
    expect(
      screen.getByTestId('system-tasks-state-audit-cleanup')
    ).toHaveTextContent('On schedule')
    expect(screen.getByTestId('system-tasks-headline')).toHaveTextContent(
      'All tasks on schedule'
    )
    expect(screen.getByTestId('system-tasks-pod')).toHaveTextContent(
      'newhub-abc123'
    )
  })

  it('headlines the number of overdue tasks and paints the badge as an error', async () => {
    apiGet.mockResolvedValue(
      snapshot([
        task({
          name: 'privacy-erasure',
          interval_seconds: 60,
          last_success_at: 0,
          state: 'overdue',
        }),
        task({ name: 'secret-rotation' }),
      ])
    )
    renderPage()
    await waitFor(() =>
      expect(screen.getByTestId('system-tasks-headline')).toHaveTextContent(
        '1 task overdue'
      )
    )
    expect(
      screen.getByTestId('system-tasks-state-privacy-erasure').className
    ).toMatch(/text-destructive/)
    expect(
      screen.getByTestId('system-tasks-state-secret-rotation').className
    ).not.toMatch(/text-destructive/)
    // An active task that never once succeeded says so.
    expect(
      screen.getByTestId('system-tasks-row-privacy-erasure')
    ).toHaveTextContent('Never')
  })

  it('pluralises the overdue headline', async () => {
    apiGet.mockResolvedValue(
      snapshot([
        task({ name: 'a', state: 'overdue' }),
        task({ name: 'b', state: 'overdue' }),
      ])
    )
    renderPage()
    await waitFor(() =>
      expect(screen.getByTestId('system-tasks-headline')).toHaveTextContent(
        '2 tasks overdue'
      )
    )
  })

  it('gives a standby task a neutral (non-error) badge', async () => {
    apiGet.mockResolvedValue(
      snapshot([
        task({
          name: 'credit-pool-reconcile',
          interval_seconds: 300,
          last_success_at: 0,
          state: 'standby',
        }),
      ])
    )
    renderPage()
    const badge = await screen.findByTestId(
      'system-tasks-state-credit-pool-reconcile'
    )
    expect(badge).toHaveTextContent('Standby')
    expect(badge.className).not.toMatch(/text-destructive/)
    expect(screen.getByTestId('system-tasks-headline')).toHaveTextContent(
      'All tasks on schedule'
    )
  })

  it('shows the standby reason next to a follower badge', async () => {
    apiGet.mockResolvedValue(
      snapshot([
        task({
          last_success_at: 0,
          state: 'standby',
          standby_reason: 'follower',
        }),
      ])
    )
    renderPage()
    const reason = await screen.findByTestId(
      'system-tasks-reason-audit-cleanup'
    )
    expect(reason).toHaveTextContent('Follower replica, not the leader')
  })

  it('shows the standby reason next to a disabled badge, still neutral', async () => {
    apiGet.mockResolvedValue(
      snapshot([
        task({
          name: 'channel-health-test',
          interval_seconds: 600,
          leader_only: false,
          last_success_at: 0,
          state: 'standby',
          standby_reason: 'disabled',
        }),
      ])
    )
    renderPage()
    const badge = await screen.findByTestId(
      'system-tasks-state-channel-health-test'
    )
    expect(badge.className).not.toMatch(/text-destructive/)
    expect(
      screen.getByTestId('system-tasks-reason-channel-health-test')
    ).toHaveTextContent('Disabled by an administrator')
    expect(
      screen.getByTestId('system-tasks-row-channel-health-test')
    ).toHaveTextContent('10m')
  })

  it('does not print a reason for a non-standby task', async () => {
    apiGet.mockResolvedValue(snapshot([task({ standby_reason: 'follower' })]))
    renderPage()
    await screen.findByTestId('system-tasks-row-audit-cleanup')
    expect(screen.queryByTestId('system-tasks-reason-audit-cleanup')).toBeNull()
  })

  // Without the caveat a "standby" row reads as broken when it is the
  // expected state for a leader-gated task on a follower.
  it('states the per-pod caveat whenever the table is shown', async () => {
    apiGet.mockResolvedValue(snapshot([]))
    renderPage()
    await screen.findByTestId('system-tasks-empty')
    expect(screen.getByTestId('system-tasks-caveat')).toHaveTextContent(
      /per-pod/i
    )
  })

  it('polls while live and stops once paused', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    apiGet.mockResolvedValue(snapshot([]))
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    renderPage()
    await screen.findByTestId('system-tasks-empty')
    const afterMount = apiGet.mock.calls.length

    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000)
    })
    const afterOneTick = apiGet.mock.calls.length
    expect(afterOneTick).toBeGreaterThan(afterMount)

    await user.click(screen.getByTestId('system-tasks-live-toggle'))
    await act(async () => {
      await vi.advanceTimersByTimeAsync(45_000)
    })
    expect(apiGet.mock.calls.length).toBe(afterOneTick)

    // Resuming restarts the cadence.
    await user.click(screen.getByTestId('system-tasks-live-toggle'))
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000)
    })
    expect(apiGet.mock.calls.length).toBeGreaterThan(afterOneTick)
  })

  /*
   * The refusal shape moved and the failure shape was missing: a 403 means
   * "not root", a 401 means "signed out", and anything else (a 502 mid
   * rollout, a dropped connection, a handler failure) means the schedule is
   * UNKNOWN, which must never read as "nothing is late".
   */
  it('shows a permission notice on 403 and stops polling', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    apiGet.mockRejectedValue(new ApiError('nope', { status: 403 }))
    renderPage()
    await screen.findByText(
      /You do not have permission to view background tasks/
    )
    expect(screen.getByText('Root access required')).toBeInTheDocument()
    expect(screen.queryByTestId('system-tasks-live-toggle')).toBeNull()
    expect(screen.queryByTestId('system-tasks-error')).toBeNull()
    const after = apiGet.mock.calls.length
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000)
    })
    expect(apiGet.mock.calls.length).toBe(after)
  })

  it('shows sign-in-again on 401, not the permission panel, and stops polling', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    apiGet.mockRejectedValue(new ApiError('expired', { status: 401 }))
    renderPage()
    await screen.findByTestId('system-tasks-signed-out')
    expect(
      screen.getByTestId('system-tasks-sign-in').getAttribute('href')
    ).toMatch(/^\/login\?redirect=/)
    expect(
      screen.queryByText(/You do not have permission to view background tasks/)
    ).toBeNull()
    expect(screen.queryByText('All tasks on schedule')).toBeNull()
    expect(screen.queryByTestId('system-tasks-live-toggle')).toBeNull()
    const after = apiGet.mock.calls.length
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000)
    })
    expect(apiGet.mock.calls.length).toBe(after)
  })

  it('shows an error state, not "all tasks on schedule", when the endpoint 502s', async () => {
    apiGet.mockRejectedValue(new ApiError('Bad gateway', { status: 502 }))
    renderPage()
    await screen.findByTestId('system-tasks-error')
    expect(screen.getByText('Task schedule unknown')).toBeInTheDocument()
    expect(screen.queryByText('All tasks on schedule')).toBeNull()
    expect(screen.queryByTestId('system-tasks-empty')).toBeNull()
    expect(
      screen.queryByText(/You do not have permission to view background tasks/)
    ).toBeNull()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  it('shows the backend message on a 200 carrying success:false', async () => {
    // unwrap() throws an ApiError carrying the message for a failed envelope.
    apiGet.mockRejectedValue(
      new ApiError('task registry unavailable', { status: 200 })
    )
    renderPage()
    const panel = await screen.findByTestId('system-tasks-error')
    expect(panel).toHaveTextContent('task registry unavailable')
    expect(screen.queryByText('All tasks on schedule')).toBeNull()
    // Not a refusal: the operator must not be sent off to ask for access.
    expect(screen.queryByText('Root access required')).toBeNull()
  })

  // Stopping outright freezes the page after one transient 502, so polling
  // continues in the error state, backed off from 15s to 30s.
  it('keeps polling in the error state, at the 30s backoff rather than 15s', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    apiGet.mockRejectedValue(new ApiError('Bad gateway', { status: 502 }))
    renderPage()
    await screen.findByTestId('system-tasks-error')
    const afterError = apiGet.mock.calls.length

    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000)
    })
    expect(apiGet.mock.calls.length).toBe(afterError)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000)
    })
    expect(apiGet.mock.calls.length).toBeGreaterThan(afterError)
  })

  it('hides the live toggle while the read is failing', async () => {
    apiGet.mockRejectedValue(new ApiError('Bad gateway', { status: 502 }))
    renderPage()
    await screen.findByTestId('system-tasks-error')
    expect(screen.queryByTestId('system-tasks-live-toggle')).toBeNull()
  })

  it('retry re-runs the fetch and leaves the error state once it succeeds', async () => {
    apiGet.mockRejectedValue(new ApiError('Bad gateway', { status: 502 }))
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('system-tasks-error')

    apiGet.mockResolvedValue(
      snapshot([
        task({
          name: 'channel-cache-sync',
          interval_seconds: 60,
          leader_only: false,
        }),
      ])
    )
    await user.click(screen.getByRole('button', { name: 'Retry' }))

    await screen.findByTestId('system-tasks-row-channel-cache-sync')
    expect(screen.queryByTestId('system-tasks-error')).toBeNull()
  })

  it('shows the table again when a later poll recovers', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    apiGet.mockRejectedValueOnce(new ApiError('Bad gateway', { status: 502 }))
    apiGet.mockResolvedValue(snapshot([task()]))
    renderPage()
    await screen.findByTestId('system-tasks-error')
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000)
    })
    await screen.findByTestId('system-tasks-row-audit-cleanup')
    expect(screen.queryByTestId('system-tasks-error')).toBeNull()
  })
})
