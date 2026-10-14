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

import { RedemptionCodesPage } from './index'

const {
  apiGet,
  apiPut,
  apiDelete,
  tenantGet,
  tenantPost,
  tenantDelete,
  routerState,
  toast,
} = vi.hoisted(() => ({
  apiGet: vi.fn(),
  apiPut: vi.fn(),
  apiDelete: vi.fn(),
  tenantGet: vi.fn(),
  tenantPost: vi.fn(),
  tenantDelete: vi.fn(),
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
    api: { get: apiGet, put: apiPut, post: vi.fn(), delete: apiDelete },
    tenantApi: {
      get: tenantGet,
      post: tenantPost,
      put: vi.fn(),
      delete: tenantDelete,
    },
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
const MASKED = 'abcd************************wxyz'
const FULL_KEY = 'k'.repeat(32)

function code(over: Record<string, unknown> = {}) {
  return {
    id: 1,
    name: 'spring-promo',
    key: MASKED,
    status: 1,
    quota: 1_000_000,
    created_time: 1_700_000_000,
    expired_time: 0,
    redeemed_time: 0,
    used_user_id: 0,
    ...over,
  }
}

function pageOf(items: unknown[]) {
  return { redemptions: items, total: items.length, page: 1, page_size: 20 }
}

function serve(handlers: Record<string, unknown>) {
  apiGet.mockImplementation((url: string) =>
    url === '/api/status' ? Promise.resolve(STATUS) : Promise.resolve(null)
  )
  tenantGet.mockImplementation((path: string) => {
    if (path === '/user/me') {
      return Promise.resolve({
        id: 1,
        role: 10,
        username: 'ops',
        quota: 0,
        used_quota: 0,
        remaining_quota: 0,
      })
    }
    const v = handlers[path]
    if (v instanceof Error) return Promise.reject(v)
    return Promise.resolve(v ?? pageOf([]))
  })
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <RedemptionCodesPage />
    </QueryClientProvider>
  )
}

async function openRowMenu(name: string) {
  const row = (await screen.findByText(name)).closest('tr') as HTMLElement
  await userEvent.click(within(row).getByRole('button', { name: 'Open menu' }))
}

beforeEach(() => {
  vi.clearAllMocks()
  routerState.search = {}
  routerState.listeners.clear()
  apiPut.mockResolvedValue(undefined)
})

describe('RedemptionCodesPage', () => {
  it('lists codes masked, with money from the status config and the expiry', async () => {
    serve({
      '/redemptions': pageOf([
        code(),
        code({
          id: 2,
          name: 'redeemed-batch',
          key: FULL_KEY,
          status: 3,
          quota: 500_000,
          used_user_id: 42,
          redeemed_time: 1_700_100_000,
        }),
      ]),
    })
    renderPage()

    expect(await screen.findByText('spring-promo')).toBeInTheDocument()
    expect(screen.getByText(MASKED)).toBeInTheDocument()
    // The full value of an unused code is never rendered.
    expect(screen.queryByText(FULL_KEY)).not.toBeInTheDocument()
    expect(await screen.findByText('$2.00')).toBeInTheDocument()
    expect(screen.getByText('$1.00')).toBeInTheDocument()
    expect(screen.getByText('Unused')).toBeInTheDocument()
    expect(screen.getByText('Used')).toBeInTheDocument()
    expect(screen.getAllByText('Never')).toHaveLength(2)
    expect(tenantGet).toHaveBeenCalledWith('/redemptions', {
      params: { page: 1, page_size: 20 },
    })
  })

  it('tells a non-staff tenant admin why the page is closed instead of failing with 403', async () => {
    serve({})
    tenantGet.mockImplementation((path: string) =>
      path === '/user/me'
        ? Promise.resolve({ id: 3, role: 1, tenant_role: 'admin', username: 'lead', quota: 0, used_quota: 0, remaining_quota: 0 })
        : Promise.resolve(pageOf([]))
    )
    renderPage()

    expect(await screen.findByText('Administrator access required')).toBeInTheDocument()
    expect(tenantGet).not.toHaveBeenCalledWith('/redemptions', expect.anything())
  })

  it('searches by name on the server after the debounce', async () => {
    serve({ '/redemptions': pageOf([code(), code({ id: 2, name: 'winter' })]) })
    renderPage()
    await screen.findByText('spring-promo')

    await userEvent.type(
      screen.getByPlaceholderText('Filter by name or ID...'),
      'winter'
    )

    await waitFor(() =>
      expect(tenantGet).toHaveBeenCalledWith('/redemptions', {
        params: { page: 1, page_size: 20, keyword: 'winter' },
      })
    )
  })

  it('narrows the loaded page by status and says so', async () => {
    routerState.search = { status: ['3'] }
    serve({
      '/redemptions': pageOf([
        code(),
        code({ id: 2, name: 'redeemed-batch', status: 3, key: FULL_KEY }),
      ]),
    })
    renderPage()

    expect(await screen.findByText('redeemed-batch')).toBeInTheDocument()
    expect(screen.queryByText('spring-promo')).not.toBeInTheDocument()
    expect(
      screen.getByText('The status filter applies to the codes on this page only.')
    ).toBeInTheDocument()
  })

  it('shows an error state when the list cannot load', async () => {
    serve({ '/redemptions': new ApiError('boom', { status: 500 }) })
    renderPage()

    expect(await screen.findByText('Failed to load redemption codes')).toBeInTheDocument()
    expect(screen.getByText('boom')).toBeInTheDocument()
  })

  it('creates a batch in quota units and shows the full codes once', async () => {
    serve({ '/redemptions': pageOf([]) })
    tenantPost.mockResolvedValue({
      codes: [
        { id: 10, key: FULL_KEY, name: 'launch' },
        { id: 11, key: 'z'.repeat(32), name: 'launch' },
      ],
      count: 2,
      requested: 2,
    })
    renderPage()

    await userEvent.click(await screen.findByRole('button', { name: 'Create Code' }))
    const name = await screen.findByLabelText('Name')
    await userEvent.type(name, 'launch')
    const quota = screen.getByLabelText('Quota ($)')
    await userEvent.clear(quota)
    await userEvent.type(quota, '10')
    const count = screen.getByLabelText('Quantity')
    await userEvent.clear(count)
    await userEvent.type(count, '2')
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

    // $10 at 500,000 quota per unit.
    await waitFor(() =>
      expect(tenantPost).toHaveBeenCalledWith('/redemptions', {
        name: 'launch',
        quota: 5_000_000,
        count: 2,
        expired_time: 0,
      })
    )
    const dialog = await screen.findByRole('dialog', { name: 'Redemption codes created' })
    expect(within(dialog).getByRole('alert')).toHaveTextContent('shown only once')
    expect((within(dialog).getByLabelText('Redemption Code') as HTMLTextAreaElement).value).toBe(
      `${FULL_KEY}\n${'z'.repeat(32)}`
    )
  })

  it('reports a partly created batch instead of claiming success', async () => {
    serve({ '/redemptions': pageOf([]) })
    tenantPost.mockResolvedValue({
      codes: [{ id: 10, key: FULL_KEY, name: 'launch' }],
      count: 1,
      requested: 3,
    })
    renderPage()

    await userEvent.click(await screen.findByRole('button', { name: 'Create Code' }))
    await userEvent.type(await screen.findByLabelText('Name'), 'launch')
    const count = screen.getByLabelText('Quantity')
    await userEvent.clear(count)
    await userEvent.type(count, '3')
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

    await waitFor(() =>
      expect(toast.warning).toHaveBeenCalledWith(
        'Only 1 of 3 redemption codes were created'
      )
    )
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('keeps the drawer and shows the server message when create fails', async () => {
    serve({ '/redemptions': pageOf([]) })
    tenantPost.mockRejectedValue(new ApiError('Quota value exceeds maximum allowed', { status: 400 }))
    renderPage()

    await userEvent.click(await screen.findByRole('button', { name: 'Create Code' }))
    await userEvent.type(await screen.findByLabelText('Name'), 'launch')
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Quota value exceeds maximum allowed')
    )
    expect(screen.getByLabelText('Name')).toBeInTheDocument()
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('blocks an empty name before any request is sent', async () => {
    serve({ '/redemptions': pageOf([]) })
    renderPage()

    await userEvent.click(await screen.findByRole('button', { name: 'Create Code' }))
    await screen.findByLabelText('Name')
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

    expect(await screen.findByText('Enter a name')).toBeInTheDocument()
    expect(tenantPost).not.toHaveBeenCalled()
  })

  it('disables an unused code through the status route', async () => {
    serve({ '/redemptions': pageOf([code()]) })
    renderPage()

    await openRowMenu('spring-promo')
    await userEvent.click(await screen.findByRole('menuitem', { name: /^Disable/ }))

    await waitFor(() =>
      expect(apiPut).toHaveBeenCalledWith('/api/redemption/?status_only=true', {
        id: 1,
        status: 2,
      })
    )
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Redemption code disabled successfully')
    )
  })

  it('keeps an untouched amount exactly as stored when editing the name', async () => {
    serve({ '/redemptions': pageOf([code({ quota: 1_234_567 })]) })
    renderPage()

    const row = (await screen.findByText('spring-promo')).closest('tr') as HTMLElement
    await userEvent.click(within(row).getByRole('button', { name: 'Edit' }))
    const name = await screen.findByLabelText('Name')
    await userEvent.clear(name)
    await userEvent.type(name, 'summer')
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

    await waitFor(() =>
      expect(apiPut).toHaveBeenCalledWith('/api/redemption/', {
        id: 1,
        name: 'summer',
        quota: 1_234_567,
        expired_time: 0,
      })
    )
  })

  it('deletes one code through the tenant route', async () => {
    serve({ '/redemptions': pageOf([code()]) })
    tenantDelete.mockResolvedValue(undefined)
    renderPage()

    await openRowMenu('spring-promo')
    await userEvent.click(await screen.findByRole('menuitem', { name: /^Delete/ }))
    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(tenantDelete).toHaveBeenCalledWith('/redemptions/1'))
  })

  it('deletes selected codes and reports the ones that failed', async () => {
    serve({
      '/redemptions': pageOf([code(), code({ id: 2, name: 'second' }), code({ id: 3, name: 'third' })]),
    })
    tenantDelete.mockImplementation((path: string) =>
      path === '/redemptions/2'
        ? Promise.reject(new ApiError('Access denied', { status: 403 }))
        : Promise.resolve(undefined)
    )
    renderPage()
    await screen.findByText('spring-promo')

    await userEvent.click(screen.getByRole('checkbox', { name: 'Select all' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Delete selected redemption codes' }))
    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(tenantDelete).toHaveBeenCalledTimes(3))
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        'Deleted 2 of 3 redemption codes',
        { description: 'Access denied' }
      )
    )
  })

  it('sweeps invalid codes and reports the count', async () => {
    serve({ '/redemptions': pageOf([code()]) })
    apiDelete.mockResolvedValue(4)
    renderPage()

    await userEvent.click(await screen.findByRole('button', { name: 'Delete Invalid' }))
    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete Invalid' }))

    await waitFor(() => expect(apiDelete).toHaveBeenCalledWith('/api/redemption/invalid'))
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith(
        'Successfully deleted 4 invalid redemption codes'
      )
    )
  })
})
