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

import { AdminTenantsPage } from './index'
import {
  listPayload,
  renderWithClient,
  serveGet,
  tenantFixture,
  type RouteConfig,
  type Routes,
} from './test-helpers'

const { apiGet, apiPost, apiPut, apiDelete, tenantGet, toast } = vi.hoisted(
  () => ({
    apiGet: vi.fn(),
    apiPost: vi.fn(),
    apiPut: vi.fn(),
    apiDelete: vi.fn(),
    tenantGet: vi.fn(),
    toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
  })
)

vi.mock('sonner', () => ({ toast }))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: { get: apiGet, post: apiPost, put: apiPut, delete: apiDelete },
    tenantApi: {
      get: tenantGet,
      post: vi.fn(),
      put: vi.fn(),
      delete: vi.fn(),
    },
  }
})

const LIST = '/api/v2/admin/tenants'

function renderPage(routes: Routes) {
  const live = serveGet(apiGet, tenantGet, routes)
  return { live, ...renderWithClient(<AdminTenantsPage />) }
}

async function openRowMenu(name: string) {
  const row = (await screen.findByText(name)).closest('tr') as HTMLElement
  await userEvent.click(within(row).getByRole('button', { name: 'Open menu' }))
}

async function pickMenuItem(name: string | RegExp) {
  await userEvent.click(await screen.findByRole('menuitem', { name }))
}

beforeEach(() => {
  vi.clearAllMocks()
  apiPost.mockResolvedValue({})
  apiPut.mockResolvedValue({})
})

describe('AdminTenantsPage list', () => {
  it('renders the tenant, its slug, plan, status, seats and cap from the wire names', async () => {
    renderPage({
      [LIST]: listPayload([
        tenantFixture({
          name: 'Global Inc',
          slug: 'global',
          plan_type: 'enterprise',
          max_users: 25,
          max_quota: 5_000_000,
          rate_limit_rpm: 60,
        }),
      ]),
    })

    expect(await screen.findByText('Global Inc')).toBeInTheDocument()
    expect(screen.getByText('global')).toBeInTheDocument()
    expect(screen.getByText('Enterprise')).toBeInTheDocument()
    expect(screen.getByTestId('tenant-status-tenant-uuid-1')).toHaveTextContent(
      'Active'
    )
    expect(screen.getByText('25')).toBeInTheDocument()
    // Money comes from the status config: 5,000,000 units at 500,000 per USD.
    expect(await screen.findByText('$10.00')).toBeInTheDocument()
    expect(screen.getByTestId('tenants-count')).toHaveTextContent('1 tenants')
    expect(apiGet).toHaveBeenCalledWith(LIST, {
      params: { page: 1, page_size: 100 },
    })
  })

  it('reads the tenants key, and the older items key as a fallback', async () => {
    renderPage({
      [LIST]: {
        items: [tenantFixture({ name: 'Legacy Shape Co' })],
        total: 1,
      },
    })
    expect(await screen.findByText('Legacy Shape Co')).toBeInTheDocument()
  })

  it('reads every page, so the search covers the whole fleet', async () => {
    const rows = (from: number, n: number) =>
      Array.from({ length: n }, (_, i) =>
        tenantFixture({
          id: `id-${from + i}`,
          name: `Tenant ${from + i}`,
          slug: `t-${from + i}`,
        })
      )
    renderPage({
      [LIST]: (config?: RouteConfig) => {
        const page = config?.params?.page
        return {
          tenants: page === 1 ? rows(0, 100) : rows(100, 20),
          total: 120,
          page,
          page_size: 100,
        }
      },
    })
    await screen.findByText('Tenant 0')
    await waitFor(() =>
      expect(screen.getByTestId('tenants-count')).toHaveTextContent(
        '120 tenants'
      )
    )
    expect(apiGet).toHaveBeenCalledWith(LIST, {
      params: { page: 2, page_size: 100 },
    })
    await userEvent.type(
      screen.getByPlaceholderText('Search by name, slug or ID...'),
      'Tenant 115'
    )
    expect(await screen.findByText('Tenant 115')).toBeInTheDocument()
    expect(screen.queryByText('Tenant 0')).not.toBeInTheDocument()
  })

  it('search filters in the browser by name, slug and id; the server is not asked', async () => {
    renderPage({
      [LIST]: listPayload([
        tenantFixture({ id: 'a', name: 'Acme Corp', slug: 'acme' }),
        tenantFixture({ id: 'b', name: 'Globex', slug: 'globex' }),
      ]),
    })
    await screen.findByText('Acme Corp')
    const box = screen.getByPlaceholderText('Search by name, slug or ID...')

    await userEvent.type(box, 'glob')
    await waitFor(() =>
      expect(screen.queryByText('Acme Corp')).not.toBeInTheDocument()
    )
    expect(screen.getByText('Globex')).toBeInTheDocument()

    await userEvent.clear(box)
    await userEvent.type(box, 'nothing-like-this')
    expect(
      await screen.findByText('No tenants match your search.')
    ).toBeInTheDocument()
    // The list endpoint ignores keyword; none is ever sent.
    for (const call of apiGet.mock.calls) {
      expect(JSON.stringify(call)).not.toContain('keyword')
    }
  })

  it('an empty fleet offers to create the first tenant', async () => {
    renderPage({ [LIST]: listPayload([]) })
    expect(
      await screen.findByText('No tenants yet. Create one to get started.')
    ).toBeInTheDocument()
  })

  it('a disabled tenant offers enable, an active one disable, a suspended one no suspend', async () => {
    renderPage({
      [LIST]: listPayload([
        tenantFixture({ id: 'on', name: 'On Co', status: 1 }),
        tenantFixture({ id: 'off', name: 'Off Co', status: 2 }),
        tenantFixture({ id: 'held', name: 'Held Co', status: 3 }),
      ]),
    })
    await openRowMenu('On Co')
    expect(
      await screen.findByRole('menuitem', { name: /Disable tenant/ })
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('menuitem', { name: /Enable tenant/ })
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('menuitem', { name: /Suspend tenant/ })
    ).toBeInTheDocument()
    await userEvent.keyboard('{Escape}')

    await openRowMenu('Off Co')
    expect(
      await screen.findByRole('menuitem', { name: /Enable tenant/ })
    ).toBeInTheDocument()
    await userEvent.keyboard('{Escape}')

    await openRowMenu('Held Co')
    expect(
      await screen.findByRole('menuitem', { name: /Enable tenant/ })
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('menuitem', { name: /Suspend tenant/ })
    ).not.toBeInTheDocument()
  })
})

