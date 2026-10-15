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

import { AdminOpenRouterSyncPage } from './index'
import {
  buildJobBody,
  formatRemaining,
  parseCategories,
  type JobFormValues,
} from './lib'

const { get, post, put, del, toastError, toastSuccess, toastInfo } = vi.hoisted(
  () => ({
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    del: vi.fn(),
    toastError: vi.fn(),
    toastSuccess: vi.fn(),
    toastInfo: vi.fn(),
  })
)

vi.mock('sonner', () => ({
  toast: { error: toastError, success: toastSuccess, info: toastInfo },
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return { ...actual, api: { get, post, put, delete: del } }
})

const HAN = /[一-鿿]/

const CATEGORIES = [
  { key: 'llm_reasoning', label: '推理语言大模型' },
  { key: 'vision', label: '多模态视觉' },
  { key: 'image_gen', label: '文生图' },
  { key: 'asr', label: '语音转文字' },
  { key: 'tts', label: '文字转语音' },
]

function job(over: Record<string, unknown> = {}) {
  return {
    id: 7,
    name: 'nightly-free',
    target_channel_id: 3,
    categories: '["llm_reasoning"]',
    top_n: 0,
    schedule: 'daily',
    enabled: false,
    last_run_at: null,
    last_error: '',
    ...over,
  }
}

const POOL = [
  {
    channel_id: 3,
    channel_name: 'or-main',
    status: 'auto_disabled',
    enabled_count: 1,
    cooling_count: 1,
    permanent_disabled_count: 1,
    key_count: 3,
    keys: [
      { index: 0, key_prefix: 'sk-or-v1-aaa***', status: 'enabled' },
      {
        index: 1,
        key_prefix: 'sk-or-v1-bbb***',
        status: 'cooling',
        cooldown_seconds_remaining: 0,
        disable_reason: 'rate limited',
      },
      {
        index: 2,
        key_prefix: 'sk-or-v1-ccc***',
        status: 'permanent_disabled',
        disable_reason: 'invalid key',
      },
    ],
  },
]

type Handlers = Record<string, unknown>

/** Route api.get by URL; an Error value rejects, a missing URL is empty. */
function route(over: Handlers = {}) {
  const handlers: Handlers = {
    '/api/openrouter-sync/jobs': [job()],
    '/api/openrouter-sync/categories': CATEGORIES,
    '/api/openrouter-sync/api-pool': POOL,
    '/api/channel/': {
      items: [
        { id: 3, name: 'or-main', type: 20 },
        { id: 4, name: 'not-openrouter', type: 1 },
      ],
    },
    ...over,
  }
  get.mockImplementation((url: string) => {
    const v = handlers[url]
    if (v instanceof Error) return Promise.reject(v)
    return Promise.resolve(v ?? [])
  })
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <AdminOpenRouterSyncPage />
    </QueryClientProvider>
  )
}

async function loaded() {
  await screen.findByTestId('sync-job-7')
}

function rowOf(id = 7) {
  return screen.getByTestId(`sync-job-${id}`)
}

beforeEach(() => {
  vi.clearAllMocks()
  route()
  post.mockResolvedValue({})
  put.mockResolvedValue({})
  del.mockResolvedValue({})
})

