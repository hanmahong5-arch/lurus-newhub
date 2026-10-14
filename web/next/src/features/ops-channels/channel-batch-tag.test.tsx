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

// jsdom has no Web Animations API; the dialogs' scroll areas call it.
if (!('getAnimations' in Element.prototype)) {
  Object.defineProperty(Element.prototype, 'getAnimations', {
    configurable: true,
    value: () => [],
  })
}

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
    'GET /api/channel/': listBody([
      channelRow(1, { name: 'alpha' }),
      channelRow(2, { name: 'beta' }),
    ]),
    'GET /api/group/': { ...ok, data: ['default'] },
    'GET /api/channel/models': { ...ok, data: [{ id: 'model-a' }] },
    ...extra,
  }
}

function calls(method: string, url: string): Req[] {
  return request.mock.calls
    .map((c) => c[0] as Req)
    .filter((c) => c.method === method && c.url === url)
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

async function enterBatchMode() {
  renderWithClient(<OpsChannelsPage />)
  await screen.findByText('alpha')
  await userEvent.click(screen.getByRole('switch', { name: 'Batch Operations' }))
  await userEvent.click(await screen.findByRole('checkbox', { name: 'Select all' }))
}

describe('batch operations', () => {
  it('enables every selected channel with one partial update each', async () => {
    setRoutes({ 'PUT /api/channel/': { ...ok, data: {} } })
    await enterBatchMode()

    await userEvent.click(
      await screen.findByRole('button', { name: 'Enable selected channels' })
    )

    await waitFor(() => expect(calls('PUT', '/api/channel/')).toHaveLength(2))
    expect(calls('PUT', '/api/channel/').map((c) => c.data)).toEqual([
      { id: 1, status: 1 },
      { id: 2, status: 1 },
    ])
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith('2 channel(s) enabled')
    )
  })

  it('reports a total failure instead of success', async () => {
    setRoutes({
      'PUT /api/channel/': { success: false, message: 'update refused' },
    })
    await enterBatchMode()

    await userEvent.click(
      await screen.findByRole('button', { name: 'Disable selected channels' })
    )

    await waitFor(() => expect(toastError).toHaveBeenCalledWith('update refused'))
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  it('reports how many succeeded when only some do', async () => {
    let first = true
    setRoutes({
      'PUT /api/channel/': () => {
        const body = first
          ? { ...ok, data: {} }
          : { success: false, message: 'second failed' }
        first = false
        return body
      },
    })
    await enterBatchMode()

    await userEvent.click(
      await screen.findByRole('button', { name: 'Enable selected channels' })
    )

    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith('1 channel(s) enabled')
    )
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('1 channel(s) failed to enable')
    )
  })

  it('deletes the selected ids after confirmation', async () => {
    setRoutes({ 'POST /api/channel/batch': { ...ok, data: 2 } })
    await enterBatchMode()

    await userEvent.click(
      await screen.findByRole('button', { name: 'Delete selected channels' })
    )
    const dialog = await screen.findByRole('dialog')
    expect(calls('POST', '/api/channel/batch')).toHaveLength(0)
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))

    await waitFor(() =>
      expect(calls('POST', '/api/channel/batch')).toHaveLength(1)
    )
    expect(calls('POST', '/api/channel/batch')[0]?.data).toEqual({ ids: [1, 2] })
  })

  it('sets a tag on the selected channels', async () => {
    setRoutes({ 'POST /api/channel/batch/tag': { ...ok, data: 2 } })
    await enterBatchMode()

    await userEvent.click(
      await screen.findByRole('button', { name: 'Set tag for selected channels' })
    )
    const dialog = await screen.findByRole('dialog')
    await userEvent.type(within(dialog).getByLabelText('Tag'), 'blue')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Set Tag' }))

    await waitFor(() =>
      expect(calls('POST', '/api/channel/batch/tag')).toHaveLength(1)
    )
    expect(calls('POST', '/api/channel/batch/tag')[0]?.data).toEqual({
      ids: [1, 2],
      tag: 'blue',
    })
  })
})

