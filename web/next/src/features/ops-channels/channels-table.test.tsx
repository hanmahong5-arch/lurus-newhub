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
    'GET /api/group/': { ...ok, data: ['default', 'vip'] },
    'GET /api/channel/models_enabled': { ...ok, data: ['model-a'] },
    'GET /api/prefill_group': { ...ok, data: [] },
    ...extra,
  }
}

function lastListParams(): Record<string, unknown> | undefined {
  const calls = request.mock.calls
    .map((c) => c[0] as Req)
    .filter((c) => c.method === 'GET' && c.url === '/api/channel/')
  return calls.at(-1)?.params
}

beforeEach(() => {
  vi.clearAllMocks()
  window.localStorage.clear()
  statusGet.mockResolvedValue({
    quota_per_unit: 500000,
    quota_display_type: 'USD',
  })
  tenantGet.mockImplementation((path: string) => {
    if (path === '/user/me') {
      return Promise.resolve({
        id: 1,
        username: 'root',
        role: 100,
        quota: 0,
        used_quota: 0,
        remaining_quota: 0,
      })
    }
    return Promise.reject(new ApiError('no ops data', { status: 404 }))
  })
  request.mockImplementation((cfg: Req) => respond(cfg))
  setRoutes()
})

describe('channel table', () => {
  it('lists the v1 channels with type tabs that carry the server counts', async () => {
    setRoutes({
      'GET /api/channel/': listBody(
        [
          channelRow(1, { name: 'alpha', type: 1, group: 'default,vip' }),
          channelRow(2, { name: 'beta', type: 14 }),
          channelRow(3, { name: 'gamma', type: 14, status: 2 }),
        ],
        { '1': 4, '14': 2 }
      ),
    })
    renderWithClient(<OpsChannelsPage />)

    expect(await screen.findByText('alpha')).toBeInTheDocument()
    expect(screen.getByText('beta')).toBeInTheDocument()
    expect(screen.getByText('gamma')).toBeInTheDocument()

    const tabs = screen.getByRole('tablist', { name: 'Channel types' })
    const all = within(tabs).getByRole('tab', { name: /All Types/ })
    expect(all).toHaveTextContent('6')
    expect(within(tabs).getByRole('tab', { name: /OpenAI/ })).toHaveTextContent(
      '4'
    )
    expect(
      within(tabs).getByRole('tab', { name: /Anthropic/ })
    ).toHaveTextContent('2')
    expect(all).toHaveAttribute('aria-selected', 'true')

    // First request: 1-based page, default size, no filters.
    expect(lastListParams()).toMatchObject({ p: 1, page_size: 20 })
  })

  it('filters by type when a tab is picked', async () => {
    setRoutes({
      'GET /api/channel/': (cfg: Req) =>
        listBody(
          cfg.params?.type === 14
            ? [channelRow(2, { name: 'beta', type: 14 })]
            : [channelRow(1, { name: 'alpha' }), channelRow(2, { name: 'beta', type: 14 })],
          { '1': 1, '14': 1 }
        ),
    })
    renderWithClient(<OpsChannelsPage />)
    await screen.findByText('alpha')

    const tabs = screen.getByRole('tablist', { name: 'Channel types' })
    await userEvent.click(within(tabs).getByRole('tab', { name: /Anthropic/ }))

    await waitFor(() => expect(lastListParams()).toMatchObject({ type: 14 }))
    await waitFor(() => expect(screen.queryByText('alpha')).toBeNull())
    expect(screen.getByText('beta')).toBeInTheDocument()
    expect(
      within(tabs).getByRole('tab', { name: /Anthropic/ })
    ).toHaveAttribute('aria-selected', 'true')
  })

  it('moves between type tabs with the arrow keys', async () => {
    setRoutes({
      'GET /api/channel/': (cfg: Req) =>
        listBody(
          cfg.params?.type === 14
            ? [channelRow(2, { name: 'beta', type: 14 })]
            : [
                channelRow(1, { name: 'alpha' }),
                channelRow(2, { name: 'beta', type: 14 }),
              ],
          { '1': 1, '14': 1 }
        ),
    })
    renderWithClient(<OpsChannelsPage />)
    await screen.findByText('alpha')
    const tabs = screen.getByRole('tablist', { name: 'Channel types' })
    const all = within(tabs).getByRole('tab', { name: /All Types/ })

    all.focus()
    await userEvent.keyboard('{ArrowRight}{ArrowRight}')

    // order: All, Anthropic, OpenAI (alphabetical); two steps lands on OpenAI
    await waitFor(() => expect(lastListParams()).toMatchObject({ type: 1 }))
    expect(within(tabs).getByRole('tab', { name: /OpenAI/ })).toHaveAttribute(
      'aria-selected',
      'true'
    )
    await userEvent.keyboard('{Home}')
    await waitFor(() => expect(lastListParams()?.type).toBeUndefined())
  })

  it('searches by keyword through the search endpoint', async () => {
    setRoutes({
      'GET /api/channel/': listBody([channelRow(1, { name: 'alpha' })]),
      'GET /api/channel/search': (cfg: Req) =>
        listBody([channelRow(7, { name: `found-${String(cfg.params?.keyword)}` })]),
    })
    renderWithClient(<OpsChannelsPage />)
    await screen.findByText('alpha')

    await userEvent.type(
      screen.getByPlaceholderText('Filter by name, ID, or key...'),
      'zed'
    )
    expect(await screen.findByText('found-zed')).toBeInTheDocument()
    const search = request.mock.calls
      .map((c) => c[0] as Req)
      .find((c) => c.url === '/api/channel/search')
    expect(search?.params).toMatchObject({ keyword: 'zed', p: 1 })
  })

  it('shows an error, not an empty list, when loading fails', async () => {
    setRoutes({
      'GET /api/channel/': new ApiError('boom', { status: 500 }),
    })
    renderWithClient(<OpsChannelsPage />)
    expect(await screen.findByText('Could not load channels')).toBeInTheDocument()
    expect(screen.getByText('boom')).toBeInTheDocument()
    expect(screen.queryByText('No Channels Found')).toBeNull()
  })

  it('treats success:false on a 200 as an error too', async () => {
    setRoutes({
      'GET /api/channel/': { success: false, message: 'denied by server' },
    })
    renderWithClient(<OpsChannelsPage />)
    expect(await screen.findByText('Could not load channels')).toBeInTheDocument()
    expect(screen.getByText('denied by server')).toBeInTheDocument()
  })

  it('joins the ops fields (routable, mismatch) onto the v1 rows by id', async () => {
    setRoutes({
      'GET /api/channel/': listBody([channelRow(1), channelRow(2)]),
    })
    tenantGet.mockImplementation((path: string) => {
      if (path === '/user/me') {
        return Promise.resolve({ id: 1, username: 'root', role: 100 })
      }
      if (path === '/channels') {
        return Promise.resolve({
          channels: [
            {
              id: 1,
              name: 'channel-1',
              type: 1,
              status: 1,
              routable: true,
              key_count: 1,
              enabled_key_count: 1,
              model_modalities: { 'model-a': 'chat' },
              modality_mismatches: [
                { model: 'model-a', reason: 'adapter_unsupported' },
              ],
            },
            {
              id: 2,
              name: 'channel-2',
              type: 1,
              status: 1,
              routable: false,
              unroutable_reasons: ['balance_low'],
            },
          ],
          total: 2,
          page: 1,
          page_size: 100,
        })
      }
      return Promise.reject(new ApiError('nope', { status: 404 }))
    })
    renderWithClient(<OpsChannelsPage />)

    expect(await screen.findByText('Routable')).toBeInTheDocument()
    expect(screen.getByText('Not routable')).toBeInTheDocument()
    expect(screen.getByTestId('channel-1-mismatch')).toHaveTextContent('Mismatch')
    expect(screen.queryByTestId('channel-2-mismatch')).toBeNull()
  })

  it('renders the card view with the same row actions', async () => {
    window.localStorage.setItem('channels:view-mode', 'card')
    setRoutes({
      'GET /api/channel/': listBody([
        channelRow(1, { name: 'alpha', status: 2 }),
      ]),
      'PUT /api/channel/': { success: true, message: '' },
    })
    renderWithClient(<OpsChannelsPage />)

    expect(await screen.findByText('alpha')).toBeInTheDocument()
    // a disabled channel offers "Enable", not "Disable"
    await userEvent.click(screen.getByRole('button', { name: 'Enable' }))
    await waitFor(() =>
      expect(
        request.mock.calls
          .map((c) => c[0] as Req)
          .filter((c) => c.method === 'PUT')
          .map((c) => c.data)
      ).toEqual([{ id: 1, status: 1 }])
    )
  })
})
