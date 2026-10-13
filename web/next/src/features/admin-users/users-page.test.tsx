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

import { AdminUsersPage } from './index'

const { apiGet, apiPut, apiPost, apiDelete, tenantGet, routerState, toast } =
  vi.hoisted(() => ({
    apiGet: vi.fn(),
    apiPut: vi.fn(),
    apiPost: vi.fn(),
    apiDelete: vi.fn(),
    tenantGet: vi.fn(),
    routerState: {
      search: {} as Record<string, unknown>,
      listeners: new Set<() => void>(),
    },
    toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
  }))

vi.mock('sonner', () => ({ toast }))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: { get: apiGet, put: apiPut, post: apiPost, delete: apiDelete },
    tenantApi: { get: tenantGet, post: vi.fn(), put: vi.fn(), delete: vi.fn() },
  }
})

vi.mock('@tanstack/react-router', async (orig) => {
  const actual = await orig<typeof import('@tanstack/react-router')>()
  const React = await import('react')
  return {
    ...actual,
    getRouteApi: () => ({
      useSearch: () =>
        React.useSyncExternalStore(
          (cb) => {
            routerState.listeners.add(cb)
            return () => routerState.listeners.delete(cb)
          },
          () => routerState.search
        ),
      useNavigate: () => (opts: { search: unknown }) => {
        const next =
          typeof opts.search === 'function'
            ? (opts.search as (p: unknown) => Record<string, unknown>)(
                routerState.search
              )
            : (opts.search as Record<string, unknown>)
        routerState.search = Object.fromEntries(
          Object.entries(next).filter(([, v]) => v !== undefined)
        )
        routerState.listeners.forEach((l) => l())
      },
    }),
  }
})

const STATUS = { quota_per_unit: 500000, quota_display_type: 'USD' }

function user(over: Record<string, unknown> = {}) {
  return {
    id: 7,
    tenant_id: 'tenant-a',
    username: 'alice',
    display_name: 'Alice',
    email: 'alice@example.com',
    role: 1,
    status: 1,
    quota: 1_000_000,
    used_quota: 250_000,
    request_count: 12,
    group: 'vip',
    remark: '',
    DeletedAt: null,
    ...over,
  }
}

function pageOf(items: unknown[]) {
  return { items, total: items.length, page: 1, page_size: 20 }
}

/** Route api.get by URL; unknown URLs answer an empty list. */
function serve(handlers: Record<string, unknown>) {
  apiGet.mockImplementation((url: string) => {
    if (url === '/api/status') return Promise.resolve(STATUS)
    if (url === '/api/group/') return Promise.resolve(['default', 'vip'])
    const v = handlers[url]
    if (v instanceof Error) return Promise.reject(v)
    if (v === undefined) return Promise.resolve(pageOf([]))
    return Promise.resolve(v)
  })
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <AdminUsersPage />
    </QueryClientProvider>
  )
}

async function openRowMenu(rowName: string) {
  const row = (await screen.findByText(rowName)).closest('tr') as HTMLElement
  await userEvent.click(within(row).getByRole('button', { name: 'Open menu' }))
}

beforeEach(() => {
  vi.clearAllMocks()
  routerState.search = {}
  routerState.listeners.clear()
  tenantGet.mockResolvedValue({ id: 1, role: 100, username: 'root', quota: 0, used_quota: 0, remaining_quota: 0 })
  apiPut.mockResolvedValue(undefined)
})

