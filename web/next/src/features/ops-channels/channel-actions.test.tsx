import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { channelRow, listBody, renderWithClient } from './channels-test-utils'
import { OpsChannelsPage } from './index'

const { request, tenantGet, statusGet, toastError, toastSuccess } = vi.hoisted(
  () => ({
    request: vi.fn(),
    tenantGet: vi.fn(),
    statusGet: vi.fn(),
    toastError: vi.fn(),
    toastSuccess: vi.fn(),
  })
)

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    http: { request },
    tenantApi: {
      get: tenantGet,
      post: vi.fn(),
      put: vi.fn(),
      delete: vi.fn(),
    },
    api: { get: statusGet, post: vi.fn(), put: vi.fn(), delete: vi.fn() },
  }
})

vi.mock('sonner', () => ({
  toast: {
    success: toastSuccess,
    error: toastError,
    info: vi.fn(),
    loading: vi.fn(),
    dismiss: vi.fn(),
  },
}))

type Req = {
  method: string
  url: string
  params?: Record<string, unknown>
  data?: Record<string, unknown>
}
type Route = unknown | ((cfg: Req) => unknown)
let routes: Record<string, Route> = {}

function respond(cfg: Req) {
  const key = `${cfg.method} ${cfg.url}`
  const route = routes[key]
  if (route === undefined) {
    return Promise.reject(new ApiError(`unrouted ${key}`, { status: 404 }))
  }
  const value = typeof route === 'function' ? route(cfg) : route
  if (value instanceof Error) return Promise.reject(value)
  return Promise.resolve({ data: value })
}

const ok = { success: true, message: '' }

function setRoutes(extra: Record<string, Route> = {}) {
  routes = {
    'GET /api/v2/admin/channels': listBody([channelRow(1, { name: 'alpha' })]),
    'GET /api/v2/admin/groups': { ...ok, data: ['default'] },
    ...extra,
  }
}

function calls(method: string, url: string): Req[] {
  return request.mock.calls
    .map((c) => c[0] as Req)
    .filter((c) => c.method === method && c.url === url)
}

async function openRowMenu() {
  await screen.findByText('alpha')
  await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
}

beforeEach(() => {
  vi.clearAllMocks()
  window.localStorage.clear()
  statusGet.mockResolvedValue({
    quota_per_unit: 500000,
    quota_display_type: 'USD',
  })
  tenantGet.mockImplementation((path: string) =>
    path === '/user/me'
      ? Promise.resolve({ id: 1, username: 'root', role: 100 })
      : Promise.reject(new ApiError('no ops data', { status: 404 }))
  )
  request.mockImplementation((cfg: Req) => respond(cfg))
  setRoutes()
})

describe('enable and disable', () => {
  it('disables with a partial update that carries only the id and status', async () => {
    setRoutes({ 'PUT /api/v2/admin/channels': { ...ok, data: {} } })
    renderWithClient(<OpsChannelsPage />)
    await screen.findByText('alpha')

    await userEvent.click(screen.getByRole('button', { name: 'Disable' }))

    await waitFor(() => expect(calls('PUT', '/api/v2/admin/channels')).toHaveLength(1))
    expect(calls('PUT', '/api/v2/admin/channels')[0]?.data).toEqual({ id: 1, status: 2 })
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith('Channel disabled successfully')
    )
  })

  it('shows the server message when a 200 answers success:false', async () => {
    setRoutes({
      'PUT /api/v2/admin/channels': { success: false, message: 'cannot disable now' },
    })
    renderWithClient(<OpsChannelsPage />)
    await screen.findByText('alpha')

    await userEvent.click(screen.getByRole('button', { name: 'Disable' }))

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('cannot disable now')
    )
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  it('shows the server message when the request itself is refused', async () => {
    setRoutes({
      'PUT /api/v2/admin/channels': new ApiError('insufficient permission', {
        status: 403,
      }),
    })
    renderWithClient(<OpsChannelsPage />)
    await screen.findByText('alpha')

    await userEvent.click(screen.getByRole('button', { name: 'Disable' }))

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('insufficient permission')
    )
  })
})

describe('delete', () => {
  it('asks for confirmation, then deletes by id', async () => {
    setRoutes({ 'DELETE /api/v2/admin/channels/1': ok })
    renderWithClient(<OpsChannelsPage />)
    await openRowMenu()

    await userEvent.click(await screen.findByRole('menuitem', { name: /Delete/ }))
    expect(calls('DELETE', '/api/v2/admin/channels/1')).toHaveLength(0)

    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))

    await waitFor(() =>
      expect(calls('DELETE', '/api/v2/admin/channels/1')).toHaveLength(1)
    )
  })
})

