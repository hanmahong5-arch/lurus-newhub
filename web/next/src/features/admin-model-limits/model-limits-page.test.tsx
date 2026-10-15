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

import { fetchTenantOptions } from './api'
import { AdminModelLimitsPage } from './index'

const { get, put, del, toastError, toastSuccess } = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
  toastError: vi.fn(),
  toastSuccess: vi.fn(),
}))

vi.mock('sonner', () => ({
  toast: { error: toastError, success: toastSuccess },
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: { get, post: vi.fn(), put, delete: del },
  }
})

const LIMITS_URL = '/api/v2/admin/tenants/default/model-limits'
const ALLOWLIST_URL = '/api/v2/admin/tenants/default/model-allowlist'

function row(over: Record<string, unknown> = {}) {
  return {
    id: 3,
    tenant_id: 'default',
    model: 'model-a',
    rate_limit_rpm: 60,
    rate_limit_tpm: 1000,
    updated_at: '2026-07-11T00:00:00Z',
    ...over,
  }
}

function allowlist(over: Record<string, unknown> = {}) {
  return {
    configured: false,
    allowed_models: [] as string[],
    mode: 'observe',
    ...over,
  }
}

const TENANTS = {
  tenants: [{ id: 'default', name: 'default' }],
  total: 1,
}

type Outcome = unknown | Error | (() => unknown)