describe('AdminUsersPage', () => {
  it('lists users with role, group and money formatted from the status config', async () => {
    serve({ '/api/user/': pageOf([user(), user({ id: 8, username: 'bob', display_name: 'Bob', role: 10, quota: 500_000 })]) })
    renderPage()

    expect(await screen.findByText('alice')).toBeInTheDocument()
    expect(screen.getByText('bob')).toBeInTheDocument()
    expect(screen.getByText('Admin')).toBeInTheDocument()
    // quota 1,000,000 at 500,000 per unit is $2.00; never a guessed figure.
    expect(await screen.findByText('$2.00')).toBeInTheDocument()
    expect(screen.getByText('$1.00')).toBeInTheDocument()
    expect(apiGet).toHaveBeenCalledWith('/api/user/', {
      params: { p: 1, page_size: 20 },
    })
  })

  it('searches by keyword on the server after the debounce', async () => {
    serve({
      '/api/user/': pageOf([user(), user({ id: 8, username: 'bob' })]),
      '/api/user/search': pageOf([user({ id: 8, username: 'bob' })]),
    })
    renderPage()
    await screen.findByText('alice')

    await userEvent.type(
      screen.getByPlaceholderText('Filter by username, name or email...'),
      'bob'
    )

    await waitFor(() =>
      expect(apiGet).toHaveBeenCalledWith('/api/user/search', {
        params: { keyword: 'bob', group: '', p: 1, page_size: 20 },
      })
    )
    await waitFor(() => expect(screen.queryByText('alice')).not.toBeInTheDocument())
    expect(screen.getByText('bob')).toBeInTheDocument()
  })

  it('narrows the loaded page by status and says it is page-only', async () => {
    routerState.search = { status: ['2'] }
    serve({
      '/api/user/': pageOf([user(), user({ id: 8, username: 'bob', status: 2 })]),
    })
    renderPage()

    expect(await screen.findByText('bob')).toBeInTheDocument()
    expect(screen.queryByText('alice')).not.toBeInTheDocument()
    expect(
      screen.getByText(
        'Status and role filters apply to the users on this page only.'
      )
    ).toBeInTheDocument()
    // The server was not asked to filter by status.
    expect(apiGet).toHaveBeenCalledWith('/api/user/', {
      params: { p: 1, page_size: 20 },
    })
  })

  it('shows an error state with retry when the list cannot load', async () => {
    serve({ '/api/user/': new ApiError('database is down', { status: 500 }) })
    renderPage()

    expect(await screen.findByText('Failed to load users')).toBeInTheDocument()
    expect(screen.getByText('database is down')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  it('disables a user by re-reading the live row and sending every field back', async () => {
    serve({
      '/api/user/': pageOf([user()]),
      // The balance moved since the table loaded: the write must carry this.
      '/api/user/7': user({ quota: 1_234_567, remark: 'vip customer' }),
    })
    renderPage()

    await openRowMenu('alice')
    await userEvent.click(await screen.findByRole('menuitem', { name: /^Disable/ }))

    await waitFor(() => expect(apiPut).toHaveBeenCalled())
    expect(apiPut).toHaveBeenCalledWith('/api/user/', {
      id: 7,
      username: 'alice',
      display_name: 'Alice',
      group: 'vip',
      quota: 1_234_567,
      remark: 'vip customer',
      role: 1,
      status: 2,
    })
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('User disabled'))
  })

  it('surfaces the server message when the update is refused', async () => {
    serve({
      '/api/user/': pageOf([user()]),
      '/api/user/7': user(),
    })
    apiPut.mockRejectedValue(new ApiError('无权更新同权限等级或更高权限等级的用户信息'))
    renderPage()

    await openRowMenu('alice')
    await userEvent.click(await screen.findByRole('menuitem', { name: /^Disable/ }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        '无权更新同权限等级或更高权限等级的用户信息'
      )
    )
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('adds money to the balance in quota units computed from the live row', async () => {
    serve({
      '/api/user/': pageOf([user()]),
      '/api/user/7': user({ quota: 1_000_000 }),
    })
    renderPage()

    await openRowMenu('alice')
    await userEvent.click(await screen.findByRole('menuitem', { name: /^Adjust Quota/ }))
    const amount = await screen.findByLabelText('Amount ($)')
    await userEvent.type(amount, '2')
    await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))

    // $2 at 500,000 per unit is 1,000,000 quota on top of 1,000,000.
    await waitFor(() =>
      expect(apiPut).toHaveBeenCalledWith(
        '/api/user/',
        expect.objectContaining({ id: 7, quota: 2_000_000 })
      )
    )
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Quota adjusted successfully')
    )
  })

  it('refuses a subtraction that would take the balance below zero', async () => {
    serve({
      '/api/user/': pageOf([user()]),
      '/api/user/7': user({ quota: 500_000 }),
    })
    renderPage()

    await openRowMenu('alice')
    await userEvent.click(await screen.findByRole('menuitem', { name: /^Adjust Quota/ }))
    await userEvent.click(await screen.findByRole('button', { name: 'Subtract' }))
    await userEvent.type(await screen.findByLabelText('Amount ($)'), '5')

    expect(await screen.findByText('The balance cannot go below zero')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Confirm' })).toBeDisabled()
    expect(apiPut).not.toHaveBeenCalled()
  })

  it('hides delete and 2FA reset from a non-root admin', async () => {
    tenantGet.mockResolvedValue({ id: 2, role: 10, username: 'ops', quota: 0, used_quota: 0, remaining_quota: 0 })
    serve({ '/api/user/': pageOf([user()]) })
    renderPage()

    await openRowMenu('alice')
    await screen.findByRole('menuitem', { name: /^Adjust Quota/ })
    expect(screen.queryByRole('menuitem', { name: /^Delete/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: /^Reset 2FA/ })).not.toBeInTheDocument()
  })

  it('deletes a user through the platform admin route for root', async () => {
    serve({ '/api/user/': pageOf([user()]) })
    apiDelete.mockResolvedValue(undefined)
    renderPage()

    await openRowMenu('alice')
    await userEvent.click(await screen.findByRole('menuitem', { name: /^Delete/ }))
    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(apiDelete).toHaveBeenCalledWith('/api/v2/admin/users/7'))
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('User deleted successfully'))
  })

  it('requires a reason before resetting two-factor and sends it', async () => {
    serve({ '/api/user/': pageOf([user()]) })
    apiPost.mockResolvedValue({ notified: true })
    renderPage()

    await openRowMenu('alice')
    await userEvent.click(await screen.findByRole('menuitem', { name: /^Reset 2FA/ }))
    const dialog = await screen.findByRole('dialog')
    const submit = within(dialog).getByRole('button', { name: 'Reset 2FA' })
    expect(submit).toBeDisabled()

    await userEvent.type(within(dialog).getByLabelText('Reason'), 'lost phone')
    await userEvent.click(submit)

    await waitFor(() =>
      expect(apiPost).toHaveBeenCalledWith(
        '/api/v2/admin/security/users/7/totp/force-disable',
        { reason: 'lost phone' }
      )
    )
  })
})
