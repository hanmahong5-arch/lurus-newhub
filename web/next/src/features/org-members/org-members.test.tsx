import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { OrgMembersPage } from './index'
import { buildInviteBody } from './lib/invite-body'
import { redeemErrorMessage, roleErrorMessage } from './lib/errors'
import { mapInviteList, mapIssuedInvite, mapMemberList } from './lib/map'

const { get, post, put, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return { ...actual, tenantApi: { get, post, put, delete: del } }
})

const tr = (k: string) => k

const ADMIN = {
  id: 1,
  username: 'alice',
  display_name: 'Alice',
  email: 'alice@example.com',
  role: 1,
  tenant_role: 'admin',
  is_payer: true,
}

function route(handlers: Record<string, unknown>) {
  get.mockImplementation((path: string) => {
    const v = handlers[path]
    if (v instanceof Error) return Promise.reject(v)
    return Promise.resolve(v ?? { items: [] })
  })
}

const PROJECTS = { items: [{ id: 7, name: 'Research', deleted: false }] }
const MEMBERS = {
  items: [
    {
      user_id: 1,
      username: 'alice',
      display_name: 'Alice',
      email: 'alice@example.com',
      tenant_role: 'admin',
      is_payer: true,
      joined_at: null,
      departments: [],
    },
    {
      user_id: 2,
      username: 'bob',
      display_name: 'Bob',
      email: 'bob@example.com',
      tenant_role: 'dept_lead',
      is_payer: false,
      joined_at: null,
      departments: [
        { project_id: 7, name: 'Research', is_lead: true },
        { project_id: 8, name: 'Ops', is_lead: true },
      ],
    },
    {
      user_id: 3,
      username: 'carol',
      display_name: '',
      email: 'carol@example.com',
      tenant_role: '',
      is_payer: false,
      joined_at: 1700000000,
      departments: [],
    },
  ],
  total: 3,
  page: 1,
  page_size: 20,
}
const INVITES = {
  invites: [
    {
      id: 11,
      code_prefix: 'abcd1234',
      status: 1,
      member_role: 'dept_lead',
      project_id: 7,
      expires_at: 0,
      created_at: '2026-01-01T00:00:00Z',
      used: false,
      revoked: false,
      expired: false,
    },
    {
      id: 12,
      code_prefix: 'zzzz9999',
      status: 3,
      member_role: '',
      project_id: 0,
      expires_at: 0,
      created_at: '2026-01-01T00:00:00Z',
      used: false,
      revoked: true,
      expired: false,
    },
  ],
  total: 2,
  page: 1,
  page_size: 20,
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <OrgMembersPage />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('mapping', () => {
  it('maps the tenant roster with real email, payer and departments', () => {
    const { items, total } = mapMemberList(MEMBERS)
    expect(total).toBe(3)
    expect(items.map((m) => m.userId)).toEqual([1, 2, 3])
    expect(items[1].departments).toEqual(['Research', 'Ops'])
    expect(items[0].isPayer).toBe(true)
    expect(items[1].isPayer).toBe(false)
    expect(items[2].email).toBe('carol@example.com')
    // falls back to username when there is no display name
    expect(items[2].name).toBe('carol')
    // joined_at null => unknown (0), a number is kept
    expect(items[0].joinedAt).toBe(0)
    expect(items[2].joinedAt).toBe(1700000000)
  })

  it('derives invite state from the server flags', () => {
    const list = mapInviteList(INVITES)
    expect(list.items.map((i) => i.state)).toEqual(['active', 'revoked'])
    expect(
      mapInviteList({ invites: [{ id: 1, used: true }] }).items[0].state
    ).toBe('used')
    expect(
      mapInviteList({ invites: [{ id: 1, expired: true }] }).items[0].state
    ).toBe('expired')
  })

  it('reads the full code from the create response only', () => {
    expect(mapIssuedInvite({ id: 3, code: 'c0de', member_role: 'admin' })).toMatchObject({
      id: 3,
      code: 'c0de',
      memberRole: 'admin',
    })
  })

  it('validates invite validity hours', () => {
    expect(buildInviteBody('', '', '')).toEqual({
      ttl_hours: 72,
      member_role: '',
      project_id: 0,
    })
    expect(buildInviteBody('720', 'admin', '7')?.project_id).toBe(7)
    expect(buildInviteBody('721', '', '')).toBeNull()
    expect(buildInviteBody('0', '', '')).toBeNull()
    expect(buildInviteBody('1.5', '', '')).toBeNull()
  })
})

describe('error messages', () => {
  it('maps every redeem error code to its own text', () => {
    const msgs = [
      'INVITE_EXPIRED',
      'INVITE_REVOKED',
      'INVITE_ALREADY_CONSUMED',
      'INVITE_NOT_FOUND',
    ].map((code) => redeemErrorMessage(new ApiError('raw', { code }), tr))
    expect(new Set(msgs).size).toBe(4)
    expect(msgs.join('')).not.toContain('raw')
  })

  it('explains LAST_TENANT_ADMIN', () => {
    expect(
      roleErrorMessage(new ApiError('x', { status: 409, code: 'LAST_TENANT_ADMIN' }), tr)
    ).toMatch(/last administrator/)
  })
})

describe('OrgMembersPage', () => {
  it('lists members for a tenant admin and shows the last-admin error', async () => {
    route({
      '/user/me': ADMIN,
      '/projects': PROJECTS,
      '/members': MEMBERS,
      '/invites': INVITES,
    })
    // user/me is read through currentUserQueryOptions.
    put.mockRejectedValue(
      new ApiError('conflict', { status: 409, code: 'LAST_TENANT_ADMIN' })
    )
    const user = userEvent.setup()
    renderPage()
    expect(await screen.findByText('Bob')).toBeInTheDocument()
    expect(screen.getByText('Alice')).toBeInTheDocument()
    expect(screen.getByText('alice@example.com')).toBeInTheDocument()

    const buttons = screen.getAllByRole('button', { name: 'Change role' })
    await user.click(buttons[0])
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(
      await screen.findByText(/last administrator of the organization/)
    ).toBeInTheDocument()
    expect(put).toHaveBeenCalledWith('/members/1/role', { tenant_role: 'admin' })
  })

  it('shows a read failure as an error, not an empty list', async () => {
    route({
      '/user/me': ADMIN,
      '/members': new ApiError('boom', { status: 500 }),
    })
    renderPage()
    expect(await screen.findByText('Could not load members')).toBeInTheDocument()
    expect(screen.queryByText('No members yet')).not.toBeInTheDocument()
  })

  it('hides management for a plain member, leaving only redeem', async () => {
    route({ '/user/me': { ...ADMIN, id: 5, tenant_role: '' } })
    renderPage()
    expect(await screen.findByText('Redeem an invitation code')).toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: 'Members' })).not.toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: 'Invitations' })).not.toBeInTheDocument()
    expect(get).not.toHaveBeenCalledWith('/invites', expect.anything())
  })

  it('creates an invite with the 72h default and offers the link', async () => {
    route({
      '/user/me': ADMIN,
      '/projects': PROJECTS,
      '/members': MEMBERS,
      '/invites': INVITES,
    })
    post.mockResolvedValue({ id: 20, code: 'deadbeef', member_role: '', project_id: 0 })
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('Bob')
    await user.click(screen.getByRole('tab', { name: 'Invitations' }))
    expect(await screen.findByText('abcd1234...')).toBeInTheDocument()
    expect(screen.getByText('Active')).toBeInTheDocument()
    expect(screen.getByText('Revoked')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Create invitation' }))
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/invites', {
        ttl_hours: 72,
        member_role: '',
        project_id: 0,
      })
    )
    const link = await screen.findByTestId('invite-link')
    expect(link.textContent).toContain('/login?invite=deadbeef')
  })

  it('revokes a pending invite after confirmation', async () => {
    route({
      '/user/me': ADMIN,
      '/projects': PROJECTS,
      '/members': MEMBERS,
      '/invites': INVITES,
    })
    del.mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('Bob')
    await user.click(screen.getByRole('tab', { name: 'Invitations' }))
    await screen.findByText('abcd1234...')
    // Only the active invite has a revoke button.
    await user.click(screen.getByRole('button', { name: 'Revoke' }))
    const dialog = await screen.findByRole('alertdialog')
    const confirm = [...dialog.querySelectorAll('button')].find(
      (b) => b.textContent === 'Revoke'
    )
    await user.click(confirm as HTMLElement)
    await waitFor(() => expect(del).toHaveBeenCalledWith('/invites/11'))
  })

  it('shows a specific message when redeeming an expired code', async () => {
    route({ '/user/me': { ...ADMIN, tenant_role: '' } })
    post.mockRejectedValue(
      new ApiError('gone', { status: 410, code: 'INVITE_EXPIRED' })
    )
    const user = userEvent.setup()
    renderPage()
    await user.type(await screen.findByLabelText('Invitation code'), 'abc')
    await user.click(screen.getByRole('button', { name: 'Redeem' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'This invitation has expired'
    )
    expect(post).toHaveBeenCalledWith('/invites/redeem', { code: 'abc' })
  })
})
