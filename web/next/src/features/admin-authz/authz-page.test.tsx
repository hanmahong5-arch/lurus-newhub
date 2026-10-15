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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AxiosRequestConfig } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError, http } from '@/lib/api'

import { AdminAuthzPage } from './index'
import { EMPTY_FORM, grantStatus, parseGrantForm } from './lib/grants'
import { classifyReadError, combineFailures } from './lib/load-status'
import type { Grant } from './types'

const toast = vi.hoisted(() => ({
  success: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
}))
vi.mock('sonner', () => ({ toast }))

const CATALOG_URL = '/api/v2/admin/authz/catalog'
const GRANTS_URL = '/api/v2/admin/authz/grants'

const catalog = () => ({
  resources: [{ resource: 'audit', actions: ['read'] }],
  roles: [
    { name: 'tenant-admin', min_role: 10 },
    { name: 'root', min_role: 100 },
  ],
})

const grant = (over: Partial<Grant> = {}): Grant => ({
  id: 1,
  user_id: 42,
  resource: 'audit',
  action: 'read',
  created_at: 1_700_000_000,
  revoked_at: null,
  ...over,
})

type Reply = unknown | Error

/**
 * Drive the REAL api layer (envelope unwrap included): only the transport is
 * replaced. A plain value is answered as a successful envelope; an Error is a
 * rejected request, as the response interceptor would have shaped it.
 */
function serve(handlers: {
  catalog?: () => Reply
  grants?: () => Reply
  post?: () => Reply
  del?: (url: string) => Reply
  raw?: (config: AxiosRequestConfig) => unknown
}) {
  return vi
    .spyOn(http, 'request')
    .mockImplementation((config: AxiosRequestConfig) => {
      if (handlers.raw) {
        const out = handlers.raw(config)
        if (out !== undefined) return Promise.resolve(out) as never
      }
      const url = String(config.url)
      const method = String(config.method ?? 'GET').toUpperCase()
      let reply: Reply
      if (method === 'GET' && url === CATALOG_URL) {
        reply = (handlers.catalog ?? catalog)()
      } else if (method === 'GET' && url === GRANTS_URL) {
        reply = (handlers.grants ?? (() => []))()
      } else if (method === 'POST' && url === GRANTS_URL) {
        reply = (handlers.post ?? (() => ({ id: 9 })))()
      } else if (method === 'DELETE') {
        reply = (handlers.del ?? (() => null))(url)
      } else {
        return Promise.reject(new Error(`unexpected ${method} ${url}`)) as never
      }
      if (reply instanceof Error) return Promise.reject(reply) as never
      return Promise.resolve({
        status: 200,
        data: { success: true, data: reply },
      }) as never
    })
}

function callsTo(spy: ReturnType<typeof serve>, method: string, url: string) {
  return spy.mock.calls.filter(
    ([c]) =>
      String(c.method ?? 'GET').toUpperCase() === method &&
      String(c.url) === url
  )
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <AdminAuthzPage />
    </QueryClientProvider>
  )
}

