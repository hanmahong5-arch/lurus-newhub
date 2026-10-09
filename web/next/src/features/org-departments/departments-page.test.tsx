import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'
import { parseMoneyConfig } from '@/lib/money'

import { OrgDepartmentsPage } from './index'
import {
  buildWriteBody,
  budgetRatio,
  isValidExternalCode,
  monthStartSec,
  parseUserId,
  quotaToDisplayInput,
} from './lib/form'
import { mapDepartments, mapSpend, visibleDepartments } from './lib/map'

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
const MONEY = parseMoneyConfig(STATUS) ?? (() => { throw new Error('bad money fixture') })()

function proj(over: Record<string, unknown> = {}) {
  return {
    id: 1,
    name: 'Research',
    description: 'R&D',
    monthly_budget_quota: 1000000,
    external_code: 'rnd',
    deleted: false,
    ...over,
  }
}

const ADMIN = { id: 1, role: 1, tenant_role: 'admin', quota: 0, used_quota: 0 }
const LEAD = { id: 2, role: 1, tenant_role: 'dept_lead', quota: 0, used_quota: 0 }

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
      <OrgDepartmentsPage />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  statusGet.mockResolvedValue(STATUS)
})

describe('mapping and visibility', () => {
  it('drops deleted rows and tolerates garbage', () => {
    expect(mapDepartments(null)).toEqual([])
    const rows = mapDepartments({
      items: [proj(), proj({ id: 2, deleted: true }), proj({ id: 0 })],
    })
    expect(rows.map((r) => r.id)).toEqual([1])
    expect(rows[0].monthlyBudgetQuota).toBe(1000000)
    expect(rows[0].externalCode).toBe('rnd')
  })

  it('spend ignores the unassigned bucket', () => {
    const s = mapSpend({
      items: [
        { project_id: 1, total_quota: 5 },
        { project_id: 0, total_quota: 9, unassigned: true },
      ],
    })
    expect(s.byProject).toEqual({ 1: 5 })
  })

  it('a lead only sees departments present in their own spend report', () => {
    const all = mapDepartments({ items: [proj(), proj({ id: 2, name: 'Sales' })] })
    const spend = mapSpend({ items: [{ project_id: 2, total_quota: 1 }] })
    expect(visibleDepartments(all, spend, false).map((d) => d.id)).toEqual([2])
    expect(visibleDepartments(all, undefined, false)).toEqual([])
    expect(visibleDepartments(all, spend, true)).toHaveLength(2)
  })
})

describe('form helpers', () => {
  it('external code format', () => {
    expect(isValidExternalCode('')).toBe(true)
    expect(isValidExternalCode('a.b_c@d:e-1')).toBe(true)
    expect(isValidExternalCode('a b')).toBe(true)
    expect(isValidExternalCode('研发部')).toBe(true)
    expect(isValidExternalCode('研'.repeat(21))).toBe(true)
    expect(isValidExternalCode('研'.repeat(22))).toBe(false)
    expect(isValidExternalCode('a\u0007b')).toBe(false)
    expect(isValidExternalCode('x'.repeat(64))).toBe(true)
    expect(isValidExternalCode('x'.repeat(65))).toBe(false)
  })

  it('budget goes through the unit price, empty means none', () => {
    const base = { name: ' Ops ', description: '', externalCode: ' ops ' }
    const ok = buildWriteBody({ ...base, budget: '2.5' }, MONEY)
    expect(ok).toEqual({
      ok: true,
      body: {
        name: 'Ops',
        description: '',
        monthly_budget_quota: 1250000,
        external_code: 'ops',
      },
    })
    const none = buildWriteBody({ ...base, budget: '' }, null)
    expect(none.ok && none.body.monthly_budget_quota).toBe(0)
    expect(buildWriteBody({ ...base, budget: '5' }, null)).toEqual({
      ok: false,
      error: 'budget-unavailable',
    })
    expect(buildWriteBody({ ...base, budget: '-1' }, MONEY)).toEqual({
      ok: false,
      error: 'budget-invalid',
    })
    expect(buildWriteBody({ ...base, name: ' ', budget: '' }, MONEY)).toEqual({
      ok: false,
      error: 'name',
    })
    expect(
      buildWriteBody({ ...base, externalCode: 'a\u0001b', budget: '' }, MONEY)
    ).toEqual({ ok: false, error: 'code-format' })
  })

  it('a legacy non-ASCII code stays editable', () => {
    const r = buildWriteBody(
      { name: 'Ops', description: '', externalCode: '研发部', budget: '' },
      MONEY
    )
    expect(r.ok && r.body.external_code).toBe('研发部')
  })

  it('edit input round-trips and misc helpers', () => {
    expect(quotaToDisplayInput(1250000, MONEY)).toBe('2.5')
    expect(quotaToDisplayInput(0, MONEY)).toBe('')
    expect(budgetRatio(5, 0)).toBeNull()
    expect(budgetRatio(15, 10)).toBe(1)
    expect(parseUserId(' 12 ')).toBe(12)
    expect(parseUserId('0')).toBeNull()
    expect(parseUserId('1a')).toBeNull()
    const d = new Date(2026, 9, 17, 13)
    expect(monthStartSec(d)).toBe(Math.floor(new Date(2026, 9, 1).getTime() / 1000))
  })
})