describe('tag mode', () => {
  it('asks the server for tag pages and groups the rows under their tag', async () => {
    window.localStorage.setItem('enable-tag-mode', 'true')
    setRoutes({
      'GET /api/channel/': listBody([
        channelRow(1, { name: 'alpha', tag: 'blue', priority: 5 }),
        channelRow(2, { name: 'beta', tag: 'blue', priority: 5 }),
      ]),
    })
    renderWithClient(<OpsChannelsPage />)

    expect(await screen.findByText(/Tag: blue/)).toBeInTheDocument()
    expect(screen.getByText('2 channels')).toBeInTheDocument()
    expect(calls('GET', '/api/channel/')[0]?.params).toMatchObject({
      tag_mode: true,
    })
    // children are collapsed until the tag row is expanded
    expect(screen.queryByText('alpha')).toBeNull()
  })

  it('enables the whole tag from its row menu', async () => {
    window.localStorage.setItem('enable-tag-mode', 'true')
    setRoutes({
      'GET /api/channel/': listBody([
        channelRow(1, { name: 'alpha', tag: 'blue' }),
      ]),
      'POST /api/channel/tag/enabled': ok,
    })
    renderWithClient(<OpsChannelsPage />)
    await screen.findByText(/Tag: blue/)

    await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: /Enable All/ }))

    await waitFor(() =>
      expect(calls('POST', '/api/channel/tag/enabled')).toHaveLength(1)
    )
    expect(calls('POST', '/api/channel/tag/enabled')[0]?.data).toEqual({
      tag: 'blue',
    })
  })
})