async function fillForm(
  user: ReturnType<typeof userEvent.setup>,
  ttl?: string
) {
  await user.type(screen.getByLabelText('User ID'), '42')
  await user.selectOptions(screen.getByLabelText('Resource'), 'audit')
  await user.selectOptions(screen.getByLabelText('Action'), 'read')
  if (ttl !== undefined) {
    await user.type(screen.getByLabelText('Lifetime (seconds)'), ttl)
  }
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('grant list', () => {
  it('shows an active and a revoked grant with distinct badges; only the active one can be revoked', async () => {
    serve({
      grants: () => [
        grant({ id: 1 }),
        grant({ id: 2, user_id: 43, revoked_at: 1_700_000_100 }),
      ],
    })
    renderPage()

    await screen.findByTestId('authz-row-1')
    expect(screen.getByTestId('authz-status-1')).toHaveTextContent('Active')
    expect(screen.getByTestId('authz-status-2')).toHaveTextContent('Revoked')
    expect(screen.getByTestId('authz-revoke-1')).toBeInTheDocument()
    expect(screen.queryByTestId('authz-revoke-2')).toBeNull()
  })

  it('marks an expired grant apart from active and revoked, shows expires_at, and keeps it revocable', async () => {
    serve({
      grants: () => [
        grant({ id: 1, expires_at: 1_700_003_600, expired: false }),
        grant({
          id: 2,
          user_id: 43,
          expires_at: 1_700_000_100,
          expired: true,
        }),
        grant({
          id: 3,
          user_id: 44,
          expires_at: null,
          expired: false,
          revoked_at: 1_700_000_200,
        }),
      ],
    })
    renderPage()

    await screen.findByTestId('authz-row-1')
    expect(screen.getByTestId('authz-status-1')).toHaveTextContent('Active')
    expect(screen.getByTestId('authz-status-2')).toHaveTextContent('Expired')
    expect(screen.getByTestId('authz-status-3')).toHaveTextContent('Revoked')
    // revoked_at is genuinely still null on the expired row.
    expect(screen.getByTestId('authz-revoke-2')).toBeInTheDocument()
    expect(screen.queryByTestId('authz-revoke-3')).toBeNull()
    expect(screen.getByTestId('authz-expires-1').textContent).not.toBe('')
    expect(screen.getByTestId('authz-expires-1')).not.toHaveTextContent('Never')
    expect(screen.getByTestId('authz-expires-3')).toHaveTextContent('Never')
  })

  it('renders the empty state only when the read succeeded with no grants', async () => {
    serve({ grants: () => [] })
    renderPage()
    await screen.findByTestId('authz-empty')
    expect(screen.queryByTestId('authz-load-error')).toBeNull()
  })

  it('lists the catalog the server returned, roles included', async () => {
    serve({})
    renderPage()
    const card = await screen.findByTestId('authz-catalog')
    expect(card).toHaveTextContent('audit')
    expect(card).toHaveTextContent('read')
    expect(card).toHaveTextContent('tenant-admin (min role 10)')
    expect(card).toHaveTextContent('root (min role 100)')
  })
})

describe('creating a grant', () => {
  it('posts a global grant (tenant_id null, no ttl key) and refreshes the list', async () => {
    const spy = serve({})
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('authz-empty')

    await fillForm(user)
    await user.click(screen.getByRole('button', { name: 'Grant permission' }))

    await waitFor(() =>
      expect(callsTo(spy, 'POST', GRANTS_URL)).toHaveLength(1)
    )
    const [config] = callsTo(spy, 'POST', GRANTS_URL)[0]
    expect(config.data).toEqual({
      user_id: 42,
      resource: 'audit',
      action: 'read',
      tenant_id: null,
    })
    // An empty ttl must OMIT the key, not send null or 0.
    expect(config.data).not.toHaveProperty('ttl_seconds')
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Grant created')
    )
    await waitFor(() => expect(callsTo(spy, 'GET', GRANTS_URL)).toHaveLength(2))
    // The form is cleared after a successful create.
    expect(screen.getByLabelText('User ID')).toHaveValue(null)
  })

  it('sends ttl_seconds only when the field is filled in', async () => {
    const spy = serve({})
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('authz-empty')

    await fillForm(user, '3600')
    await user.click(screen.getByRole('button', { name: 'Grant permission' }))

    await waitFor(() =>
      expect(callsTo(spy, 'POST', GRANTS_URL)).toHaveLength(1)
    )
    expect(callsTo(spy, 'POST', GRANTS_URL)[0][0].data).toEqual({
      user_id: 42,
      resource: 'audit',
      action: 'read',
      tenant_id: null,
      ttl_seconds: 3600,
    })
  })

  it('sends ttl_seconds:0 rather than minting a permanent grant, and shows the server refusal', async () => {
    const spy = serve({
      post: () =>
        new ApiError('ttl_seconds must be between 1 and 7776000 (90 days)', {
          status: 400,
          code: 'GRANT_INVALID',
        }),
    })
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('authz-empty')

    await fillForm(user, '0')
    // The number input's own min=1 blocks a click-submit in a browser; submit
    // the form directly so parsing is exercised in isolation from that.
    fireEvent.submit(screen.getByTestId('authz-create-form'))

    await waitFor(() =>
      expect(callsTo(spy, 'POST', GRANTS_URL)).toHaveLength(1)
    )
    expect(callsTo(spy, 'POST', GRANTS_URL)[0][0].data).toEqual({
      user_id: 42,
      resource: 'audit',
      action: 'read',
      tenant_id: null,
      ttl_seconds: 0,
    })
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        'ttl_seconds must be between 1 and 7776000 (90 days)'
      )
    )
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('surfaces a duplicate-grant conflict and keeps what was typed', async () => {
    serve({
      post: () =>
        new ApiError(
          'an active grant for this user/resource/action already exists',
          {
            status: 409,
            code: 'GRANT_EXISTS',
          }
        ),
    })
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('authz-empty')

    await fillForm(user)
    await user.click(screen.getByRole('button', { name: 'Grant permission' }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        'an active grant for this user/resource/action already exists'
      )
    )
    expect(screen.getByLabelText('User ID')).toHaveValue(42)
  })

  it('offers the actions of the chosen resource only, and resets the action when the resource changes', async () => {
    serve({
      catalog: () => ({
        resources: [
          { resource: 'audit', actions: ['read'] },
          { resource: 'billing', actions: ['read', 'write'] },
        ],
        roles: [],
      }),
    })
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('authz-empty')

    expect(screen.getByLabelText('Action')).toBeDisabled()
    await user.selectOptions(screen.getByLabelText('Resource'), 'billing')
    expect(screen.getByLabelText('Action')).toBeEnabled()
    await user.selectOptions(screen.getByLabelText('Action'), 'write')
    expect(screen.getByLabelText('Action')).toHaveValue('write')

    await user.selectOptions(screen.getByLabelText('Resource'), 'audit')
    expect(screen.getByLabelText('Action')).toHaveValue('')
  })

  it('does not submit until user, resource and action are all chosen', async () => {
    const spy = serve({})
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('authz-empty')

    const submit = screen.getByRole('button', { name: 'Grant permission' })
    expect(submit).toBeDisabled()
    await user.type(screen.getByLabelText('User ID'), '42')
    expect(submit).toBeDisabled()
    await user.selectOptions(screen.getByLabelText('Resource'), 'audit')
    expect(submit).toBeDisabled()
    fireEvent.submit(screen.getByTestId('authz-create-form'))
    expect(callsTo(spy, 'POST', GRANTS_URL)).toHaveLength(0)
  })
})

