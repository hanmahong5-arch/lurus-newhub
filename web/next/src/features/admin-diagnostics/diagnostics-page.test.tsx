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
import type { AxiosRequestConfig } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError, http } from '@/lib/api'

import { AdminDiagnosticsPage } from './index'
import { formatPct } from './lib/format'
import { classifyReadError } from './lib/load-status'

const toast = vi.hoisted(() => ({
  success: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
}))
vi.mock('sonner', () => ({ toast }))

const AFFINITY_URL = '/api/v2/admin/routing/affinity'
const TOTP_URL = '/api/v2/admin/security/totp-stats'
const PURGE_ALL_URL = `${AFFINITY_URL}?all=true`

// backend defaults to 'redis': mem_entries is meaningful only for 'memory', so
// the default fixture must be the case that hides it.
const AFFINITY = {
  enabled: true,
  ttl_seconds: 300,
  hit: 137,
  miss: 42,
  stale: 5,
  backend: 'redis',
  mem_entries: 9,
}

const TOTP = {
  enrolled: 8,
  pending: 3,
  total_users: 20,
  adoption_pct: 40,
  backup_codes_exhausted: 1,
  no_codes_issued: 2,
}

type Reply = { status?: number; data?: unknown } | Error

const affinityBody = (data = AFFINITY, scope = 'replica') => ({
  data: { success: true, scope, data },
})
const totpBody = (data = TOTP) => ({
  data: { success: true, message: '', data },
})

/**
 * Replace only the transport: the real api layer (envelope unwrap, ApiError
 * mapping) stays under test. An Error is a rejected request as the response
 * interceptor would have shaped it; anything else is the axios response.
 */
function serve(
  handlers: {
    affinity?: () => Reply
    totp?: () => Reply
    purgeOne?: (url: string) => Reply
    purgeAll?: () => Reply
  } = {}
) {
  return vi
    .spyOn(http, 'request')
    .mockImplementation((config: AxiosRequestConfig) => {
      const url = String(config.url)
      const method = String(config.method ?? 'GET').toUpperCase()
      let reply: Reply
      if (method === 'GET' && url === AFFINITY_URL) {
        reply = (
          handlers.affinity ?? (() => ({ status: 200, ...affinityBody() }))
        )()
      } else if (method === 'GET' && url === TOTP_URL) {
        reply = (handlers.totp ?? (() => ({ status: 200, ...totpBody() })))()
      } else if (method === 'DELETE' && url === PURGE_ALL_URL) {
        reply = (
          handlers.purgeAll ??
          (() => ({
            status: 200,
            data: { success: true, data: { purged: 4, complete: true } },
          }))
        )()
      } else if (method === 'DELETE' && url.startsWith(`${AFFINITY_URL}/`)) {
        reply = (handlers.purgeOne ?? (() => ({ status: 204, data: '' })))(url)
      } else {
        return Promise.reject(new Error(`unexpected ${method} ${url}`)) as never
      }
      if (reply instanceof Error) return Promise.reject(reply) as never
      return Promise.resolve(reply) as never
    })
}

function calls(spy: ReturnType<typeof serve>, method: string, url: string) {
  return spy.mock.calls.filter(
    ([c]) =>
      String(c.method ?? 'GET').toUpperCase() === method &&
      String(c.url) === url
  )
}

const getCount = (spy: ReturnType<typeof serve>) =>
  calls(spy, 'GET', AFFINITY_URL).length + calls(spy, 'GET', TOTP_URL).length

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <AdminDiagnosticsPage />
    </QueryClientProvider>
  )
}

const text = (id: string) => screen.getByTestId(id).textContent

beforeEach(() => {
  vi.clearAllMocks()
})