describe('AdminTenantsPage failed reads', () => {
  it('a 403 says administrator access is required, with no count and no empty-state prompt', async () => {
    renderPage({ [LIST]: new ApiError('forbidden', { status: 403 }) })
    expect(
      await screen.findByText('Administrator access required')
    ).toBeInTheDocument()
    expect(screen.queryByTestId('tenants-count')).not.toBeInTheDocument()
    expect(
      screen.queryByText('No tenants yet. Create one to get started.')
    ).not.toBeInTheDocument()
  })

  it('a 500 is an error with retry, never "0 tenants" or the empty state', async () => {
    renderPage({ [LIST]: new ApiError('boom', { status: 500 }) })
    expect(await screen.findByText('Failed to load tenants')).toBeInTheDocument()
    expect(screen.queryByTestId('tenants-count')).not.toBeInTheDocument()
    expect(screen.queryByText(/\d+ tenants/)).not.toBeInTheDocument()
    expect(
      screen.queryByText('No tenants yet. Create one to get started.')
    ).not.toBeInTheDocument()
    // A transport failure is not a permission refusal.
    expect(
      screen.queryByText('Administrator access required')
    ).not.toBeInTheDocument()
  })

  it('a 200 carrying success:false (surfaced as an ApiError) is a failed read too', async () => {
    renderPage({
      [LIST]: new ApiError('tenant store unavailable'),
    })
    expect(await screen.findByText('Failed to load tenants')).toBeInTheDocument()
    expect(screen.getByText('tenant store unavailable')).toBeInTheDocument()
    expect(screen.queryByTestId('tenants-count')).not.toBeInTheDocument()
  })

  it('clears the error and shows the rows once a retry succeeds', async () => {
    const { live } = renderPage({
      [LIST]: new ApiError('boom', { status: 500 }),
    })
    await screen.findByText('Failed to load tenants')

    live[LIST] = listPayload([tenantFixture()])
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))

    expect(await screen.findByText('Acme Corp')).toBeInTheDocument()
    expect(screen.queryByText('Failed to load tenants')).not.toBeInTheDocument()
  })

  it('a failed refresh keeps the last list on screen and says so', async () => {
    const { live, client } = renderPage({
      [LIST]: listPayload([tenantFixture()]),
    })
    await screen.findByText('Acme Corp')

    live[LIST] = new ApiError('boom', { status: 500 })
    await client.refetchQueries({ queryKey: ['admin-tenants', 'list'] })

    expect(
      await screen.findByText(
        'Could not refresh tenants. Showing the last list that loaded.'
      )
    ).toBeInTheDocument()
    expect(screen.getByText('Acme Corp')).toBeInTheDocument()
  })
})