describe('revoking a grant', () => {
  it('sends nothing until the confirmation is accepted, then deletes and refreshes', async () => {
    const spy = serve({ grants: () => [grant({ id: 1 })] })
    const user = userEvent.setup()
    renderPage()

    await user.click(await screen.findByTestId('authz-revoke-1'))
    expect(callsTo(spy, 'DELETE', `${GRANTS_URL}/1`)).toHaveLength(0)
    expect(await screen.findByText('Revoke this grant?')).toBeInTheDocument()

    await user.click(
      screen.getAllByRole('button', { name: 'Revoke' }).at(-1) as HTMLElement
    )

    await waitFor(() =>
      expect(callsTo(spy, 'DELETE', `${GRANTS_URL}/1`)).toHaveLength(1)
    )
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith('Grant revoked')
    )
    await waitFor(() => expect(callsTo(spy, 'GET', GRANTS_URL)).toHaveLength(2))
    await waitFor(() =>
      expect(screen.queryByText('Revoke this grant?')).toBeNull()
    )
  })

  it('sends nothing when the confirmation is cancelled', async () => {
    const spy = serve({ grants: () => [grant({ id: 1 })] })
    const user = userEvent.setup()
    renderPage()

    await user.click(await screen.findByTestId('authz-revoke-1'))
    await user.click(await screen.findByRole('button', { name: 'Cancel' }))

    await waitFor(() =>
      expect(screen.queryByText('Revoke this grant?')).toBeNull()
    )
    expect(callsTo(spy, 'DELETE', `${GRANTS_URL}/1`)).toHaveLength(0)
  })

  it('reports a failed revoke and re-reads the list, which may be stale', async () => {
    const spy = serve({
      grants: () => [grant({ id: 1 })],
      del: () => new ApiError('Grant not found', { status: 404 }),
    })
    const user = userEvent.setup()
    renderPage()

    await user.click(await screen.findByTestId('authz-revoke-1'))
    await user.click(
      (await screen.findAllByRole('button', { name: 'Revoke' })).at(
        -1
      ) as HTMLElement
    )

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Grant not found')
    )
    expect(toast.success).not.toHaveBeenCalled()
    await waitFor(() => expect(callsTo(spy, 'GET', GRANTS_URL)).toHaveLength(2))
  })
})