describe('affinity + TOTP panels', () => {
  it('reads both endpoints on mount', async () => {
    const spy = serve()
    renderPage()
    await screen.findByTestId('diag-affinity-hit')
    expect(calls(spy, 'GET', AFFINITY_URL)).toHaveLength(1)
    expect(calls(spy, 'GET', TOTP_URL)).toHaveLength(1)
  })

  it('renders the real affinity counters, not placeholders', async () => {
    serve()
    renderPage()
    await screen.findByTestId('diag-affinity-hit')

    expect(text('diag-affinity-hit')).toBe('137')
    expect(text('diag-affinity-miss')).toBe('42')
    expect(text('diag-affinity-stale')).toBe('5')
    expect(text('diag-affinity-backend')).toBe('redis')
    expect(text('diag-affinity-ttl')).toBe('300')
    expect(text('diag-affinity-enabled')).toBe('Yes')
  })

  it('renders the scope from the response, not a hard-coded word', async () => {
    serve({
      affinity: () => ({ status: 200, ...affinityBody(AFFINITY, 'replica') }),
    })
    const { unmount } = renderPage()
    await screen.findByTestId('diag-affinity-hit')
    expect(text('diag-affinity-scope-note')).toMatch(/Scope: replica\./)
    unmount()

    serve({
      affinity: () => ({ status: 200, ...affinityBody(AFFINITY, 'cluster-x') }),
    })
    renderPage()
    await screen.findByTestId('diag-affinity-hit')
    expect(text('diag-affinity-scope-note')).toMatch(/Scope: cluster-x\./)
  })

  it('does not present mem_entries as a binding count while the backend is redis', async () => {
    serve()
    renderPage()
    await screen.findByTestId('diag-affinity-hit')

    expect(screen.queryByTestId('diag-affinity-mementries')).toBeNull()
    expect(text('diag-affinity-mementries-note')).toMatch(/redis/i)
  })

  it('renders mem_entries as a real count when the backend is memory', async () => {
    serve({
      affinity: () => ({
        status: 200,
        ...affinityBody({ ...AFFINITY, backend: 'memory', mem_entries: 9 }),
      }),
    })
    renderPage()
    await screen.findByTestId('diag-affinity-mementries')

    expect(text('diag-affinity-mementries')).toBe('9')
    expect(screen.queryByTestId('diag-affinity-mementries-note')).toBeNull()
  })

  it('renders "No" for enabled:false, not a truthy default', async () => {
    serve({
      affinity: () => ({
        status: 200,
        ...affinityBody({ ...AFFINITY, enabled: false }),
      }),
    })
    renderPage()
    await screen.findByTestId('diag-affinity-enabled')
    expect(text('diag-affinity-enabled')).toBe('No')
  })

  it('renders the real TOTP adoption numbers', async () => {
    serve()
    renderPage()
    await screen.findByTestId('diag-totp-enrolled')

    expect(text('diag-totp-enrolled')).toBe('8')
    expect(text('diag-totp-pending')).toBe('3')
    expect(text('diag-totp-total')).toBe('20')
    expect(text('diag-totp-adoption-pct')).toBe('40.0%')
    expect(text('diag-totp-exhausted')).toBe('1')
    expect(text('diag-totp-no-codes')).toBe('2')
  })

  it('says plainly that the no-second-factor privileged count is not available', async () => {
    serve()
    renderPage()
    await screen.findByTestId('diag-totp-no-factor-note')
    expect(text('diag-totp-no-factor-note')).toMatch(/not exposed/i)
  })

  it('shows the unavailable marker, not the permission notice, for a 200 {success:false} it cannot classify', async () => {
    serve({
      totp: () => ({
        status: 200,
        data: { success: false, message: 'root required' },
      }),
    })
    renderPage()

    await screen.findByTestId('diag-totp-unavailable')
    expect(
      screen.queryByText(/You do not have permission to view diagnostics/)
    ).toBeNull()
    // The panel whose GET succeeded still renders its real numbers.
    expect(text('diag-affinity-hit')).toBe('137')
  })

  it('shows the permission notice for a resolved body carrying PERMISSION_DENIED', async () => {
    serve({
      totp: () => ({
        status: 200,
        data: {
          success: false,
          error_code: 'PERMISSION_DENIED',
          message: 'permission denied',
        },
      }),
    })
    renderPage()

    await screen.findByText(/You do not have permission to view diagnostics/)
    expect(screen.queryByTestId('diag-affinity-hit')).toBeNull()
  })

  it('shows the permission notice for the v1 session refusal text', async () => {
    serve({
      affinity: () => ({
        status: 200,
        data: { success: false, message: '无权进行此操作，权限不足' },
      }),
    })
    renderPage()
    await screen.findByText(/You do not have permission to view diagnostics/)
  })

  it('shows the permission notice when a GET is rejected with HTTP 403', async () => {
    serve({ affinity: () => new ApiError('forbidden', { status: 403 }) })
    renderPage()

    await screen.findByText(/You do not have permission to view diagnostics/)
    expect(screen.queryByTestId('diag-affinity-hit')).toBeNull()
    expect(screen.queryByTestId('diag-totp-enrolled')).toBeNull()
  })

  it('a failed GET marks only its own panel unavailable and fabricates no numbers', async () => {
    serve({ totp: () => new ApiError('boom', { status: 500 }) })
    renderPage()

    await screen.findByTestId('diag-totp-unavailable')
    expect(screen.queryByTestId('diag-totp-adoption-pct')).toBeNull()
    expect(screen.queryByTestId('diag-totp-enrolled')).toBeNull()
    expect(text('diag-affinity-hit')).toBe('137')
  })

  it('lets the operator retry just the panel that failed', async () => {
    let fail = true
    const spy = serve({
      totp: () =>
        fail
          ? new ApiError('boom', { status: 500 })
          : { status: 200, ...totpBody() },
    })
    const user = userEvent.setup()
    renderPage()
    const marker = await screen.findByTestId('diag-totp-unavailable')

    fail = false
    await user.click(within(marker).getByRole('button', { name: 'Retry' }))

    await screen.findByTestId('diag-totp-enrolled')
    expect(screen.queryByTestId('diag-totp-unavailable')).toBeNull()
    // Only the failed read was repeated.
    expect(calls(spy, 'GET', AFFINITY_URL)).toHaveLength(1)
    expect(calls(spy, 'GET', TOTP_URL)).toHaveLength(2)
  })

  it('refreshes both panels from the header button', async () => {
    const spy = serve()
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('diag-affinity-hit')

    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(getCount(spy)).toBe(4))
  })
})