describe('lib', () => {
  it('parses the categories column as JSON text, an array, or garbage', () => {
    expect(parseCategories('["vision","asr"]')).toEqual(['vision', 'asr'])
    expect(parseCategories(['tts'])).toEqual(['tts'])
    expect(parseCategories(null)).toEqual([])
    expect(parseCategories('not json')).toEqual([])
    expect(parseCategories('{"a":1}')).toEqual([])
  })

  it('formats the cool-down countdown', () => {
    const t = ((k: string) => k) as never
    expect(formatRemaining(0, t)).toBe('Recovering shortly')
    expect(formatRemaining(undefined, t)).toBe('Recovering shortly')
    expect(formatRemaining(45, t)).toBe('45s')
    expect(formatRemaining(200, t)).toBe('3m 20s')
    expect(formatRemaining(7500, t)).toBe('2h 5m')
  })

  it('validates the job form', () => {
    const ok: JobFormValues = {
      name: ' daily ',
      channelId: '3',
      categories: ['vision'],
      topN: '10',
      schedule: 'daily',
      enabled: true,
    }
    expect(buildJobBody(ok)).toEqual({
      ok: true,
      body: {
        name: 'daily',
        target_channel_id: 3,
        categories: ['vision'],
        top_n: 10,
        schedule: 'daily',
        enabled: true,
      },
    })
    expect(buildJobBody({ ...ok, name: '  ' })).toEqual({
      ok: false,
      error: 'required',
    })
    expect(buildJobBody({ ...ok, channelId: '' })).toEqual({
      ok: false,
      error: 'required',
    })
    expect(buildJobBody({ ...ok, categories: [] })).toEqual({
      ok: false,
      error: 'required',
    })
    expect(buildJobBody({ ...ok, topN: '1001' })).toEqual({
      ok: false,
      error: 'top-n',
    })
    expect(buildJobBody({ ...ok, topN: '-1' })).toEqual({
      ok: false,
      error: 'top-n',
    })
    expect(buildJobBody({ ...ok, topN: '2.5' })).toEqual({
      ok: false,
      error: 'top-n',
    })
    const blank = buildJobBody({ ...ok, topN: '' })
    expect(blank.ok && blank.body.top_n).toBe(0)
  })
})

describe('job list', () => {
  it('renders the row in English with no Chinese on screen', async () => {
    const { container } = renderPage()
    await loaded()
    const row = rowOf()
    expect(row).toHaveTextContent('nightly-free')
    expect(row).toHaveTextContent('or-main (#3)')
    // The server labels are fixed Chinese; the page localises known keys.
    expect(row).toHaveTextContent('Reasoning language models')
    expect(row).toHaveTextContent('No limit')
    expect(row).toHaveTextContent('Daily (24h)')
    expect(row).toHaveTextContent('No')
    expect(row).toHaveTextContent('—')
    expect(container.textContent).toContain('OpenRouter free-model sync')
    expect(container.textContent).toContain('Last run')
    expect(container.textContent).toContain('Auto-disabled (pending recovery)')
    expect(container.textContent).toContain('Recovering shortly')
    expect(container.textContent).not.toMatch(HAN)
  })

  it('shows the Top N, the last run and the last error', async () => {
    route({
      '/api/openrouter-sync/jobs': [
        job({
          top_n: 10,
          enabled: true,
          last_run_at: '2026-10-01T08:30:00Z',
          last_error: 'upstream 502',
        }),
      ],
    })
    renderPage()
    await loaded()
    const row = rowOf()
    expect(row).toHaveTextContent('10')
    expect(row).toHaveTextContent('Yes')
    expect(row).toHaveTextContent(/2026-10-01/)
    expect(within(row).getByText('upstream 502')).toBeInTheDocument()
  })

  it('falls back to #id for a channel the picker does not list', async () => {
    route({
      '/api/openrouter-sync/jobs': [job({ target_channel_id: 99 })],
    })
    renderPage()
    await loaded()
    expect(rowOf()).toHaveTextContent('#99')
  })

  it('falls back to the raw key for an unknown category', async () => {
    route({
      '/api/openrouter-sync/jobs': [job({ categories: '["embedding"]' })],
    })
    renderPage()
    await loaded()
    expect(rowOf()).toHaveTextContent('embedding')
  })

  it('asks only for OpenRouter channels and drops any other type', async () => {
    renderPage()
    await loaded()
    expect(get).toHaveBeenCalledWith('/api/channel/', {
      params: { p: 1, page_size: 200, type: 20 },
    })
  })

  it('a failed read is an error with retry, not an empty list', async () => {
    route({
      '/api/openrouter-sync/jobs': new ApiError('boom', { status: 500 }),
    })
    renderPage()
    expect(
      await screen.findByText('Could not load the sync jobs')
    ).toBeInTheDocument()
    expect(screen.getByText('boom')).toBeInTheDocument()
    expect(screen.queryByText('No sync jobs yet')).toBeNull()
    const user = userEvent.setup()
    route()
    await user.click(screen.getAllByRole('button', { name: 'Retry' })[0])
    await loaded()
  })

  it('a 200 with success:false (thrown by the helper) is also an error', async () => {
    route({
      '/api/openrouter-sync/jobs': new ApiError('db down', { status: 200 }),
    })
    renderPage()
    expect(await screen.findByText('db down')).toBeInTheDocument()
    expect(screen.queryByText('No sync jobs yet')).toBeNull()
  })

  it('shows the empty state only for a genuinely empty list', async () => {
    route({ '/api/openrouter-sync/jobs': [] })
    renderPage()
    expect(await screen.findByText('No sync jobs yet')).toBeInTheDocument()
  })

  it('blocks create and edit while categories or channels failed to load', async () => {
    route({
      '/api/openrouter-sync/categories': new ApiError('nope', { status: 500 }),
    })
    renderPage()
    await loaded()
    await screen.findByTestId('sync-deps-error')
    expect(screen.getByRole('button', { name: 'New job' })).toBeDisabled()
    expect(within(rowOf()).getByRole('button', { name: 'Edit' })).toBeDisabled()
  })
})