describe('read failures are never shown as an empty list', () => {
  it('says root is required on a 403', async () => {
    serve({
      catalog: () => new ApiError('forbidden', { status: 403 }),
      grants: () => new ApiError('forbidden', { status: 403 }),
    })
    renderPage()

    expect(
      await screen.findByText(
        'Only root can manage delegated permission grants.'
      )
    ).toBeInTheDocument()
    expect(screen.queryByTestId('authz-create-form')).toBeNull()
    expect(screen.queryByTestId('authz-empty')).toBeNull()
  })

  it('reads a v1-shaped permission refusal body (HTTP 200, success:false) as a refusal', async () => {
    serve({
      raw: () => ({
        status: 200,
        data: { success: false, message: '无权进行此操作，权限不足' },
      }),
    })
    renderPage()

    expect(
      await screen.findByText(
        'Only root can manage delegated permission grants.'
      )
    ).toBeInTheDocument()
    expect(screen.queryByTestId('authz-create-form')).toBeNull()
  })

  it('reads a PERMISSION_DENIED envelope as a refusal', async () => {
    serve({
      raw: () => ({
        status: 200,
        data: {
          success: false,
          error_code: 'PERMISSION_DENIED',
          message: 'permission denied',
        },
      }),
    })
    renderPage()
    expect(
      await screen.findByText(
        'Only root can manage delegated permission grants.'
      )
    ).toBeInTheDocument()
  })

  it('says the read failed, not that nothing is issued, when the GET 500s', async () => {
    serve({
      catalog: () => new ApiError('boom', { status: 500 }),
      grants: () => new ApiError('boom', { status: 500 }),
    })
    renderPage()

    await screen.findByTestId('authz-load-error')
    expect(screen.queryByTestId('authz-empty')).toBeNull()
    expect(screen.queryByText('No grants issued yet.')).toBeNull()
    // A read failure is not a permission refusal.
    expect(
      screen.queryByText('Only root can manage delegated permission grants.')
    ).toBeNull()
  })

  it('treats a 200 success:false that is not a refusal as a failed read', async () => {
    serve({
      raw: () => ({
        status: 200,
        data: { success: false, message: 'grant store unavailable' },
      }),
    })
    renderPage()

    const err = await screen.findByTestId('authz-load-error')
    expect(err).toHaveTextContent('grant store unavailable')
    expect(screen.queryByTestId('authz-empty')).toBeNull()
    expect(
      screen.queryByText('Only root can manage delegated permission grants.')
    ).toBeNull()
  })

  it('a failed catalog read does not let the grant list render as empty', async () => {
    serve({
      catalog: () => new ApiError('boom', { status: 500 }),
      grants: () => [],
    })
    renderPage()

    await screen.findByTestId('authz-load-error')
    expect(screen.queryByTestId('authz-empty')).toBeNull()
    expect(screen.queryByTestId('authz-create-form')).toBeNull()
  })

  it('recovers on retry once the reads succeed', async () => {
    let fail = true
    serve({
      catalog: () => (fail ? new ApiError('boom', { status: 500 }) : catalog()),
      grants: () =>
        fail ? new ApiError('boom', { status: 500 }) : [grant({ id: 7 })],
    })
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('authz-load-error')

    fail = false
    await user.click(screen.getByRole('button', { name: 'Retry' }))

    await screen.findByTestId('authz-row-7')
    expect(screen.queryByTestId('authz-load-error')).toBeNull()
  })

  it('a refresh that fails after a good load replaces the stale list with the error', async () => {
    let fail = false
    serve({
      grants: () =>
        fail ? new ApiError('boom', { status: 500 }) : [grant({ id: 1 })],
    })
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('authz-row-1')

    fail = true
    await user.click(screen.getByRole('button', { name: 'Refresh' }))

    await screen.findByTestId('authz-load-error')
    expect(screen.queryByTestId('authz-row-1')).toBeNull()
  })
})

describe('lib', () => {
  it('derives status with revoked over expired over active', () => {
    expect(grantStatus(grant())).toBe('active')
    expect(grantStatus(grant({ expired: true }))).toBe('expired')
    expect(grantStatus(grant({ expired: true, revoked_at: 5 }))).toBe('revoked')
  })

  it('parseGrantForm omits ttl only for an empty field and keeps 0', () => {
    const base = {
      ...EMPTY_FORM,
      userId: '42',
      resource: 'audit',
      action: 'read',
    }
    expect(parseGrantForm(base)).toEqual({
      userId: 42,
      resource: 'audit',
      action: 'read',
    })
    expect(parseGrantForm({ ...base, ttlSeconds: '   ' })).not.toHaveProperty(
      'ttlSeconds'
    )
    expect(parseGrantForm({ ...base, ttlSeconds: '0' })).toMatchObject({
      ttlSeconds: 0,
    })
    expect(parseGrantForm({ ...base, ttlSeconds: '3600' })).toMatchObject({
      ttlSeconds: 3600,
    })
    expect(parseGrantForm({ ...base, userId: '0' })).toBeNull()
    expect(parseGrantForm({ ...base, userId: '-3' })).toBeNull()
    expect(parseGrantForm({ ...base, action: '' })).toBeNull()
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
    expect(classifyReadError(new ApiError('store down', { status: 200 }))).toBe(
      'error'
    )
    expect(combineFailures([null, null])).toBeNull()
    expect(combineFailures(['error', 'forbidden'])).toBe('forbidden')
    expect(combineFailures([null, 'error'])).toBe('error')
  })
})