describe('test connection dialog', () => {
  it('tests every model and reports each result', async () => {
    setRoutes({
      'GET /api/v2/admin/channels/test/1': (cfg: Req) =>
        cfg.params?.model === 'model-a'
          ? { success: true, message: '', time: 0.42 }
          : { success: false, message: 'upstream said no', time: 1.5 },
    })
    renderWithClient(<OpsChannelsPage />)
    await openRowMenu()
    await userEvent.click(
      await screen.findByRole('menuitem', { name: /Test Connection/ })
    )

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('model-a')).toBeInTheDocument()
    expect(within(dialog).getByText('model-b')).toBeInTheDocument()

    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Test all 2 models' })
    )

    expect(await within(dialog).findByText('Success')).toBeInTheDocument()
    expect(await within(dialog).findByText('Failed')).toBeInTheDocument()
    expect(within(dialog).getByText('upstream said no')).toBeInTheDocument()
    expect(within(dialog).getByText('420ms')).toBeInTheDocument()

    const tested = calls('GET', '/api/v2/admin/channels/test/1').map((c) => c.params?.model)
    expect(tested.sort()).toEqual(['model-a', 'model-b'])
  })

  it('tests one model with the chosen endpoint type', async () => {
    setRoutes({
      'GET /api/v2/admin/channels/test/1': { success: true, message: '', time: 0.1 },
    })
    renderWithClient(<OpsChannelsPage />)
    await openRowMenu()
    await userEvent.click(
      await screen.findByRole('menuitem', { name: /Test Connection/ })
    )
    const dialog = await screen.findByRole('dialog')

    const buttons = within(dialog).getAllByRole('button', {
      name: 'Test Connection',
    })
    const [first] = buttons
    expect(first).toBeDefined()
    await userEvent.click(first as HTMLElement)

    await waitFor(() =>
      expect(calls('GET', '/api/v2/admin/channels/test/1')).toHaveLength(1)
    )
    expect(calls('GET', '/api/v2/admin/channels/test/1')[0]?.params).toEqual({
      model: 'model-a',
    })
  })
})

describe('balance', () => {
  it('queries the upstream balance and shows it in USD', async () => {
    setRoutes({
      'GET /api/v2/admin/channels/update_balance/1': { ...ok, balance: 12.5 },
    })
    renderWithClient(<OpsChannelsPage />)
    await openRowMenu()
    await userEvent.click(
      await screen.findByRole('menuitem', { name: /Query Balance/ })
    )
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Update Balance' })
    )

    await waitFor(() =>
      expect(within(dialog).getByTestId('channel-balance')).toHaveTextContent(
        '$12.50'
      )
    )
    expect(toastSuccess).toHaveBeenCalledWith('Balance updated successfully')
  })

  it('shows the failure instead of a stale balance', async () => {
    setRoutes({
      'GET /api/v2/admin/channels': listBody([
        channelRow(1, { name: 'alpha', balance: 3 }),
      ]),
      'GET /api/v2/admin/channels/update_balance/1': {
        success: false,
        message: 'balance not supported',
      },
    })
    renderWithClient(<OpsChannelsPage />)
    await openRowMenu()
    await userEvent.click(
      await screen.findByRole('menuitem', { name: /Query Balance/ })
    )
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Update Balance' })
    )

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('balance not supported')
    )
    expect(within(dialog).getByTestId('channel-balance')).toHaveTextContent(
      '$3.00'
    )
  })
})

describe('copy', () => {
  it('copies with the chosen suffix and resets the balance', async () => {
    setRoutes({
      'POST /api/v2/admin/channels/copy/1': { ...ok, data: { id: 9 } },
    })
    renderWithClient(<OpsChannelsPage />)
    await openRowMenu()
    await userEvent.click(
      await screen.findByRole('menuitem', { name: /Copy Channel/ })
    )
    const dialog = await screen.findByRole('dialog')
    const suffix = within(dialog).getByLabelText('Name Suffix')
    await userEvent.clear(suffix)
    await userEvent.type(suffix, '-b')
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Copy Channel' })
    )

    await waitFor(() =>
      expect(calls('POST', '/api/v2/admin/channels/copy/1')).toHaveLength(1)
    )
    expect(calls('POST', '/api/v2/admin/channels/copy/1')[0]?.params).toEqual({
      suffix: '-b',
      reset_balance: true,
    })
  })
})
