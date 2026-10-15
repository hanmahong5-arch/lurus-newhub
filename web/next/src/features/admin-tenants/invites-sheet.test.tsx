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
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { TenantInvitesSheet } from './components/tenant-invites-sheet'
import {
  renderWithClient,
  serveGet,
  tenantFixture,
  type Routes,
} from './test-helpers'
import type { TenantInvite } from './types'

const {
  apiGet,
  apiPost,
  apiDelete,
  tenantGet,
  toast,
  setDialog,
  refresh,
  copyToClipboard,
} = vi.hoisted(() => ({
  apiGet: vi.fn(),
  apiPost: vi.fn(),
  apiDelete: vi.fn(),
  tenantGet: vi.fn(),
  toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
  setDialog: vi.fn(),
  refresh: vi.fn(),
  copyToClipboard: vi.fn(),
}))

vi.mock('sonner', () => ({ toast }))

vi.mock('@/lib/copy-to-clipboard', () => ({ copyToClipboard }))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: {
      get: apiGet,
      post: apiPost,
      put: vi.fn(),
      delete: apiDelete,
    },
    tenantApi: {
      get: tenantGet,
      post: vi.fn(),
      put: vi.fn(),
      delete: vi.fn(),
    },
  }
})

vi.mock('./components/tenants-provider', () => ({
  useTenants: () => ({ dialog: null, setDialog, refresh }),
}))

const TENANT = tenantFixture({ id: 't-acme', name: 'Acme' })
const INVITES_URL = '/api/v2/admin/tenants/t-acme/invites'

const PENDING: TenantInvite = {
  id: 1,
  code_prefix: 'abcd1234',
  status: 1,
  expired_time: 0,
  consumed_by_account_id: null,
  created_at: '2026-09-19T00:00:00Z',
}

const CONSUMED: TenantInvite = {
  id: 2,
  code_prefix: 'ffff9999',
  status: 2,
  expired_time: 1_999_999_999,
  consumed_by_account_id: 4242,
  created_at: '2026-09-18T00:00:00Z',
}

const list = (invites: TenantInvite[], total = invites.length) => ({
  invites,
  total,
  page: 1,
  page_size: 100,
})

function renderSheet(routes: Routes) {
  const live = serveGet(apiGet, tenantGet, routes)
  return { live, ...renderWithClient(<TenantInvitesSheet tenant={TENANT} />) }
}

async function ready() {
  const submit = await screen.findByTestId('invite-issue-submit')
  await waitFor(() => expect(submit).toBeEnabled())
}

/** Issue one invite so the plaintext link is on screen. */
async function issue(code = 'onetimecode1234567890abcdef') {
  apiPost.mockResolvedValueOnce({ id: 1, code, status: 1 })
  await userEvent.click(screen.getByTestId('invite-issue-submit'))
  await screen.findByTestId('invite-issued-link')
}

beforeEach(() => {
  vi.clearAllMocks()
  apiPost.mockResolvedValue({})
  apiDelete.mockResolvedValue({})
  copyToClipboard.mockResolvedValue(true)
})

describe('TenantInvitesSheet list', () => {
  it('renders the empty state when nothing was issued', async () => {
    renderSheet({ [INVITES_URL]: list([]) })
    expect(await screen.findByText('No invites issued yet.')).toBeInTheDocument()
    expect(apiGet).toHaveBeenCalledWith(INVITES_URL, {
      params: { page: 1, page_size: 100 },
    })
  })

  it('renders the code prefix only, never a full code, and revoke only on pending rows', async () => {
    renderSheet({ [INVITES_URL]: list([PENDING, CONSUMED]) })

    const pending = await screen.findByTestId('invite-row-1')
    expect(pending).toHaveTextContent('abcd1234…')
    expect(pending).toHaveTextContent('Pending')
    const consumed = screen.getByTestId('invite-row-2')
    expect(consumed).toHaveTextContent('ffff9999…')
    expect(consumed).toHaveTextContent('Consumed')
    expect(consumed).toHaveTextContent('4242')
    expect(screen.getByTestId('invite-revoke-btn-1')).toBeInTheDocument()
    expect(screen.queryByTestId('invite-revoke-btn-2')).not.toBeInTheDocument()
  })

  it('a pending code past its expiry reads as expired', async () => {
    renderSheet({
      [INVITES_URL]: list([{ ...PENDING, expired: true, expired_time: 1_000 }]),
    })
    expect(await screen.findByTestId('invite-row-1')).toHaveTextContent(
      'Expired'
    )
  })

  it('says when only part of a long list is shown', async () => {
    renderSheet({ [INVITES_URL]: list([PENDING], 250) })
    expect(
      await screen.findByText('Showing the latest 1 of 250 invites.')
    ).toBeInTheDocument()
  })

  it('a load failure is a distinct message, not the empty state, and retry works', async () => {
    const { live } = renderSheet({
      [INVITES_URL]: new ApiError('boom', { status: 500 }),
    })
    expect(
      await screen.findByTestId('invite-list-load-failed')
    ).toBeInTheDocument()
    expect(screen.queryByText('No invites issued yet.')).not.toBeInTheDocument()

    live[INVITES_URL] = list([PENDING])
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByTestId('invite-row-1')).toBeInTheDocument()
  })

  it('a success:false answer (an ApiError without a status) is a load failure too', async () => {
    renderSheet({ [INVITES_URL]: new ApiError('nope') })
    expect(
      await screen.findByTestId('invite-list-load-failed')
    ).toBeInTheDocument()
    expect(screen.queryByText('No invites issued yet.')).not.toBeInTheDocument()
  })
})