/** Route api.get by URL; an Error value rejects, anything else resolves. */
function route(handlers: {
  limits?: Outcome
  allowlist?: Outcome
  tenants?: Outcome
}) {
  const settle = (o: Outcome) => {
    const v = typeof o === 'function' ? (o as () => unknown)() : o
    if (v instanceof Error) return Promise.reject(v)
    return v instanceof Promise ? v : Promise.resolve(v)
  }
  get.mockImplementation((url: string) => {
    if (url.includes('/model-allowlist')) {
      return settle(handlers.allowlist ?? allowlist())
    }
    if (url.includes('/model-limits')) return settle(handlers.limits ?? [])
    return settle(handlers.tenants ?? TENANTS)
  })
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <AdminModelLimitsPage />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('rate limits table', () => {
  it('renders the tenant model limits from GET', async () => {
    route({ limits: [row()] })
    renderPage()

    const r = await screen.findByTestId('limit-row-3')
    expect(within(r).getByText('model-a')).toBeInTheDocument()
    expect(within(r).getByText('60')).toBeInTheDocument()
    expect(within(r).getByText('1,000')).toBeInTheDocument()
    expect(screen.getByText('1 model limits')).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith(LIMITS_URL)
  })

  it('renders "Unlimited" for a zero cap and the number for the other', async () => {
    route({ limits: [row({ rate_limit_rpm: 0, rate_limit_tpm: 500 })] })
    renderPage()

    const r = await screen.findByTestId('limit-row-3')
    expect(within(r).getByText('Unlimited')).toBeInTheDocument()
    expect(within(r).getByText('500')).toBeInTheDocument()
  })

  it('shows the empty state for a tenant that really has no limits', async () => {
    route({ limits: [] })
    renderPage()

    expect(
      await screen.findByText('No per-model limits set for this tenant.')
    ).toBeInTheDocument()
    expect(screen.getByText('0 model limits')).toBeInTheDocument()
  })

  it('treats a null body as no limits instead of crashing', async () => {
    route({ limits: null })
    renderPage()
    expect(
      await screen.findByText('No per-model limits set for this tenant.')
    ).toBeInTheDocument()
  })
})

describe('create and edit', () => {
  it('creates a limit and PUTs model, rpm and tpm', async () => {
    route({ limits: [] })
    put.mockResolvedValue(row())
    const user = userEvent.setup()
    renderPage()

    await user.click(
      await screen.findByRole('button', { name: 'New model limit' })
    )
    const dialog = await screen.findByRole('dialog', {
      name: 'New model limit',
    })
    await user.type(within(dialog).getByLabelText('Model name'), 'model-b')
    await user.type(within(dialog).getByLabelText('RPM limit'), '120')
    await user.type(within(dialog).getByLabelText('TPM limit'), '4000')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(LIMITS_URL, {
        model: 'model-b',
        rate_limit_rpm: 120,
        rate_limit_tpm: 4000,
      })
    )
    expect(toastSuccess).toHaveBeenCalledWith('Model limit saved')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('blank or negative caps are sent as 0 (unlimited); the model is trimmed', async () => {
    route({ limits: [] })
    put.mockResolvedValue(row())
    const user = userEvent.setup()
    renderPage()

    await user.click(
      await screen.findByRole('button', { name: 'New model limit' })
    )
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('Model name'), '  model-b  ')
    await user.type(within(dialog).getByLabelText('RPM limit'), '-5')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(LIMITS_URL, {
        model: 'model-b',
        rate_limit_rpm: 0,
        rate_limit_tpm: 0,
      })
    )
  })

  it('cannot save without a model name', async () => {
    route({ limits: [] })
    const user = userEvent.setup()
    renderPage()

    await user.click(
      await screen.findByRole('button', { name: 'New model limit' })
    )
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('button', { name: 'Save' })).toBeDisabled()
    await user.type(within(dialog).getByLabelText('Model name'), '   ')
    expect(within(dialog).getByRole('button', { name: 'Save' })).toBeDisabled()
    expect(put).not.toHaveBeenCalled()
  })

  it('the dialog is a labelled modal; Escape closes it without saving', async () => {
    route({ limits: [] })
    const user = userEvent.setup()
    renderPage()

    await user.click(
      await screen.findByRole('button', { name: 'New model limit' })
    )
    const dialog = await screen.findByRole('dialog', {
      name: 'New model limit',
    })
    expect(within(dialog).getByLabelText('Model name')).toBeInTheDocument()

    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(put).not.toHaveBeenCalled()
  })

  it('edits an existing row with the model field locked', async () => {
    route({ limits: [row()] })
    put.mockResolvedValue(row())
    const user = userEvent.setup()
    renderPage()

    await user.click(
      await screen.findByRole('button', { name: 'Edit model-a' })
    )
    const dialog = await screen.findByRole('dialog', {
      name: 'Edit model limit',
    })
    // The model is the row key: renaming would orphan the old row.
    expect(within(dialog).getByLabelText('Model name')).toBeDisabled()
    expect(within(dialog).getByLabelText('Model name')).toHaveValue('model-a')

    const rpm = within(dialog).getByLabelText('RPM limit')
    await user.clear(rpm)
    await user.type(rpm, '90')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(
        LIMITS_URL,
        expect.objectContaining({ model: 'model-a', rate_limit_rpm: 90 })
      )
    )
  })

  it('keeps the dialog open and reports the server message when the save fails', async () => {
    route({ limits: [] })
    put.mockRejectedValue(
      new ApiError('speed limits exceed the allowed range', { status: 400 })
    )
    const user = userEvent.setup()
    renderPage()

    await user.click(
      await screen.findByRole('button', { name: 'New model limit' })
    )
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('Model name'), 'model-b')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith(
        'speed limits exceed the allowed range'
      )
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(toastSuccess).not.toHaveBeenCalled()
  })
})

