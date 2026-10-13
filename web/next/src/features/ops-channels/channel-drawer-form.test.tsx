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

const STORED = channelRow(1, {
  name: 'alpha',
  models: 'model-a,model-b',
  group: 'default,vip',
  base_url: 'https://upstream.example',
  priority: 3,
  setting: JSON.stringify({
    plan_kind: 'minimax',
    plan_threshold_pct: 90,
    proxy: 'http://old-proxy:8080',
  }),
})

function setRoutes(extra: Record<string, Route> = {}) {
  routes = {
    'GET /api/channel/': listBody([STORED]),
    'GET /api/channel/1': { ...ok, data: STORED },
    'GET /api/group/': { ...ok, data: ['default', 'vip'] },
    'GET /api/channel/models_enabled': { ...ok, data: ['model-a', 'model-c'] },
    'GET /api/prefill_group/': { ...ok, data: [] },
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

async function openCreate() {
  renderWithClient(<OpsChannelsPage />)
  await screen.findByText('alpha')
  await userEvent.click(screen.getByRole('button', { name: /Create Channel/ }))
  const sheet = await screen.findByRole('dialog')
  return sheet
}

async function pickProvider(sheet: HTMLElement, name: RegExp) {
  await userEvent.click(await within(sheet).findByRole('option', { name }))
}

async function fillCreateForm(sheet: HTMLElement) {
  await userEvent.type(within(sheet).getByLabelText(/^Name/), 'fresh')
  await userEvent.type(
    within(sheet).getByPlaceholderText('Enter API key for this channel'),
    'sk-test-1'
  )
  const models = within(sheet).getByPlaceholderText(
    'Comma-separated model names, e.g., model-a,model-b'
  )
  await userEvent.type(models, 'model-a,')
  // The suggestion list stays open and hides the rest of the drawer from the
  // accessibility tree until it is dismissed.
  await userEvent.keyboard('{Escape}')
}

describe('create channel', () => {
  it('sends a single-key create with the form values', async () => {
    setRoutes({ 'POST /api/channel/': ok })
    const sheet = await openCreate()
    await pickProvider(sheet, /OpenAI #1/)
    await fillCreateForm(sheet)

    await userEvent.click(
      within(sheet).getByRole('button', { name: 'Create Channel' })
    )

    await waitFor(() => expect(calls('POST', '/api/channel/')).toHaveLength(1))
    const body = calls('POST', '/api/channel/')[0]?.data as {
      mode: string
      channel: Record<string, unknown>
    }
    expect(body.mode).toBe('single')
    expect(body.channel).toMatchObject({
      name: 'fresh',
      type: 1,
      key: 'sk-test-1',
      models: 'model-a',
      group: 'default',
      status: 1,
    })
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith('Channel created successfully')
    )
  })

  it('keeps the drawer open and shows the server message when create fails', async () => {
    setRoutes({
      'POST /api/channel/': { success: false, message: 'channel cannot be empty' },
    })
    const sheet = await openCreate()
    await pickProvider(sheet, /OpenAI #1/)
    await fillCreateForm(sheet)

    await userEvent.click(
      within(sheet).getByRole('button', { name: 'Create Channel' })
    )

    expect(await within(sheet).findByRole('alert')).toHaveTextContent(
      'channel cannot be empty'
    )
    expect(toastSuccess).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('refuses an empty form without calling the server', async () => {
    const sheet = await openCreate()
    await pickProvider(sheet, /OpenAI #1/)

    await userEvent.click(
      within(sheet).getByRole('button', { name: 'Create Channel' })
    )

    expect(
      await within(sheet).findByText('Channel name is required')
    ).toBeInTheDocument()
    expect(calls('POST', '/api/channel/')).toHaveLength(0)
  })

  it('requires a base URL for types that cannot run without one', async () => {
    const sheet = await openCreate()
    await pickProvider(sheet, /Azure #3/)
    await userEvent.type(within(sheet).getByLabelText(/^Name/), 'az')
    await userEvent.type(
      within(sheet).getByPlaceholderText('Enter API key for this channel'),
      'k'
    )
    await userEvent.type(
      within(sheet).getByPlaceholderText(
        'Comma-separated model names, e.g., model-a,model-b'
      ),
      'model-a,'
    )
    await userEvent.keyboard('{Escape}')
    await userEvent.click(
      within(sheet).getByRole('button', { name: 'Create Channel' })
    )

    expect(
      await within(sheet).findByText('Base URL is required for this channel type')
    ).toBeInTheDocument()
    expect(calls('POST', '/api/channel/')).toHaveLength(0)
  })

  it('asks before saving a redirect whose source model is not in the list', async () => {
    setRoutes({ 'POST /api/channel/': ok })
    const sheet = await openCreate()
    await pickProvider(sheet, /OpenAI #1/)
    await fillCreateForm(sheet)

    await userEvent.click(
      within(sheet).getByRole('button', { name: 'Add Mapping' })
    )
    await userEvent.type(
      within(sheet).getByLabelText('Request Model Name'),
      'ghost-model'
    )
    await userEvent.type(
      within(sheet).getByLabelText('Upstream Model Name'),
      'model-a'
    )
    await userEvent.click(
      within(sheet).getByRole('button', { name: /Create Channel/ })
    )

    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('ghost-model')
    expect(calls('POST', '/api/channel/')).toHaveLength(0)

    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Add and submit' })
    )

    await waitFor(() => expect(calls('POST', '/api/channel/')).toHaveLength(1))
    const body = calls('POST', '/api/channel/')[0]?.data as {
      channel: { models: string; model_mapping: string }
    }
    expect(body.channel.models).toBe('model-a,ghost-model')
    expect(JSON.parse(body.channel.model_mapping)).toEqual({
      'ghost-model': 'model-a',
    })
  })
})

describe('model presets', () => {
  it('adds a preset group of models to the list', async () => {
    setRoutes({
      'GET /api/prefill_group/': {
        ...ok,
        data: [{ id: 1, name: 'Starter', items: '["model-p","model-q"]' }],
      },
      'POST /api/channel/': ok,
    })
    const sheet = await openCreate()
    await pickProvider(sheet, /OpenAI #1/)
    await userEvent.type(within(sheet).getByLabelText(/^Name/), 'fresh')
    await userEvent.type(
      within(sheet).getByPlaceholderText('Enter API key for this channel'),
      'sk-p'
    )

    await userEvent.click(
      await within(sheet).findByRole('button', { name: 'Add group: Starter' })
    )
    await userEvent.click(
      within(sheet).getByRole('button', { name: /Create Channel/ })
    )

    await waitFor(() => expect(calls('POST', '/api/channel/')).toHaveLength(1))
    const body = calls('POST', '/api/channel/')[0]?.data as {
      channel: { models: string }
    }
    expect(body.channel.models).toBe('model-p,model-q')
  })
})

describe('edit channel', () => {
  it('loads the stored record, omits a blank key and keeps unmanaged setting members', async () => {
    setRoutes({ 'PUT /api/channel/': { ...ok, data: STORED } })
    renderWithClient(<OpsChannelsPage />)
    await screen.findByText('alpha')
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))

    const sheet = await screen.findByRole('dialog')
    const name = await within(sheet).findByDisplayValue('alpha')
    await userEvent.clear(name)
    await userEvent.type(name, 'alpha-2')
    await userEvent.click(
      within(sheet).getByRole('button', { name: 'Update Channel' })
    )

    await waitFor(() => expect(calls('PUT', '/api/channel/')).toHaveLength(1))
    const body = calls('PUT', '/api/channel/')[0]?.data as Record<
      string,
      unknown
    >
    expect(body).toMatchObject({
      id: 1,
      name: 'alpha-2',
      type: 1,
      models: 'model-a,model-b',
      group: 'default,vip',
      base_url: 'https://upstream.example',
      priority: 3,
    })
    expect('key' in body).toBe(false)
    const setting = JSON.parse(String(body.setting)) as Record<string, unknown>
    expect(setting).toMatchObject({
      plan_kind: 'minimax',
      plan_threshold_pct: 90,
      proxy: 'http://old-proxy:8080',
    })
  })

  it('shows the server refusal and does not close', async () => {
    setRoutes({
      'PUT /api/channel/': new ApiError('insufficient permission', {
        status: 403,
      }),
    })
    renderWithClient(<OpsChannelsPage />)
    await screen.findByText('alpha')
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    const sheet = await screen.findByRole('dialog')
    await within(sheet).findByDisplayValue('alpha')

    await userEvent.click(
      within(sheet).getByRole('button', { name: 'Update Channel' })
    )

    expect(await within(sheet).findByRole('alert')).toHaveTextContent(
      'insufficient permission'
    )
  })

  it('shows an error state when the stored record cannot be loaded', async () => {
    setRoutes({
      'GET /api/channel/1': new ApiError('gone', { status: 500 }),
    })
    renderWithClient(<OpsChannelsPage />)
    await screen.findByText('alpha')
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))

    expect(await screen.findByText('Could not load channel')).toBeInTheDocument()
    expect(screen.getByText('gone')).toBeInTheDocument()
  })

  it('reveals the stored key for the operator', async () => {
    setRoutes({
      'POST /api/channel/1/key': { ...ok, data: { key: 'sk-stored-secret' } },
    })
    renderWithClient(<OpsChannelsPage />)
    await screen.findByText('alpha')
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    const sheet = await screen.findByRole('dialog')
    await within(sheet).findByDisplayValue('alpha')

    await userEvent.click(
      within(sheet).getByRole('button', { name: 'Reveal current key' })
    )

    expect(await within(sheet).findByLabelText('Current key')).toHaveValue(
      'sk-stored-secret'
    )
  })
})

describe('fetch upstream models', () => {
  it('fetches with the unsaved base URL, type and key, then fills the list', async () => {
    setRoutes({
      'POST /api/channel/fetch_models': {
        ...ok,
        data: ['model-a', 'model-x', 'model-y'],
      },
    })
    const sheet = await openCreate()
    await pickProvider(sheet, /OpenAI #1/)
    await userEvent.type(
      within(sheet).getByPlaceholderText('Enter API key for this channel'),
      'sk-fetch'
    )

    await userEvent.click(
      within(sheet).getByRole('button', { name: 'Fetch upstream models' })
    )

    await waitFor(() =>
      expect(calls('POST', '/api/channel/fetch_models')).toHaveLength(1)
    )
    expect(calls('POST', '/api/channel/fetch_models')[0]?.data).toEqual({
      base_url: '',
      type: 1,
      key: 'sk-fetch',
    })
    expect(await screen.findByText('model-x')).toBeInTheDocument()
  })
})