describe('OrgDepartmentsPage as tenant admin', () => {
  it('lists departments with budget and month usage, and manage actions', async () => {
    route({
      '/user/me': ADMIN,
      '/projects': { items: [proj()], total: 1 },
      '/projects/spend': { items: [{ project_id: 1, total_quota: 250000 }] },
    })
    renderPage()
    expect(await screen.findByText('Research')).toBeInTheDocument()
    expect(screen.getByText('rnd')).toBeInTheDocument()
    expect(screen.getByText('$2.00')).toBeInTheDocument()
    expect(screen.getByText('$0.50')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /New department/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Edit Research' })).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith('/projects/spend', {
      params: { start: expect.any(Number) },
    })
  })

  it('a failed read is an error with retry, not an empty list', async () => {
    route({
      '/user/me': ADMIN,
      '/projects': new ApiError('boom', { status: 500 }),
      '/projects/spend': { items: [] },
    })
    renderPage()
    expect(await screen.findByText('Could not load the departments')).toBeInTheDocument()
    expect(screen.getByText('boom')).toBeInTheDocument()
    expect(screen.queryByText('No departments yet')).not.toBeInTheDocument()
  })

  it('a failed usage read keeps the table and shows an alert', async () => {
    route({
      '/user/me': ADMIN,
      '/projects': { items: [proj()] },
      '/projects/spend': new ApiError('spend down', { status: 500 }),
    })
    renderPage()
    expect(await screen.findByText('Research')).toBeInTheDocument()
    expect(await screen.findByRole('alert')).toHaveTextContent('spend down')
  })

  it('creates a department with the budget converted to quota', async () => {
    route({
      '/user/me': ADMIN,
      '/projects': { items: [] },
      '/projects/spend': { items: [] },
    })
    post.mockResolvedValue({})
    const user = userEvent.setup()
    renderPage()
    expect(await screen.findByText('No departments yet')).toBeInTheDocument()
    await user.click(screen.getAllByRole('button', { name: /New department/ })[0])
    await user.type(await screen.findByLabelText('Name'), 'Ops')
    await user.type(screen.getByLabelText('External code'), 'ops-1')
    await user.type(screen.getByLabelText('Monthly budget'), '3')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/projects', {
        name: 'Ops',
        description: '',
        monthly_budget_quota: 1500000,
        external_code: 'ops-1',
      })
    )
  })

  it('refuses a over-long (bytes) external code without calling the server', async () => {
    route({
      '/user/me': ADMIN,
      '/projects': { items: [] },
      '/projects/spend': { items: [] },
    })
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('No departments yet')
    await user.click(screen.getAllByRole('button', { name: /New department/ })[0])
    await user.type(await screen.findByLabelText('Name'), 'Ops')
    await user.type(screen.getByLabelText('External code'), '研'.repeat(22))
    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('external code')
    expect(post).not.toHaveBeenCalled()
  })

  it('adds and removes a member', async () => {
    route({
      '/user/me': ADMIN,
      '/projects': { items: [proj()] },
      '/projects/spend': { items: [] },
      '/projects/1/members': {
        items: [
          {
            user_id: 7,
            username: 'alice',
            display_name: 'Alice',
            tenant_role: 'dept_lead',
            added_at: 1,
          },
        ],
      },
    })
    post.mockResolvedValue({})
    del.mockResolvedValue({})
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'Members of Research' }))
    const row = await screen.findByTestId('member-row-7')
    expect(within(row).getByText('Department lead')).toBeInTheDocument()

    await user.type(screen.getByLabelText('User ID'), '9')
    await user.click(screen.getByRole('button', { name: 'Add member' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/projects/1/members', { user_id: 9 })
    )

    await user.click(within(row).getByRole('button', { name: 'Remove alice' }))
    await user.click(within(row).getByRole('button', { name: 'Confirm remove' }))
    await waitFor(() =>
      expect(del).toHaveBeenCalledWith('/projects/1/members', {
        params: { user_id: 7 },
      })
    )
  })

  it('member list failure is an error, not an empty list', async () => {
    route({
      '/user/me': ADMIN,
      '/projects': { items: [proj()] },
      '/projects/spend': { items: [] },
      '/projects/1/members': new ApiError('nope', { status: 500 }),
    })
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'Members of Research' }))
    expect(await screen.findByText('Could not load the members')).toBeInTheDocument()
    expect(screen.queryByText('This department has no members yet.')).not.toBeInTheDocument()
  })

  it('deletes after confirmation', async () => {
    route({
      '/user/me': ADMIN,
      '/projects': { items: [proj()] },
      '/projects/spend': { items: [] },
    })
    del.mockResolvedValue({})
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'Delete Research' }))
    expect(del).not.toHaveBeenCalled()
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(del).toHaveBeenCalledWith('/projects/1'))
  })
})

describe('OrgDepartmentsPage as department lead', () => {
  it('shows only own departments, read only', async () => {
    route({
      '/user/me': LEAD,
      '/projects': {
        items: [proj(), proj({ id: 2, name: 'Sales', external_code: 'sales' })],
      },
      '/projects/spend': { items: [{ project_id: 2, total_quota: 500000 }] },
    })
    renderPage()
    expect(await screen.findByText('Sales')).toBeInTheDocument()
    expect(screen.queryByText('Research')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /New department/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Edit/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Delete/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Members of/ })).not.toBeInTheDocument()
    expect(screen.getByText('$1.00')).toBeInTheDocument()
  })

  it('a failed spend read is an error for a lead, not an empty page', async () => {
    route({
      '/user/me': LEAD,
      '/projects': { items: [proj()] },
      '/projects/spend': new ApiError('forbidden', { status: 403 }),
    })
    renderPage()
    expect(await screen.findByText('Could not load your departments')).toBeInTheDocument()
    expect(screen.getByText('forbidden')).toBeInTheDocument()
  })
})
