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

import { ApiError } from '@/lib/api'

import { KeysPage } from './index'

const { get, post, put, del, statusGet } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
  statusGet: vi.fn(),
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

beforeEach(() => {
  vi.clearAllMocks()
  statusGet.mockResolvedValue(STATUS)
})

describe('KeysPage', () => {
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

  it('keeps a wide table inside its own scroll container (no page overflow at 390px)', async () => {
    route({ '/tokens': page([row()]) })
    renderPage()
    const cell = await screen.findByText('prod-backend')
    const table = cell.closest('table')
    const scroller = table?.closest('[data-slot="table-container"]')
    expect(scroller).not.toBeNull()
    expect(scroller).toHaveClass('overflow-x-auto')
    // Grid items default to min-width:auto, so without min-w-0 the table's
    // min-content width would stretch the page instead of scrolling.
    expect(screen.getByTestId('keys-table-box')).toHaveClass('min-w-0')
    expect(screen.getByTestId('keys-body')).toHaveClass('min-w-0')
  })

  it('a limited key shows used / total from the unit price', async () => {
    route({
      '/tokens': page([
        row({ unlimited_quota: false, used_quota: 250000, remain_quota: 750000 }),
      ]),
    })
    renderPage()
    expect(await screen.findByText('$0.50 / $2.00')).toBeInTheDocument()
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

  it('creating a key shows the one-time secret', async () => {
    route({ '/tokens': page([row()]) })
    post.mockResolvedValue({ id: 9, name: 'new', key: 'sk-FULLSECRET' })
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('prod-backend')
    await user.click(screen.getByRole('button', { name: /Create key/ }))
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'new')
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

  it('does not submit a create form without a name', async () => {
    route({ '/tokens': page([row()]) })
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('prod-backend')
    await user.click(screen.getByRole('button', { name: /Create key/ }))
    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Enter a name for the key.'
    )
    expect(post).not.toHaveBeenCalled()
  })

  it('deleting asks first, then calls DELETE', async () => {
    route({ '/tokens': page([row()]) })
    del.mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('prod-backend')
    await user.click(screen.getByRole('button', { name: 'Delete prod-backend' }))
    expect(del).not.toHaveBeenCalled()
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(del).toHaveBeenCalledWith('/tokens/1'))
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
})