describe('AdminTenantsPage create', () => {
  async function openCreate() {
    await screen.findByText('Acme Corp')
    await userEvent.click(screen.getAllByRole('button', { name: /New tenant/ })[0])
    return screen.findByRole('dialog')
  }

  it('POSTs the server wire names, with the slug following the name', async () => {
    renderPage({ [LIST]: listPayload([tenantFixture()]) })
    await openCreate()

    await userEvent.type(screen.getByLabelText('Name'), 'New Co')
    expect(screen.getByLabelText('Slug')).toHaveValue('new-co')
    await userEvent.type(
      screen.getByLabelText('Identity provider organization ID'),
      'org-new'
    )
    await userEvent.click(screen.getByRole('button', { name: 'Create tenant' }))

    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1))
    const [url, body] = apiPost.mock.calls[0]
    expect(url).toBe(LIST)
    expect(body).toEqual({
      zitadel_org_id: 'org-new',
      slug: 'new-co',
      name: 'New Co',
      plan_type: 'free',
    })
    for (const stale of ['quotaLimit', 'planType', 'plan', 'quota_limit']) {
      expect(body).not.toHaveProperty(stale)
    }
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Tenant created'))
    // The list is read again.
    await waitFor(() => expect(apiGet.mock.calls.filter(([u]) => u === LIST).length).toBeGreaterThan(1))
  })

  it('a hand-typed slug is sanitised and stops following the name', async () => {
    renderPage({ [LIST]: listPayload([tenantFixture()]) })
    await openCreate()
    await userEvent.type(screen.getByLabelText('Slug'), 'My Slug_1')
    expect(screen.getByLabelText('Slug')).toHaveValue('my-slug-1')
    await userEvent.type(screen.getByLabelText('Name'), 'Other')
    expect(screen.getByLabelText('Slug')).toHaveValue('my-slug-1')
  })

  it('converts the money cap to quota units and carries seats and limits', async () => {
    renderPage({ [LIST]: listPayload([tenantFixture()]) })
    await openCreate()
    await userEvent.type(screen.getByLabelText('Name'), 'Capped')
    await userEvent.type(
      screen.getByLabelText('Identity provider organization ID'),
      'org-cap'
    )
    await userEvent.selectOptions(screen.getByLabelText('Plan'), 'pro')
    await userEvent.type(screen.getByLabelText('Seats'), '25')
    await userEvent.type(
      screen.getByLabelText(/Monthly quota cap/),
      '5'
    )
    await userEvent.type(screen.getByLabelText('RPM limit'), '60')
    await userEvent.type(screen.getByLabelText('TPM limit'), '9000')
    await userEvent.click(screen.getByRole('button', { name: 'Create tenant' }))

    await waitFor(() => expect(apiPost).toHaveBeenCalled())
    expect(apiPost.mock.calls[0][1]).toEqual({
      zitadel_org_id: 'org-cap',
      slug: 'capped',
      name: 'Capped',
      plan_type: 'pro',
      max_users: 25,
      max_quota: 2_500_000,
      rate_limit_rpm: 60,
      rate_limit_tpm: 9000,
    })
  })

  it('refuses an empty form without calling the server', async () => {
    renderPage({ [LIST]: listPayload([tenantFixture()]) })
    await openCreate()
    await userEvent.click(screen.getByRole('button', { name: 'Create tenant' }))

    expect((await screen.findAllByText('This field is required')).length).toBe(3)
    expect(apiPost).not.toHaveBeenCalled()
  })

  it('shows the server’s reason and keeps the form when creation is refused', async () => {
    apiPost.mockRejectedValue(
      new ApiError('Slug "admin" is reserved by a platform route', {
        status: 400,
      })
    )
    renderPage({ [LIST]: listPayload([tenantFixture()]) })
    await openCreate()
    await userEvent.type(screen.getByLabelText('Name'), 'Admin')
    await userEvent.type(
      screen.getByLabelText('Identity provider organization ID'),
      'org-x'
    )
    await userEvent.click(screen.getByRole('button', { name: 'Create tenant' }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        'Slug "admin" is reserved by a platform route'
      )
    )
    expect(screen.getByLabelText('Name')).toHaveValue('Admin')
  })
})