describe('TenantInvitesSheet issue', () => {
  it('shows the one-time link built from the origin and the code, and POSTs the validity', async () => {
    renderSheet({ [INVITES_URL]: list([]) })
    await ready()
    const ttl = screen.getByTestId('invite-ttl-hours')
    expect(ttl).toHaveValue('72')
    await userEvent.clear(ttl)
    await userEvent.type(ttl, '24')
    await issue('brandnewcode1234567890abcd')

    expect(apiPost).toHaveBeenCalledWith(INVITES_URL, { ttl_hours: 24 })
    expect(screen.getByTestId('invite-issued-link')).toHaveValue(
      `${window.location.origin}/login?invite=brandnewcode1234567890abcd`
    )
    expect(screen.getByText(/shown once/i)).toBeInTheDocument()
    expect(toast.success).toHaveBeenCalledWith('Invite issued')
    // The list is read again so the new row appears.
    await waitFor(() =>
      expect(apiGet.mock.calls.filter(([u]) => u === INVITES_URL).length).toBe(2)
    )
  })

  it('the full code is shown only in the link, nowhere else', async () => {
    renderSheet({ [INVITES_URL]: list([]) })
    await ready()
    await issue('secretcode1234567890abcdefgh')
    const box = screen.getByTestId('invite-issued-box')
    // Only the link input carries it; no stray text node repeats the code.
    expect(within(box).queryByText('secretcode1234567890abcdefgh')).toBeNull()
  })

  it('a blank validity sends an empty body so the server applies its default', async () => {
    renderSheet({ [INVITES_URL]: list([]) })
    await ready()
    await userEvent.clear(screen.getByTestId('invite-ttl-hours'))
    await issue()
    expect(apiPost).toHaveBeenCalledWith(INVITES_URL, {})
  })

  it('a validity of 0 also sends an empty body', async () => {
    renderSheet({ [INVITES_URL]: list([]) })
    await ready()
    const ttl = screen.getByTestId('invite-ttl-hours')
    await userEvent.clear(ttl)
    await userEvent.type(ttl, '0')
    await issue()
    expect(apiPost).toHaveBeenCalledWith(INVITES_URL, {})
  })

  it.each([
    ['721', 'The validity can be at most 720 hours (30 days).'],
    ['abc', 'Enter a whole number of hours.'],
    ['1.5', 'Enter a whole number of hours.'],
  ])('refuses a validity of %s without calling the server', async (value, message) => {
    renderSheet({ [INVITES_URL]: list([]) })
    await ready()
    const ttl = screen.getByTestId('invite-ttl-hours')
    await userEvent.clear(ttl)
    await userEvent.type(ttl, value)
    await userEvent.click(screen.getByTestId('invite-issue-submit'))
    expect(await screen.findByText(message)).toBeInTheDocument()
    expect(apiPost).not.toHaveBeenCalled()
  })

  it('a failed issue shows an error and renders no link box', async () => {
    apiPost.mockRejectedValue(new ApiError('boom', { status: 500 }))
    renderSheet({ [INVITES_URL]: list([]) })
    await ready()
    await userEvent.click(screen.getByTestId('invite-issue-submit'))
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith('boom'))
    expect(screen.queryByTestId('invite-issued-link')).not.toBeInTheDocument()
  })

  it('the copy button copies the exact issued link', async () => {
    renderSheet({ [INVITES_URL]: list([]) })
    await ready()
    await issue('copycode1234567890abcd12345678')
    await userEvent.click(
      screen.getByRole('button', { name: 'Copy invite link' })
    )
    await waitFor(() =>
      expect(copyToClipboard).toHaveBeenCalledWith(
        `${window.location.origin}/login?invite=copycode1234567890abcd12345678`
      )
    )
  })
})

