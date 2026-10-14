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

import { KeysPage } from './index'

const { get, post, put, del, statusGet, toastError, toastSuccess } = vi.hoisted(
  () => ({
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    del: vi.fn(),
    statusGet: vi.fn(),
    toastError: vi.fn(),
    toastSuccess: vi.fn(),
  })
)

vi.mock('sonner', () => ({
  toast: { error: toastError, success: toastSuccess },
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    tenantApi: { get, post, put, delete: del },
    api: { get: statusGet, post: vi.fn(), put: vi.fn(), delete: vi.fn() },
  }
})

const STATUS = { quota_per_unit: 500000, quota_display_type: 'USD' }

function row(over: Record<string, unknown> = {}) {
  return {
    id: 1,
    name: 'prod-backend',
    key: 'sk-ab****yz',
    status: 1,
    created_time: 1_700_000_000,
    accessed_time: 0,
    expired_time: -1,
    remain_quota: 0,
    used_quota: 250000,
    unlimited_quota: true,
    model_limits_enabled: false,
    model_limits: '',
    allow_ips: null,
    project_id: 0,
    ...over,
  }
}

function page(items: unknown[], total = items.length) {
  return { items, total, page: 1, page_size: 20 }
}

function route(handlers: Record<string, unknown>) {
  get.mockImplementation((path: string) => {
    const v = handlers[path]
    if (v instanceof Error) return Promise.reject(v)
    if (v === undefined) return Promise.resolve({ items: [] })
    return Promise.resolve(v)
  })
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <KeysPage />
    </QueryClientProvider>
  )
}

const TWO_KEYS = [
  row(),
  row({ id: 2, name: 'old-job', status: 2, key: 'sk-cd****wx' }),
]

beforeEach(() => {
  vi.clearAllMocks()
  window.localStorage.clear()
  statusGet.mockResolvedValue(STATUS)
})

describe('KeysPage list', () => {
  it('renders rows with masked key, unlimited quota and never-expires', async () => {
    route({ '/tokens': page([row()]) })
    renderPage()
    expect(await screen.findByText('prod-backend')).toBeInTheDocument()
    expect(screen.getByText('sk-ab****yz')).toBeInTheDocument()
    expect(screen.getByText('Unlimited')).toBeInTheDocument()
    expect(screen.getByText('Never')).toBeInTheDocument()
    expect(screen.getByText('Enabled')).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith('/tokens', {
      params: { p: 1, size: 20 },
    })
  })

  it('never offers to reveal or copy an existing key', async () => {
    route({ '/tokens': page([row()]) })
    renderPage()
    await screen.findByText('prod-backend')
    expect(screen.queryByRole('button', { name: /copy/i })).toBeNull()
  })

  it('a limited key shows remaining and used from the unit price', async () => {
    route({
      '/tokens': page([
        row({ unlimited_quota: false, used_quota: 250000, remain_quota: 750000 }),
      ]),
    })
    renderPage()
    expect(await screen.findByText('$1.50 left')).toBeInTheDocument()
    expect(screen.getByText('$0.50 used')).toBeInTheDocument()
    expect(screen.getByRole('progressbar')).toBeInTheDocument()
  })

  it('shows the project name and a past expiry as expired', async () => {
    route({
      '/tokens': page([
        row({ project_id: 7, expired_time: 1_000_000_000 }),
      ]),
      '/projects': { items: [{ id: 7, name: 'Alpha' }] },
    })
    renderPage()
    expect(await screen.findByText('Alpha')).toBeInTheDocument()
    expect(screen.getByText('Expired')).toBeInTheDocument()
  })

  it('shows the empty state only for a genuinely empty list', async () => {
    route({ '/tokens': page([]) })
    renderPage()
    expect(await screen.findByText('No API keys yet')).toBeInTheDocument()
  })

  it('a failed read is an error with retry, not an empty list', async () => {
    route({ '/tokens': new ApiError('boom', { status: 500 }) })
    renderPage()
    expect(await screen.findByText('Could not load your keys')).toBeInTheDocument()
    expect(screen.getByText('boom')).toBeInTheDocument()
    expect(screen.queryByText('No API keys yet')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  it('asks the server for the next page', async () => {
    route({ '/tokens': page([row()], 45) })
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('prod-backend')
    await user.click(await screen.findByRole('button', { name: 'Go to next page' }))
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith('/tokens', {
        params: { p: 2, size: 20 },
      })
    )
  })

  it('renders a card list on a phone-sized screen', async () => {
    const original = window.matchMedia
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: (query: string) => ({
        ...original(query),
        matches: query.includes('max-width: 640px'),
      }),
    })
    try {
      route({ '/tokens': page(TWO_KEYS) })
      renderPage()
      const list = await screen.findByTestId('keys-mobile-list')
      expect(within(list).getByText('prod-backend')).toBeInTheDocument()
      expect(within(list).getByText('old-job')).toBeInTheDocument()
      expect(
        within(list).getByRole('switch', { name: 'Disable prod-backend' })
      ).toBeInTheDocument()
    } finally {
      Object.defineProperty(window, 'matchMedia', {
        configurable: true,
        value: original,
      })
    }
  })
})