describe('delete', () => {
  it('stays locked until the model name is typed, then DELETEs with ?model=', async () => {
    route({ limits: [row({ model: 'vendor-a/model-a' })] })
    del.mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderPage()

    // The test i18n instance escapes interpolated values ("/" -> "&#x2F;");
    // the app's config turns that off, so match on the stable prefix.
    await user.click(
      await screen.findByRole('button', { name: /^Delete vendor-a/ })
    )
    const dialog = await screen.findByRole('alertdialog')
    const confirm = within(dialog).getByRole('button', { name: 'Delete' })
    expect(confirm).toBeDisabled()

    await user.click(confirm)
    expect(del).not.toHaveBeenCalled()

    await user.type(within(dialog).getByRole('textbox'), 'vendor-a/model-a')
    expect(confirm).toBeEnabled()
    await user.click(confirm)

    await waitFor(() =>
      expect(del).toHaveBeenCalledWith(LIMITS_URL, {
        params: { model: 'vendor-a/model-a' },
      })
    )
    expect(toastSuccess).toHaveBeenCalledWith('Model limit deleted')
  })

  it('cancelling deletes nothing and forgets what was typed', async () => {
    route({ limits: [row()] })
    const user = userEvent.setup()
    renderPage()

    await user.click(
      await screen.findByRole('button', { name: 'Delete model-a' })
    )
    let dialog = await screen.findByRole('alertdialog')
    await user.type(within(dialog).getByRole('textbox'), 'model-a')
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())

    await user.click(screen.getByRole('button', { name: 'Delete model-a' }))
    dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByRole('textbox')).toHaveValue('')
    expect(
      within(dialog).getByRole('button', { name: 'Delete' })
    ).toBeDisabled()
    expect(del).not.toHaveBeenCalled()
  })
})

describe('read failures are never an empty list', () => {
  it('shows the forbidden panel on 403 and hides the write controls', async () => {
    get.mockRejectedValue(new ApiError('forbidden', { status: 403 }))
    renderPage()

    expect(await screen.findByTestId('limits-forbidden')).toBeInTheDocument()
    expect(screen.getAllByText('Admin access required').length).toBeGreaterThan(
      0
    )
    expect(screen.queryByRole('button', { name: 'New model limit' })).toBeNull()
  })

  it('also reads the PERMISSION_DENIED error code as forbidden', async () => {
    route({
      limits: new ApiError('denied', {
        status: 200,
        code: 'PERMISSION_DENIED',
      }),
    })
    renderPage()
    expect(await screen.findByTestId('limits-forbidden')).toBeInTheDocument()
  })

  it('says the limits read failed - not that the tenant has no limits - on a 500', async () => {
    route({ limits: new ApiError('boom', { status: 500 }) })
    renderPage()

    expect(await screen.findByTestId('limits-error')).toBeInTheDocument()
    expect(screen.getByText('boom')).toBeInTheDocument()
    expect(screen.queryByText('0 model limits')).toBeNull()
    expect(screen.getByText('Count unavailable')).toBeInTheDocument()
    expect(
      screen.queryByText('No per-model limits set for this tenant.')
    ).toBeNull()
  })

  it('treats a 200 carrying success:false as a failed read', async () => {
    // The api helper folds {success:false} into an ApiError without a 4xx.
    route({ limits: new ApiError('database is starting up', { status: 200 }) })
    renderPage()

    expect(await screen.findByTestId('limits-error')).toBeInTheDocument()
    expect(screen.queryByText('0 model limits')).toBeNull()
    expect(
      screen.queryByText('No per-model limits set for this tenant.')
    ).toBeNull()
  })

  it('clears the limits error once a retry succeeds', async () => {
    let fail = true
    route({
      limits: () => (fail ? new ApiError('boom', { status: 500 }) : [row()]),
    })
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('limits-error')

    fail = false
    await user.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByTestId('limit-row-3')).toBeInTheDocument()
    expect(screen.queryByTestId('limits-error')).toBeNull()
  })

  it('keeps the default tenant usable when only the tenant list fails', async () => {
    route({ tenants: new ApiError('down', { status: 502 }), limits: [row()] })
    renderPage()

    expect(await screen.findByTestId('limit-row-3')).toBeInTheDocument()
    expect(
      await screen.findByText(/only the default tenant is selectable/)
    ).toBeInTheDocument()
    expect(screen.getByLabelText('Tenant')).toHaveValue('default')
  })
})