describe('AdminTenantsPage status changes', () => {
  async function arm(
    status: number,
    menuItem: RegExp,
    confirmName: 'Enable' | 'Disable' | 'Suspend'
  ) {
    renderPage({
      [LIST]: listPayload([
        tenantFixture({ id: 'uuid-1', name: 'Acme Corp', status }),
      ]),
    })
    await openRowMenu('Acme Corp')
    await pickMenuItem(menuItem)
    const dialog = await screen.findByRole('alertdialog')
    return {
      dialog,
      confirm: within(dialog).getByRole('button', { name: confirmName }),
    }
  }

  it('disable POSTs to /:id/disable with an empty body, but only after the name is typed', async () => {
    const { dialog, confirm } = await arm(1, /Disable tenant/, 'Disable')
    expect(
      within(dialog).getByText('Disable tenant "Acme Corp"?')
    ).toBeInTheDocument()
    expect(confirm).toBeDisabled()

    await userEvent.type(within(dialog).getByRole('textbox'), 'acme')
    expect(confirm).toBeDisabled()
    await userEvent.clear(within(dialog).getByRole('textbox'))
    await userEvent.type(within(dialog).getByRole('textbox'), 'Acme Corp')
    expect(confirm).toBeEnabled()
    await userEvent.click(confirm)

    await waitFor(() =>
      expect(apiPost).toHaveBeenCalledWith(`${LIST}/uuid-1/disable`, {})
    )
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Tenant disabled')
    )
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    )
    // The table is read again after the change.
    expect(apiGet.mock.calls.filter(([u]) => u === LIST).length).toBeGreaterThan(1)
  })

  it('enable POSTs to /:id/enable for a disabled tenant', async () => {
    const { dialog, confirm } = await arm(2, /Enable tenant/, 'Enable')
    await userEvent.type(within(dialog).getByRole('textbox'), 'Acme Corp')
    await userEvent.click(confirm)
    await waitFor(() =>
      expect(apiPost).toHaveBeenCalledWith(`${LIST}/uuid-1/enable`, {})
    )
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Tenant enabled')
    )
  })

  it('suspend POSTs to /:id/suspend', async () => {
    const { dialog, confirm } = await arm(1, /Suspend tenant/, 'Suspend')
    await userEvent.type(within(dialog).getByRole('textbox'), 'Acme Corp')
    await userEvent.click(confirm)
    await waitFor(() =>
      expect(apiPost).toHaveBeenCalledWith(`${LIST}/uuid-1/suspend`, {})
    )
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Tenant suspended')
    )
  })

  it('cancelling sends nothing', async () => {
    const { dialog } = await arm(1, /Disable tenant/, 'Disable')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    )
    expect(apiPost).not.toHaveBeenCalled()
  })

  it('a refused change shows the reason and leaves the dialog open', async () => {
    apiPost.mockRejectedValue(
      new ApiError('Cannot disable default tenant', { status: 403 })
    )
    const { dialog, confirm } = await arm(1, /Disable tenant/, 'Disable')
    await userEvent.type(within(dialog).getByRole('textbox'), 'Acme Corp')
    await userEvent.click(confirm)

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Cannot disable default tenant')
    )
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
    expect(toast.success).not.toHaveBeenCalled()
  })
})