describe('API pool card', () => {
  it('shows each key masked with its status, countdown and reason', async () => {
    renderPage()
    const card = await screen.findByTestId('api-pool-channel-3')
    expect(card).toHaveTextContent('or-main')
    expect(card).toHaveTextContent('(channel #3)')
    expect(card).toHaveTextContent('Enabled 1')
    expect(card).toHaveTextContent('Cooling 1')
    expect(card).toHaveTextContent('Permanently disabled 1')
    expect(card).toHaveTextContent('Total 3')
    expect(within(card).getByText('sk-or-v1-aaa***')).toBeInTheDocument()
    expect(within(card).getByText('Cooling down')).toBeInTheDocument()
    expect(within(card).getByText('Permanently disabled')).toBeInTheDocument()
    expect(within(card).getByText('rate limited')).toBeInTheDocument()
    expect(within(card).getByText('invalid key')).toBeInTheDocument()
    // Only a cooling key has a countdown.
    expect(within(card).getAllByText('Recovering shortly')).toHaveLength(1)
  })

  it('says so when no channel runs multi-key mode', async () => {
    route({ '/api/openrouter-sync/api-pool': [] })
    renderPage()
    expect(await screen.findByTestId('api-pool-empty')).toBeInTheDocument()
  })

  it('a failed pool read is an error, not "no pool configured"', async () => {
    route({
      '/api/openrouter-sync/api-pool': new ApiError('pool down', {
        status: 500,
      }),
    })
    renderPage()
    const alert = await screen.findByTestId('api-pool-error')
    expect(alert).toHaveTextContent('pool down')
    expect(screen.queryByTestId('api-pool-empty')).toBeNull()
  })

  it('keeps the last snapshot when a refresh fails', async () => {
    renderPage()
    await screen.findByTestId('api-pool-channel-3')
    route({
      '/api/openrouter-sync/api-pool': new ApiError('pool down', {
        status: 500,
      }),
    })
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: 'Refresh API pool status' })
    )
    const stale = await screen.findByTestId('api-pool-stale')
    expect(stale).toHaveTextContent('pool down')
    expect(screen.getByTestId('api-pool-channel-3')).toBeInTheDocument()
  })
})