describe('KeysPage filters', () => {
  it('narrows the loaded page by name', async () => {
    route({ '/tokens': page(TWO_KEYS) })
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('old-job')
    await user.type(screen.getByPlaceholderText('Filter by name...'), 'old')
    await waitFor(() =>
      expect(screen.queryByText('prod-backend')).not.toBeInTheDocument()
    )
    expect(screen.getByText('old-job')).toBeInTheDocument()
    expect(
      screen.getByText('Search and status filters apply to the keys on this page.')
    ).toBeInTheDocument()
  })

  it('narrows the loaded page by status', async () => {
    route({ '/tokens': page(TWO_KEYS) })
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('old-job')
    await user.click(screen.getByRole('button', { name: /Status/ }))
    await user.click(await screen.findByRole('option', { name: /Disabled/ }))
    await waitFor(() =>
      expect(screen.queryByText('prod-backend')).not.toBeInTheDocument()
    )
    expect(screen.getByText('old-job')).toBeInTheDocument()
  })
})

describe('KeysPage mutations', () => {
  it('creating a key shows the one-time secret', async () => {
    route({ '/tokens': page([row()]) })
    post.mockResolvedValue({ id: 9, name: 'new', key: 'sk-FULLSECRET' })
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('prod-backend')
    await user.click(screen.getByRole('button', { name: /Create key/ }))
    await user.type(await screen.findByRole('textbox', { name: 'Name' }), 'new')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByTestId('issued-key')).toHaveTextContent(
      'sk-FULLSECRET'
    )
    expect(post).toHaveBeenCalledWith(
      '/tokens',
      expect.objectContaining({
        name: 'new',
        unlimited_quota: true,
        remain_quota: 0,
        expired_time: -1,
      })
    )
  })

  it('a rejected create keeps the drawer open and reports the reason', async () => {
    route({ '/tokens': page([row()]) })
    post.mockRejectedValue(new ApiError('quota too high', { status: 400 }))
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('prod-backend')
    await user.click(screen.getByRole('button', { name: /Create key/ }))
    await user.type(await screen.findByRole('textbox', { name: 'Name' }), 'x')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('quota too high'))
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveValue('x')
    expect(screen.queryByTestId('issued-key')).toBeNull()
  })

  it('does not submit a create form without a name', async () => {
    route({ '/tokens': page([row()]) })
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('prod-backend')
    await user.click(screen.getByRole('button', { name: /Create key/ }))
    await user.click(await screen.findByRole('button', { name: 'Create' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Enter a name for the key.'
    )
    expect(post).not.toHaveBeenCalled()
  })

  it('editing sends the whole form to the key it was opened for', async () => {
    route({ '/tokens': page([row()]) })
    put.mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('prod-backend')
    await user.click(screen.getByRole('button', { name: 'Edit prod-backend' }))
    const name = await screen.findByRole('textbox', { name: 'Name' })
    expect(name).toHaveValue('prod-backend')
    await user.clear(name)
    await user.type(name, 'renamed')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(
        '/tokens/1',
        expect.objectContaining({
          name: 'renamed',
          unlimited_quota: true,
          expired_time: -1,
        })
      )
    )
  })

  it('deleting asks first, then calls DELETE', async () => {
    route({ '/tokens': page([row()]) })
    del.mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('prod-backend')
    await user.click(
      screen.getByRole('button', { name: 'More actions for prod-backend' })
    )
    await user.click(await screen.findByRole('menuitem', { name: /Delete key/ }))
    expect(del).not.toHaveBeenCalled()
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(del).toHaveBeenCalledWith('/tokens/1'))
  })

  it('a failed delete reports the error and keeps the key', async () => {
    route({ '/tokens': page([row()]) })
    del.mockRejectedValue(new ApiError('in use', { status: 409 }))
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('prod-backend')
    await user.click(
      screen.getByRole('button', { name: 'More actions for prod-backend' })
    )
    await user.click(await screen.findByRole('menuitem', { name: /Delete key/ }))
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('in use'))
    expect(screen.getByText('prod-backend')).toBeInTheDocument()
  })

  it('rotating confirms, then shows the new one-time secret', async () => {
    route({ '/tokens': page([row()]) })
    post.mockResolvedValue({ id: 1, key: 'sk-ROTATED' })
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('prod-backend')
    await user.click(
      screen.getByRole('button', { name: 'More actions for prod-backend' })
    )
    await user.click(await screen.findByRole('menuitem', { name: /Rotate key/ }))
    expect(post).not.toHaveBeenCalled()
    await user.click(await screen.findByRole('button', { name: 'Rotate' }))
    expect(await screen.findByTestId('issued-key')).toHaveTextContent(
      'sk-ROTATED'
    )
    expect(post).toHaveBeenCalledWith('/tokens/1/rotate')
  })

  it('disabling sends only the status change', async () => {
    route({ '/tokens': page([row()]) })
    put.mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('prod-backend')
    await user.click(screen.getByRole('switch', { name: 'Disable prod-backend' }))
    await waitFor(() => expect(put).toHaveBeenCalledWith('/tokens/1', { status: 2 }))
  })

  it('a failed toggle surfaces the error', async () => {
    route({ '/tokens': page([row()]) })
    put.mockRejectedValue(new ApiError('not allowed', { status: 403 }))
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('prod-backend')
    await user.click(screen.getByRole('switch', { name: 'Disable prod-backend' }))
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('not allowed'))
  })

  it('bulk delete sends the selected ids after confirmation', async () => {
    route({ '/tokens': page(TWO_KEYS) })
    post.mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('old-job')
    await user.click(screen.getByRole('checkbox', { name: 'Select prod-backend' }))
    await user.click(screen.getByRole('checkbox', { name: 'Select old-job' }))
    await user.click(
      await screen.findByRole('button', { name: 'Delete selected keys' })
    )
    expect(post).not.toHaveBeenCalled()
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/tokens/batch-delete', { ids: [1, 2] })
    )
  })
})