describe('AdminTenantsPage edit', () => {
  async function openEdit(over = {}) {
    renderPage({
      [LIST]: listPayload([
        tenantFixture({ id: 'uuid-1', max_quota: 1_000_000, ...over }),
      ]),
    })
    await screen.findByText('Acme Corp')
    await userEvent.click(screen.getByRole('button', { name: 'Edit tenant' }))
    return screen.findByRole('dialog')
  }

  it('is prefilled, keeps slug and org read-only, and saves nothing until something changes', async () => {
    await openEdit()
    expect(screen.getByLabelText('Name')).toHaveValue('Acme Corp')
    expect(screen.getByLabelText('Slug')).toBeDisabled()
    expect(screen.getByLabelText('Identity provider organization ID')).toBeDisabled()
    expect(screen.getByLabelText('Seats')).toHaveValue('100')
    await waitFor(() =>
      expect(screen.getByLabelText(/Monthly quota cap/)).toHaveValue('2')
    )
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
  })

  it('PUTs only the fields that changed, in wire names', async () => {
    await openEdit()
    const name = screen.getByLabelText('Name')
    await userEvent.clear(name)
    await userEvent.type(name, 'Acme Inc')
    await userEvent.selectOptions(screen.getByLabelText('Plan'), 'enterprise')
    await userEvent.click(
      screen.getByRole('switch', {
        name: 'Platform wallet is the only balance gate',
      })
    )
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))

    await waitFor(() => expect(apiPut).toHaveBeenCalledTimes(1))
    expect(apiPut).toHaveBeenCalledWith(`${LIST}/uuid-1`, {
      name: 'Acme Inc',
      plan_type: 'enterprise',
      wallet_authoritative: true,
    })
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Tenant updated')
    )
  })

  it('converts an edited cap to quota units', async () => {
    await openEdit()
    const cap = screen.getByLabelText(/Monthly quota cap/)
    await waitFor(() => expect(cap).toHaveValue('2'))
    await userEvent.clear(cap)
    await userEvent.type(cap, '10')
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(apiPut).toHaveBeenCalled())
    expect(apiPut.mock.calls[0][1]).toEqual({ max_quota: 5_000_000 })
  })

  it('refuses a cleared cap instead of silently keeping the old one', async () => {
    await openEdit()
    const cap = screen.getByLabelText(/Monthly quota cap/)
    await waitFor(() => expect(cap).toHaveValue('2'))
    await userEvent.clear(cap)
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    expect(
      await screen.findByText('This value can be changed here but not cleared.')
    ).toBeInTheDocument()
    expect(apiPut).not.toHaveBeenCalled()
  })

  it('shows a refusal from the server', async () => {
    apiPut.mockRejectedValue(new ApiError('No fields to update', { status: 400 }))
    await openEdit()
    const name = screen.getByLabelText('Name')
    await userEvent.clear(name)
    await userEvent.type(name, 'Acme Inc')
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('No fields to update')
    )
  })
})

describe('AdminTenantsPage row drawers', () => {
  it.each([
    [
      'Stats',
      'Acme Corp · stats',
      '/api/v2/admin/tenants/tenant-uuid-1/stats',
      { tenant_id: 'tenant-uuid-1', user_count: 3, max_users: 100 },
    ],
    [
      'Invites',
      'Acme Corp · invites',
      '/api/v2/admin/tenants/tenant-uuid-1/invites',
      { invites: [], total: 0 },
    ],
    [
      'Credit pool',
      'Acme Corp · credit pool',
      '/api/v2/admin/tenants/tenant-uuid-1/credit-pool',
      {
        id: 1,
        tenant_id: 'tenant-uuid-1',
        current_balance: 1,
        max_balance: -1,
        reset_period: 'monthly',
      },
    ],
  ])('the row menu opens %s for the clicked tenant', async (item, title, url, payload) => {
    renderPage({
      [LIST]: listPayload([tenantFixture()]),
      [url]: payload,
      '/api/v2/admin/tenants/tenant-uuid-1/credit-pool/usage': { draws: [] },
    })
    await openRowMenu('Acme Corp')
    await pickMenuItem(item)
    expect(await screen.findByRole('dialog', { name: title })).toBeInTheDocument()
    await waitFor(() =>
      expect(apiGet.mock.calls.some(([u]) => u === url)).toBe(true)
    )
  })

  it('the row menu opens the rate-limits dialog prefilled from the row', async () => {
    renderPage({
      [LIST]: listPayload([
        tenantFixture({ rate_limit_rpm: 60, rate_limit_tpm: 0 }),
      ]),
    })
    await openRowMenu('Acme Corp')
    await pickMenuItem('Rate limits')
    const dialog = await screen.findByRole('dialog', {
      name: 'Acme Corp · rate limits',
    })
    const [rpm, tpm] = within(dialog).getAllByRole('spinbutton')
    expect(rpm).toHaveValue(60)
    expect(tpm).toHaveValue(null)
  })
})