describe('create and edit', () => {
  async function openNewJob() {
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'New job' })).toBeEnabled()
    )
    await user.click(screen.getByRole('button', { name: 'New job' }))
    await screen.findByText('New sync job')
    return user
  }

  it('renders the form in English', async () => {
    await openNewJob()
    const text = document.body.textContent ?? ''
    expect(text).toContain('Job name')
    expect(text).toContain('Top N (0 = unlimited)')
    expect(text).toContain('Speech to text')
    expect(text).toContain('Manual only')
    expect(text).not.toMatch(HAN)
  })

  it('offers only the OpenRouter channels as targets', async () => {
    await openNewJob()
    const select = screen.getByLabelText('Target channel (OpenRouter)')
    const labels = within(select as HTMLElement)
      .getAllByRole('option')
      .map((o) => o.textContent)
    expect(labels).toEqual(['Select an OpenRouter channel', 'or-main (#3)'])
  })

  it('refuses to save without name, channel and categories', async () => {
    const user = await openNewJob()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(
      await screen.findByText(
        'Name, target channel and categories are required.'
      )
    ).toBeInTheDocument()
    expect(post).not.toHaveBeenCalled()
  })

  it('refuses a Top N outside 0 to 1000', async () => {
    const user = await openNewJob()
    await user.type(screen.getByLabelText('Job name'), 'x')
    await user.selectOptions(
      screen.getByLabelText('Target channel (OpenRouter)'),
      '3'
    )
    await user.click(screen.getByRole('checkbox', { name: 'Speech to text' }))
    const topN = screen.getByLabelText('Top N (0 = unlimited)')
    await user.clear(topN)
    await user.type(topN, '1001')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(
      await screen.findByText('Top N must be a whole number from 0 to 1000.')
    ).toBeInTheDocument()
    expect(post).not.toHaveBeenCalled()
  })

  it('creates a job with the typed values', async () => {
    const user = await openNewJob()
    await user.type(screen.getByLabelText('Job name'), 'weekly-vision')
    await user.selectOptions(
      screen.getByLabelText('Target channel (OpenRouter)'),
      '3'
    )
    await user.click(
      screen.getByRole('checkbox', { name: 'Multimodal vision' })
    )
    await user.click(screen.getByRole('checkbox', { name: 'Speech to text' }))
    const topN = screen.getByLabelText('Top N (0 = unlimited)')
    await user.clear(topN)
    await user.type(topN, '5')
    await user.selectOptions(screen.getByLabelText('Schedule'), 'weekly')

    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/api/openrouter-sync/jobs', {
        name: 'weekly-vision',
        target_channel_id: 3,
        categories: ['vision', 'asr'],
        top_n: 5,
        schedule: 'weekly',
        enabled: true,
      })
    )
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Saved'))
    // The sheet closes and the list is read again.
    await waitFor(() => expect(screen.queryByText('New sync job')).toBeNull())
    expect(
      get.mock.calls.filter((c) => c[0] === '/api/openrouter-sync/jobs').length
    ).toBeGreaterThan(1)
  })

  it('keeps the form open and shows the server refusal', async () => {
    post.mockRejectedValue(
      new ApiError('invalid or missing field: categories', { status: 200 })
    )
    const user = await openNewJob()
    await user.type(screen.getByLabelText('Job name'), 'x')
    await user.selectOptions(
      screen.getByLabelText('Target channel (OpenRouter)'),
      '3'
    )
    await user.click(screen.getByRole('checkbox', { name: 'Text to image' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(
      await screen.findByText('invalid or missing field: categories')
    ).toBeInTheDocument()
    expect(screen.getByText('New sync job')).toBeInTheDocument()
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  it('edits through PUT with the full body, parsing the stored categories', async () => {
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await waitFor(() =>
      expect(
        within(rowOf()).getByRole('button', { name: 'Edit' })
      ).toBeEnabled()
    )
    await user.click(within(rowOf()).getByRole('button', { name: 'Edit' }))
    await screen.findByText('Edit job #7')
    expect(screen.getByLabelText('Job name')).toHaveValue('nightly-free')
    expect(screen.getByLabelText('Target channel (OpenRouter)')).toHaveValue(
      '3'
    )
    expect(
      screen.getByRole('checkbox', { name: 'Reasoning language models' })
    ).toBeChecked()
    expect(
      screen.getByRole('checkbox', { name: 'Text to image' })
    ).not.toBeChecked()

    await user.click(screen.getByRole('checkbox', { name: 'Text to image' }))
    await user.click(screen.getByRole('switch', { name: 'Enabled' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(put).toHaveBeenCalledWith('/api/openrouter-sync/jobs/7', {
        name: 'nightly-free',
        target_channel_id: 3,
        categories: ['llm_reasoning', 'image_gen'],
        top_n: 0,
        schedule: 'daily',
        enabled: true,
      })
    )
    expect(post).not.toHaveBeenCalled()
  })

  it('keeps a job target that the channel picker no longer lists', async () => {
    route({
      '/api/openrouter-sync/jobs': [job({ target_channel_id: 99 })],
    })
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await waitFor(() =>
      expect(
        within(rowOf()).getByRole('button', { name: 'Edit' })
      ).toBeEnabled()
    )
    await user.click(within(rowOf()).getByRole('button', { name: 'Edit' }))
    await screen.findByText('Edit job #7')
    expect(screen.getByLabelText('Target channel (OpenRouter)')).toHaveValue(
      '99'
    )
  })
})

describe('delete', () => {
  it('confirms, then deletes and reloads', async () => {
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await user.click(within(rowOf()).getByRole('button', { name: 'Delete' }))
    expect(
      await screen.findByText('Delete job "nightly-free"?')
    ).toBeInTheDocument()
    expect(del).not.toHaveBeenCalled()
    const dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    await waitFor(() =>
      expect(del).toHaveBeenCalledWith('/api/openrouter-sync/jobs/7')
    )
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith('Deleted'))
  })

  it('cancelling deletes nothing', async () => {
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await user.click(within(rowOf()).getByRole('button', { name: 'Delete' }))
    await user.click(await screen.findByRole('button', { name: 'Cancel' }))
    expect(del).not.toHaveBeenCalled()
  })

  it('surfaces a refusal instead of silently reloading', async () => {
    del.mockRejectedValue(new ApiError('job is running', { status: 200 }))
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await user.click(within(rowOf()).getByRole('button', { name: 'Delete' }))
    const dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('Delete failed', {
        description: 'job is running',
      })
    )
    expect(toastSuccess).not.toHaveBeenCalled()
  })
})

describe('run', () => {
  it('runs a job and reports what was added and removed', async () => {
    post.mockResolvedValue({ added: ['a', 'b'], removed: ['c'] })
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await user.click(within(rowOf()).getByRole('button', { name: 'Run now' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/api/openrouter-sync/jobs/7/run')
    )
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith('Run finished', {
        description: 'Added 2, removed 1',
      })
    )
  })

  it('reports a skipped run with its reason', async () => {
    post.mockResolvedValue({ skipped: true, skip_reason: 'not due yet' })
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await user.click(within(rowOf()).getByRole('button', { name: 'Run now' }))
    await waitFor(() =>
      expect(toastInfo).toHaveBeenCalledWith('Skipped', {
        description: 'not due yet',
      })
    )
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  it('warns when the circuit breaker tripped, instead of claiming success', async () => {
    post.mockResolvedValue({ circuit_breaker_on: true })
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await user.click(within(rowOf()).getByRole('button', { name: 'Run now' }))
    expect(
      await screen.findByText('Circuit breaker tripped')
    ).toBeInTheDocument()
    expect(toastSuccess).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Got it' }))
    await waitFor(() =>
      expect(screen.queryByText('Circuit breaker tripped')).toBeNull()
    )
  })

  it('shows the failure message when the run is refused', async () => {
    post.mockRejectedValue(
      new ApiError('target channel missing', { status: 200 })
    )
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await user.click(within(rowOf()).getByRole('button', { name: 'Run now' }))
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('Run failed', {
        description: 'target channel missing',
      })
    )
    expect(toastSuccess).not.toHaveBeenCalled()
    // The button comes back so the operator can try again.
    expect(
      within(rowOf()).getByRole('button', { name: 'Run now' })
    ).toBeEnabled()
  })

  it('disables both run buttons of a job while it runs', async () => {
    let finish: (v: unknown) => void = () => undefined
    post.mockReturnValue(new Promise((r) => (finish = r)))
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await user.click(within(rowOf()).getByRole('button', { name: 'Run now' }))
    await waitFor(() =>
      expect(
        within(rowOf()).getByRole('button', { name: 'Run now' })
      ).toBeDisabled()
    )
    expect(
      within(rowOf()).getByRole('button', { name: 'Force run' })
    ).toBeDisabled()
    finish({ added: [], removed: [] })
    await waitFor(() =>
      expect(
        within(rowOf()).getByRole('button', { name: 'Run now' })
      ).toBeEnabled()
    )
  })

  it('a forced run asks first and then sends force=true', async () => {
    post.mockResolvedValue({ added: [], removed: [] })
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await user.click(within(rowOf()).getByRole('button', { name: 'Force run' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(post).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button', { name: 'Force run' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/api/openrouter-sync/jobs/7/run?force=true'
      )
    )
  })

  it('cancelling the force confirmation runs nothing', async () => {
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await user.click(within(rowOf()).getByRole('button', { name: 'Force run' }))
    await user.click(await screen.findByRole('button', { name: 'Cancel' }))
    expect(post).not.toHaveBeenCalled()
  })

  it('runs every enabled job after a confirmation', async () => {
    post.mockResolvedValue({ skipped: true, skip_reason: 'no enabled jobs' })
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await user.click(screen.getByRole('button', { name: 'Run all enabled' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(post).not.toHaveBeenCalled()
    await user.click(
      within(dialog).getByRole('button', { name: 'Run all enabled' })
    )
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/api/openrouter-sync/run-all')
    )
    await waitFor(() =>
      expect(toastInfo).toHaveBeenCalledWith('Skipped', {
        description: 'no enabled jobs',
      })
    )
  })
})

describe('preview', () => {
  it('lists the models a run would import, without writing anything', async () => {
    route({
      '/api/openrouter-sync/jobs/7/preview': [
        {
          id: 'vendor-a/model-a:free',
          name: 'Model A',
          created: 1_700_000_000,
        },
        { id: 'vendor-b/model-b:free', name: 'Model B', created: 0 },
      ],
    })
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await user.click(within(rowOf()).getByRole('button', { name: 'Preview' }))
    const dialog = await screen.findByRole('dialog')
    expect(
      await within(dialog).findByText('vendor-a/model-a:free')
    ).toBeInTheDocument()
    expect(within(dialog).getByText('Model B')).toBeInTheDocument()
    expect(
      within(dialog).getByText('2023-11-1', { exact: false })
    ).toBeInTheDocument()
    expect(post).not.toHaveBeenCalled()
    expect(put).not.toHaveBeenCalled()
  })

  it('a failed dry run is an error with retry, not an empty list', async () => {
    route({
      '/api/openrouter-sync/jobs/7/preview': new ApiError(
        'upstream unreachable',
        {
          status: 200,
        }
      ),
    })
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await user.click(within(rowOf()).getByRole('button', { name: 'Preview' }))
    const err = await screen.findByTestId('preview-error')
    expect(err).toHaveTextContent('Preview failed')
    expect(err).toHaveTextContent('upstream unreachable')
    expect(screen.queryByText('No models match')).toBeNull()
  })

  it('says so when nothing matches', async () => {
    route({ '/api/openrouter-sync/jobs/7/preview': [] })
    const user = userEvent.setup()
    renderPage()
    await loaded()
    await user.click(within(rowOf()).getByRole('button', { name: 'Preview' }))
    expect(await screen.findByText('No models match')).toBeInTheDocument()
  })
})