describe('tag editing', () => {
  const tagged = listBody([
    channelRow(1, { name: 'alpha', tag: 'blue', models: 'model-a' }),
    channelRow(2, { name: 'beta', tag: 'blue', models: 'model-a,model-b' }),
  ])

  async function openTagDialog(action: 'edit' | 'batch') {
    window.localStorage.setItem('enable-tag-mode', 'true')
    renderWithClient(<OpsChannelsPage />)
    await screen.findByText(/Tag: blue/)
    if (action === 'edit') {
      await userEvent.click(screen.getByRole('button', { name: 'Edit Tag' }))
    } else {
      await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
      await userEvent.click(await screen.findByRole('menuitem', { name: /Batch Edit/ }))
    }
    return screen.findByRole('dialog')
  }

  it("renames a tag without overwriting each channel's model list", async () => {
    setRoutes({
      'GET /api/channel/': tagged,
      // the server reports the longest list among the tag's channels
      'GET /api/channel/tag/models': { ...ok, data: 'model-a,model-b' },
      'PUT /api/channel/tag': ok,
    })
    const dialog = await openTagDialog('edit')
    const name = await within(dialog).findByLabelText(/Tag Name/)
    await userEvent.clear(name)
    await userEvent.type(name, 'green')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Save Changes' }))

    await waitFor(() => expect(calls('PUT', '/api/channel/tag')).toHaveLength(1))
    expect(calls('PUT', '/api/channel/tag')[0]?.data).toEqual({
      tag: 'blue',
      new_tag: 'green',
    })
  })

  it('shows the server message when the tag update is refused', async () => {
    setRoutes({
      'GET /api/channel/': tagged,
      'GET /api/channel/tag/models': { ...ok, data: 'model-a' },
      'PUT /api/channel/tag': { success: false, message: 'tag is locked' },
    })
    const dialog = await openTagDialog('edit')
    const name = await within(dialog).findByLabelText(/Tag Name/)
    await userEvent.clear(name)
    await userEvent.type(name, 'green')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Save Changes' }))

    await waitFor(() => expect(toastError).toHaveBeenCalledWith('tag is locked'))
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  it('batch edit sends only what changed', async () => {
    setRoutes({
      'GET /api/channel/': tagged,
      'GET /api/channel/tag/models': { ...ok, data: 'model-a,model-b' },
      'PUT /api/channel/tag': ok,
    })
    const dialog = await openTagDialog('batch')
    const mapping = await within(dialog).findByDisplayValue('blue')
    await userEvent.clear(mapping)
    await userEvent.type(mapping, 'teal')
    await userEvent.click(within(dialog).getByRole('button', { name: /Save/ }))

    await waitFor(() => expect(calls('PUT', '/api/channel/tag')).toHaveLength(1))
    expect(calls('PUT', '/api/channel/tag')[0]?.data).toEqual({
      tag: 'blue',
      new_tag: 'teal',
    })
  })
})

describe('multi-key channels', () => {
  const multi = channelRow(1, {
    name: 'pool',
    channel_info: {
      is_multi_key: true,
      multi_key_size: 2,
      multi_key_polling_index: 0,
      multi_key_mode: 'polling',
    },
  })
  const status = {
    ...ok,
    data: {
      keys: [
        { index: 0, status: 1, key_preview: 'sk-aaaaaaaa...' },
        {
          index: 1,
          status: 3,
          key_preview: 'sk-bbbbbbbb...',
          reason: 'quota exhausted',
          disabled_time: 1_700_000_000,
        },
      ],
      total: 2,
      page: 1,
      page_size: 10,
      total_pages: 1,
      enabled_count: 1,
      manual_disabled_count: 0,
      auto_disabled_count: 1,
    },
  }

  async function openKeys() {
    renderWithClient(<OpsChannelsPage />)
    await screen.findByText('pool')
    await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: /Manage Keys/ }))
    return screen.findByRole('dialog')
  }

  it('lists the key pool with per-key status', async () => {
    setRoutes({
      'GET /api/channel/': listBody([multi]),
      'POST /api/channel/multi_key/manage': status,
    })
    const dialog = await openKeys()

    expect(await within(dialog).findByText('#1')).toBeInTheDocument()
    expect(within(dialog).getByText('#2')).toBeInTheDocument()
    expect(within(dialog).getByText('quota exhausted')).toBeInTheDocument()
    expect(calls('POST', '/api/channel/multi_key/manage')[0]?.data).toMatchObject(
      { channel_id: 1, action: 'get_key_status', page: 1 }
    )
  })

  it('disables one key after confirmation', async () => {
    setRoutes({
      'GET /api/channel/': listBody([multi]),
      'POST /api/channel/multi_key/manage': (cfg: Req) =>
        cfg.data?.action === 'get_key_status' ? status : { ...ok, message: 'done' },
    })
    const dialog = await openKeys()
    await within(dialog).findByText('#1')

    await userEvent.click(within(dialog).getByRole('button', { name: 'Disable' }))
    const confirm = await screen.findByRole('alertdialog')
    await userEvent.click(within(confirm).getByRole('button', { name: 'Continue' }))

    await waitFor(() =>
      expect(
        calls('POST', '/api/channel/multi_key/manage').some(
          (c) => c.data?.action === 'disable_key' && c.data?.key_index === 0
        )
      ).toBe(true)
    )
    expect(toastSuccess).toHaveBeenCalledWith('done')
  })

  it('shows the server message when a key action fails', async () => {
    setRoutes({
      'GET /api/channel/': listBody([multi]),
      'POST /api/channel/multi_key/manage': (cfg: Req) =>
        cfg.data?.action === 'get_key_status'
          ? status
          : { success: false, message: 'key index out of range' },
    })
    const dialog = await openKeys()
    await within(dialog).findByText('#1')

    await userEvent.click(within(dialog).getByRole('button', { name: 'Enable' }))
    const confirm = await screen.findByRole('alertdialog')
    await userEvent.click(within(confirm).getByRole('button', { name: 'Continue' }))

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('key index out of range')
    )
  })

  it('shows the server message when loading the pool fails', async () => {
    setRoutes({
      'GET /api/channel/': listBody([multi]),
      'POST /api/channel/multi_key/manage': {
        success: false,
        message: 'not a multi-key channel',
      },
    })
    await openKeys()

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('not a multi-key channel')
    )
  })

  it('does not offer key management on a single-key channel', async () => {
    setRoutes({ 'GET /api/channel/': listBody([channelRow(1, { name: 'pool' })]) })
    renderWithClient(<OpsChannelsPage />)
    await screen.findByText('pool')
    await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
    await screen.findByRole('menuitem', { name: /Query Balance/ })
    expect(screen.queryByRole('menuitem', { name: /Manage Keys/ })).toBeNull()
  })
})