describe('TenantInvitesSheet revoke', () => {
  it('asks first, then DELETEs and reads the list again', async () => {
    const { live } = renderSheet({ [INVITES_URL]: list([PENDING]) })
    await userEvent.click(await screen.findByTestId('invite-revoke-btn-1'))

    const dialog = await screen.findByRole('alertdialog')
    expect(apiDelete).not.toHaveBeenCalled()
    live[INVITES_URL] = list([])
    await userEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }))

    await waitFor(() =>
      expect(apiDelete).toHaveBeenCalledWith(`${INVITES_URL}/1`)
    )
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Invite revoked')
    )
    await waitFor(() =>
      expect(apiGet.mock.calls.filter(([u]) => u === INVITES_URL).length).toBe(2)
    )
    expect(await screen.findByText('No invites issued yet.')).toBeInTheDocument()
  })

  it('cancelling the confirmation revokes nothing', async () => {
    renderSheet({ [INVITES_URL]: list([PENDING]) })
    await userEvent.click(await screen.findByTestId('invite-revoke-btn-1'))
    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    )
    expect(apiDelete).not.toHaveBeenCalled()
  })

  it('a refused revoke shows the reason and keeps the row', async () => {
    apiDelete.mockRejectedValue(new ApiError('Invite not found', { status: 404 }))
    renderSheet({ [INVITES_URL]: list([PENDING]) })
    await userEvent.click(await screen.findByTestId('invite-revoke-btn-1'))
    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }))
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Invite not found')
    )
    expect(screen.getByTestId('invite-row-1')).toBeInTheDocument()
  })
})

describe('TenantInvitesSheet as a dialog', () => {
  it('is a modal dialog named after the tenant', async () => {
    renderSheet({ [INVITES_URL]: list([]) })
    await ready()
    expect(screen.getByRole('dialog')).toHaveAccessibleName('Acme · invites')
  })

  it('Escape closes while no link is pending', async () => {
    renderSheet({ [INVITES_URL]: list([]) })
    await ready()
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(setDialog).toHaveBeenCalledWith(null))
    expect(setDialog).toHaveBeenCalledTimes(1)
  })

  it('while the one-time link is shown, Escape and the X do not close and focus goes to Done', async () => {
    renderSheet({ [INVITES_URL]: list([]) })
    await ready()
    await issue()

    await userEvent.keyboard('{Escape}')
    expect(setDialog).not.toHaveBeenCalled()

    await userEvent.click(screen.getByRole('button', { name: 'Close' }))
    expect(setDialog).not.toHaveBeenCalled()
    expect(screen.getByTestId('invite-issued-dismiss')).toHaveFocus()
    expect(screen.getByTestId('invite-issued-link')).toBeInTheDocument()
  })

  it('a click outside the sheet does not throw the link away either', async () => {
    renderSheet({ [INVITES_URL]: list([]) })
    await ready()
    await issue()
    await userEvent.click(document.body)
    expect(setDialog).not.toHaveBeenCalled()
    expect(screen.getByTestId('invite-issued-link')).toBeInTheDocument()
  })

  it('the explicit Done next to the link is what closes the drawer', async () => {
    renderSheet({ [INVITES_URL]: list([]) })
    await ready()
    await issue()
    await userEvent.click(screen.getByTestId('invite-issued-dismiss'))
    expect(setDialog).toHaveBeenCalledWith(null)
  })

  it('an issue in flight refuses Escape', async () => {
    let settle: (value: unknown) => void = () => undefined
    apiPost.mockImplementationOnce(
      () => new Promise((resolve) => (settle = resolve))
    )
    renderSheet({ [INVITES_URL]: list([]) })
    await ready()
    await userEvent.click(screen.getByTestId('invite-issue-submit'))
    await waitFor(() => expect(apiPost).toHaveBeenCalled())
    await userEvent.keyboard('{Escape}')
    expect(setDialog).not.toHaveBeenCalled()
    settle({ id: 1, code: 'abc', status: 1 })
    await screen.findByTestId('invite-issued-link')
  })
})