describe('model availability (allow-list)', () => {
  it('shows the unrestricted state when no allow-list row is configured', async () => {
    route({})
    renderPage()

    expect(
      await screen.findByTestId('allowlist-unrestricted')
    ).toBeInTheDocument()
    expect(screen.getByTestId('allowlist-mode').dataset.mode).toBe('observe')
  })

  it('renders observe as "still answers", distinct from enforce', async () => {
    route({
      allowlist: allowlist({
        configured: true,
        allowed_models: ['model-a'],
        mode: 'observe',
      }),
    })
    renderPage()

    const mode = await screen.findByTestId('allowlist-mode')
    expect(mode.dataset.mode).toBe('observe')
    expect(mode.textContent).toContain('still answers')
    expect(mode.textContent).not.toContain('refused')
  })

  it('renders enforce as "refused", distinct from observe', async () => {
    route({
      allowlist: allowlist({
        configured: true,
        allowed_models: ['model-a'],
        mode: 'enforce',
      }),
    })
    renderPage()

    const mode = await screen.findByTestId('allowlist-mode')
    expect(mode.dataset.mode).toBe('enforce')
    expect(mode.textContent).toContain('refused')
    expect(mode.textContent).not.toContain('still answers')
  })

  it('renders the configured entries', async () => {
    route({
      allowlist: allowlist({
        configured: true,
        allowed_models: ['model-a', 'model-b-*'],
      }),
    })
    renderPage()

    const list = await screen.findByTestId('allowlist-entries')
    expect(within(list).getByText('model-a')).toBeInTheDocument()
    expect(within(list).getByText('model-b-*')).toBeInTheDocument()
  })

  it('adds a draft entry and PUTs exactly allowed_models', async () => {
    route({
      allowlist: allowlist({ configured: true, allowed_models: ['model-a'] }),
    })
    put.mockResolvedValue({})
    const user = userEvent.setup()
    renderPage()

    await screen.findByText('model-a')
    await user.type(
      screen.getByLabelText('Model name or prefix pattern'),
      'model-b{Enter}'
    )
    expect(screen.getByText('model-b')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save allow-list' }))

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(ALLOWLIST_URL, {
        allowed_models: ['model-a', 'model-b'],
      })
    )
    expect(toastSuccess).toHaveBeenCalledWith('Model allow-list saved')
  })

  it('ignores a duplicate entry and rejects a wildcard anywhere but the end', async () => {
    route({
      allowlist: allowlist({ configured: true, allowed_models: ['model-a'] }),
    })
    const user = userEvent.setup()
    renderPage()

    await screen.findByText('model-a')
    const input = screen.getByLabelText('Model name or prefix pattern')
    await user.type(input, 'model-a')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    expect(
      within(screen.getByTestId('allowlist-entries')).getAllByRole('listitem')
    ).toHaveLength(1)

    await user.type(input, 'mo*del')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Use * only as a trailing wildcard'
    )
    expect(
      within(screen.getByTestId('allowlist-entries')).getAllByRole('listitem')
    ).toHaveLength(1)
  })

  it('removes a draft entry before saving', async () => {
    route({
      allowlist: allowlist({
        configured: true,
        allowed_models: ['model-a', 'model-b'],
      }),
    })
    put.mockResolvedValue({})
    const user = userEvent.setup()
    renderPage()

    await screen.findByText('model-a')
    await user.click(screen.getByRole('button', { name: 'Remove model-a' }))
    await user.click(screen.getByRole('button', { name: 'Save allow-list' }))

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(ALLOWLIST_URL, {
        allowed_models: ['model-b'],
      })
    )
  })

  it('saving an empty list needs the tenant typed first (deny-all guard)', async () => {
    route({
      allowlist: allowlist({ configured: true, allowed_models: ['model-a'] }),
    })
    put.mockResolvedValue({})
    const user = userEvent.setup()
    renderPage()

    await screen.findByText('model-a')
    await user.click(screen.getByRole('button', { name: 'Remove model-a' }))
    expect(
      screen.getByText(/Saving an empty list denies every model/)
    ).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save allow-list' }))

    // The PUT must NOT fire until the confirmation is accepted.
    expect(put).not.toHaveBeenCalled()
    const dialog = await screen.findByRole('alertdialog')
    const confirm = within(dialog).getByRole('button', { name: 'Confirm' })
    expect(confirm).toBeDisabled()
    await user.type(within(dialog).getByRole('textbox'), 'default')
    await user.click(confirm)

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(ALLOWLIST_URL, { allowed_models: [] })
    )
  })

  it('clearing needs the tenant typed, then DELETEs and refetches', async () => {
    let configured = true
    route({
      allowlist: () =>
        allowlist({
          configured,
          allowed_models: configured ? ['model-a'] : [],
          mode: 'enforce',
        }),
    })
    del.mockImplementation(() => {
      configured = false
      return Promise.resolve({})
    })
    const user = userEvent.setup()
    renderPage()

    await screen.findByText('model-a')
    await user.click(
      screen.getByRole('button', { name: 'Clear (make unrestricted)' })
    )
    expect(del).not.toHaveBeenCalled()
    const dialog = await screen.findByRole('alertdialog')
    await user.type(within(dialog).getByRole('textbox'), 'default')
    await user.click(within(dialog).getByRole('button', { name: 'Confirm' }))

    await waitFor(() => expect(del).toHaveBeenCalledWith(ALLOWLIST_URL))
    // The editor re-seeds from the server: now unrestricted, nothing listed.
    expect(
      await screen.findByTestId('allowlist-unrestricted')
    ).toBeInTheDocument()
    expect(screen.queryByText('model-a')).toBeNull()
  })

  it('the clear button is disabled when the tenant has no allow-list', async () => {
    route({})
    renderPage()

    await screen.findByTestId('allowlist-unrestricted')
    expect(
      screen.getByRole('button', { name: 'Clear (make unrestricted)' })
    ).toBeDisabled()
  })

  it('shows an explicit error - not the unrestricted defaults - when the read fails, and locks save/clear', async () => {
    route({ allowlist: new ApiError('boom', { status: 500 }) })
    renderPage()

    expect(await screen.findByTestId('allowlist-error')).toBeInTheDocument()
    // Neither the observe copy nor the "unrestricted" copy may appear: both
    // would be statements about a state this page never read.
    expect(screen.queryByTestId('allowlist-mode')).toBeNull()
    expect(screen.queryByTestId('allowlist-unrestricted')).toBeNull()
    expect(screen.queryByText(/still answers/i)).toBeNull()
    expect(screen.queryByText(/every catalog model is reachable/i)).toBeNull()
    expect(
      screen.getByRole('button', { name: 'Save allow-list' })
    ).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Clear (make unrestricted)' })
    ).toBeDisabled()
    expect(put).not.toHaveBeenCalled()
  })

  it('the allow-list error clears on retry once the read succeeds', async () => {
    let fail = true
    route({
      allowlist: () =>
        fail ? new ApiError('boom', { status: 500 }) : allowlist(),
    })
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('allowlist-error')

    fail = false
    await user.click(
      within(screen.getByTestId('allowlist-error')).getByRole('button', {
        name: 'Retry',
      })
    )

    expect(await screen.findByTestId('allowlist-mode')).toBeInTheDocument()
    expect(screen.queryByTestId('allowlist-error')).toBeNull()
  })

  it('a 403 on the allow-list read turns the whole page into the forbidden panel', async () => {
    route({ allowlist: new ApiError('no', { status: 403 }) })
    renderPage()
    expect(await screen.findByTestId('limits-forbidden')).toBeInTheDocument()
  })

  it('keeps the draft and says why when the save fails', async () => {
    route({
      allowlist: allowlist({ configured: true, allowed_models: ['model-a'] }),
    })
    put.mockRejectedValue(
      new ApiError('allowed_models contains a duplicate entry', { status: 400 })
    )
    const user = userEvent.setup()
    renderPage()

    await screen.findByText('model-a')
    await user.click(screen.getByRole('button', { name: 'Save allow-list' }))

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith(
        'allowed_models contains a duplicate entry'
      )
    )
    expect(screen.getByText('model-a')).toBeInTheDocument()
  })

  it('drops a stale allow-list response after the tenant selection has moved on', async () => {
    let resolveDefault: (v: unknown) => void = () => {}
    get.mockImplementation((url: string) => {
      if (url.includes('/tenants/default/model-allowlist')) {
        return new Promise((resolve) => {
          resolveDefault = resolve
        })
      }
      if (url.includes('/tenants/other/model-allowlist')) {
        return Promise.resolve(
          allowlist({ configured: true, allowed_models: ['other-model'] })
        )
      }
      if (url.includes('/model-limits')) return Promise.resolve([])
      return Promise.resolve({
        tenants: [
          { id: 'default', name: 'default' },
          { id: 'other', name: 'other' },
        ],
        total: 2,
      })
    })
    const user = userEvent.setup()
    renderPage()

    await waitFor(() =>
      expect(screen.getByRole('option', { name: 'other' })).toBeInTheDocument()
    )
    // Switch while the default tenant's allow-list read is still in flight.
    await user.selectOptions(screen.getByLabelText('Tenant'), 'other')
    expect(await screen.findByText('other-model')).toBeInTheDocument()

    // Now the stale response lands.
    resolveDefault(
      allowlist({ configured: true, allowed_models: ['default-model'] })
    )
    await new Promise((r) => setTimeout(r, 20))

    expect(screen.getByText('other-model')).toBeInTheDocument()
    expect(screen.queryByText('default-model')).toBeNull()
  })

  it('saves against the selected tenant, never the previous one', async () => {
    get.mockImplementation((url: string) => {
      if (url.includes('/model-allowlist')) {
        return Promise.resolve(
          allowlist({ configured: true, allowed_models: ['model-a'] })
        )
      }
      if (url.includes('/model-limits')) return Promise.resolve([])
      return Promise.resolve({
        tenants: [
          { id: 'default', name: 'default' },
          { id: 'other', name: 'other' },
        ],
        total: 2,
      })
    })
    put.mockResolvedValue({})
    const user = userEvent.setup()
    renderPage()

    await waitFor(() =>
      expect(screen.getByRole('option', { name: 'other' })).toBeInTheDocument()
    )
    await user.selectOptions(screen.getByLabelText('Tenant'), 'other')
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith(
        '/api/v2/admin/tenants/other/model-allowlist'
      )
    )
    await screen.findByText('model-a')
    await user.click(screen.getByRole('button', { name: 'Save allow-list' }))

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(
        '/api/v2/admin/tenants/other/model-allowlist',
        { allowed_models: ['model-a'] }
      )
    )
  })
})

describe('tenant list', () => {
  it('always offers the default tenant, first, even without a row', async () => {
    get.mockResolvedValue({
      tenants: [{ id: 't-1', name: 'Acme' }],
      total: 1,
    })
    const out = await fetchTenantOptions()
    expect(out.map((t) => t.id)).toEqual(['default', 't-1'])
    expect(out[1]?.name).toBe('Acme')
  })

  it('pages through every tenant', async () => {
    const page1 = Array.from({ length: 100 }, (_, i) => ({
      id: `t-${i}`,
      name: `T${i}`,
    }))
    get
      .mockResolvedValueOnce({ tenants: page1, total: 101 })
      .mockResolvedValueOnce({
        tenants: [{ id: 't-100', name: 'T100' }],
        total: 101,
      })
    const out = await fetchTenantOptions()
    expect(get).toHaveBeenCalledTimes(2)
    expect(out).toHaveLength(102)
    expect(out.at(-1)?.id).toBe('t-100')
  })
})