describe('purge one binding', () => {
  async function ready() {
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('diag-affinity-purge-key-input')
    return user
  }

  it('sends a DELETE with the typed key and refetches on success', async () => {
    const spy = serve()
    const user = await ready()
    await screen.findByTestId('diag-affinity-hit')

    await user.type(
      screen.getByTestId('diag-affinity-purge-key-input'),
      'abc123'
    )
    await user.click(screen.getByTestId('diag-affinity-purge-key-btn'))

    await waitFor(() =>
      expect(calls(spy, 'DELETE', `${AFFINITY_URL}/abc123`)).toHaveLength(1)
    )
    // Two GETs on mount + two more after a successful purge.
    await waitFor(() => expect(getCount(spy)).toBe(4))
    await waitFor(() =>
      expect(text('diag-affinity-purge-key-msg')).toMatch(/Binding purged/)
    )
    expect(screen.getByTestId('diag-affinity-purge-key-input')).toHaveValue('')
  })

  it('url-encodes the key so it cannot escape its path segment', async () => {
    const spy = serve()
    const user = await ready()
    await screen.findByTestId('diag-affinity-hit')

    await user.type(
      screen.getByTestId('diag-affinity-purge-key-input'),
      ' a/b c?d '
    )
    await user.click(screen.getByTestId('diag-affinity-purge-key-btn'))

    await waitFor(() =>
      expect(
        calls(spy, 'DELETE', `${AFFINITY_URL}/${encodeURIComponent('a/b c?d')}`)
      ).toHaveLength(1)
    )
  })

  it('reports "no such binding" on a 404 and leaves the numbers alone', async () => {
    const spy = serve({
      purgeOne: () =>
        new ApiError('affinity binding not found', { status: 404 }),
    })
    const user = await ready()
    await screen.findByTestId('diag-affinity-hit')

    await user.type(
      screen.getByTestId('diag-affinity-purge-key-input'),
      'stale-key'
    )
    await user.click(screen.getByTestId('diag-affinity-purge-key-btn'))

    await waitFor(() =>
      expect(text('diag-affinity-purge-key-msg')).toMatch(/No such binding/)
    )
    expect(text('diag-affinity-hit')).toBe('137')
    // A failed purge does not refetch.
    expect(getCount(spy)).toBe(2)
  })

  it('does not read a backend failure as "no such binding"', async () => {
    serve({
      purgeOne: () =>
        new ApiError('affinity purge failed: redis down', { status: 500 }),
    })
    const user = await ready()
    await screen.findByTestId('diag-affinity-hit')

    await user.type(screen.getByTestId('diag-affinity-purge-key-input'), 'k1')
    await user.click(screen.getByTestId('diag-affinity-purge-key-btn'))

    await waitFor(() =>
      expect(text('diag-affinity-purge-key-msg')).toMatch(/redis down/)
    )
    expect(text('diag-affinity-purge-key-msg')).not.toMatch(/No such binding/)
  })

  it('sends nothing when the key field is empty or blank', async () => {
    const spy = serve()
    const user = await ready()

    await user.click(screen.getByTestId('diag-affinity-purge-key-btn'))
    await user.type(screen.getByTestId('diag-affinity-purge-key-input'), '   ')
    await user.click(screen.getByTestId('diag-affinity-purge-key-btn'))

    expect(spy.mock.calls.filter(([c]) => c.method === 'DELETE')).toHaveLength(
      0
    )
  })

  it('shows the server message on a non-204 answer instead of doing nothing', async () => {
    serve({
      purgeOne: () => ({
        status: 200,
        data: { success: false, message: 'root required for this action' },
      }),
    })
    const user = await ready()
    await screen.findByTestId('diag-affinity-hit')

    await user.type(
      screen.getByTestId('diag-affinity-purge-key-input'),
      'abc123'
    )
    await user.click(screen.getByTestId('diag-affinity-purge-key-btn'))

    await waitFor(() =>
      expect(text('diag-affinity-purge-key-msg')).toMatch(
        /root required for this action/
      )
    )
  })

  it('still offers the purge controls when the stats could not be read', async () => {
    serve({ affinity: () => new ApiError('boom', { status: 500 }) })
    renderPage()
    await screen.findByTestId('diag-affinity-unavailable')
    expect(
      screen.getByTestId('diag-affinity-purge-key-btn')
    ).toBeInTheDocument()
    expect(
      screen.getByTestId('diag-affinity-purge-all-btn')
    ).toBeInTheDocument()
  })
})

describe('purge all (typed confirmation)', () => {
  async function openDialog(spy: ReturnType<typeof serve>) {
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByTestId('diag-affinity-purge-all-btn'))
    // The button alone must only open the dialog.
    expect(calls(spy, 'DELETE', PURGE_ALL_URL)).toHaveLength(0)
    const dialog = await screen.findByRole('alertdialog')
    return { user, dialog }
  }

  const confirmButton = (dialog: HTMLElement) =>
    within(dialog).getByRole('button', { name: 'Purge all' })

  it('stays disarmed until the exact phrase is typed, then deletes and closes', async () => {
    const spy = serve()
    const { user, dialog } = await openDialog(spy)

    expect(confirmButton(dialog)).toBeDisabled()
    const input = within(dialog).getByLabelText(/Type PURGE ALL to confirm/)
    await user.type(input, 'purge all')
    expect(confirmButton(dialog)).toBeDisabled()
    await user.clear(input)
    await user.type(input, 'PURGE ALL')
    expect(confirmButton(dialog)).toBeEnabled()
    expect(calls(spy, 'DELETE', PURGE_ALL_URL)).toHaveLength(0)

    await user.click(confirmButton(dialog))

    await waitFor(() =>
      expect(calls(spy, 'DELETE', PURGE_ALL_URL)).toHaveLength(1)
    )
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(toast.success).toHaveBeenCalledWith('Purged 4 binding(s)')
    // Refetch after the purge.
    await waitFor(() => expect(getCount(spy)).toBe(4))
  })

  it('sends nothing when the dialog is cancelled', async () => {
    const spy = serve()
    const { user, dialog } = await openDialog(spy)

    await user.type(
      within(dialog).getByLabelText(/Type PURGE ALL to confirm/),
      'PURGE ALL'
    )
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(calls(spy, 'DELETE', PURGE_ALL_URL)).toHaveLength(0)
  })

  it('reopens disarmed: the typed phrase does not survive a close', async () => {
    const spy = serve()
    const { user, dialog } = await openDialog(spy)
    await user.type(
      within(dialog).getByLabelText(/Type PURGE ALL to confirm/),
      'PURGE ALL'
    )
    await user.click(confirmButton(dialog))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())

    await user.click(screen.getByTestId('diag-affinity-purge-all-btn'))
    const again = await screen.findByRole('alertdialog')
    expect(
      within(again).getByLabelText(/Type PURGE ALL to confirm/)
    ).toHaveValue('')
    expect(confirmButton(again)).toBeDisabled()
  })

  it('surfaces an incomplete purge instead of reporting a full wipe', async () => {
    const spy = serve({
      purgeAll: () => ({
        status: 200,
        data: { success: true, data: { purged: 2, complete: false } },
      }),
    })
    const { user, dialog } = await openDialog(spy)
    await user.type(
      within(dialog).getByLabelText(/Type PURGE ALL to confirm/),
      'PURGE ALL'
    )
    await user.click(confirmButton(dialog))

    await waitFor(() => expect(toast.warning).toHaveBeenCalled())
    expect(String(toast.warning.mock.calls[0][0])).toMatch(/Incomplete/)
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('keeps the dialog open and reports the error when the purge is rejected', async () => {
    const spy = serve({
      purgeAll: () => new ApiError('redis down', { status: 500 }),
    })
    const { user, dialog } = await openDialog(spy)
    await user.type(
      within(dialog).getByLabelText(/Type PURGE ALL to confirm/),
      'PURGE ALL'
    )
    await user.click(confirmButton(dialog))

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('redis down'))
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
    expect(toast.success).not.toHaveBeenCalled()
    expect(calls(spy, 'DELETE', PURGE_ALL_URL)).toHaveLength(1)
  })
})

describe('lib', () => {
  it('formats a percentage to one decimal and never invents a zero', () => {
    expect(formatPct(40)).toBe('40.0%')
    expect(formatPct(33.333)).toBe('33.3%')
    expect(formatPct(undefined)).toBe('-')
    expect(formatPct(Number.NaN)).toBe('-')
  })

  it('classifies read errors', () => {
    expect(classifyReadError(new ApiError('x', { status: 403 }))).toBe(
      'forbidden'
    )
    expect(classifyReadError(new ApiError('x', { status: 401 }))).toBe(
      'unauthenticated'
    )
    expect(classifyReadError(new ApiError('x', { status: 500 }))).toBe('error')
    expect(
      classifyReadError(
        new ApiError('x', { status: 200, code: 'PERMISSION_DENIED' })
      )
    ).toBe('forbidden')
    expect(
      classifyReadError(new ApiError('maintenance', { status: 200 }))
    ).toBe('error')
  })
})
