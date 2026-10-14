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
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { PlaygroundPage } from './index'

type Listener = (event: { data?: string; readyState?: number }) => void

type FakeSource = {
  url: string
  options: Record<string, unknown>
  listeners: Map<string, Listener[]>
  closed: boolean
  readyState: number
}

const h = vi.hoisted(() => ({
  apiGet: vi.fn(),
  httpPost: vi.fn(),
  tenantGet: vi.fn(),
  sources: [] as unknown[],
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: { get: h.apiGet, post: vi.fn(), put: vi.fn(), delete: vi.fn() },
    http: { post: h.httpPost },
    tenantApi: {
      get: h.tenantGet,
      post: vi.fn(),
      put: vi.fn(),
      delete: vi.fn(),
    },
  }
})

vi.mock('sse.js', () => ({
  SSE: class FakeSSE {
    url: string
    options: Record<string, unknown>
    listeners = new Map<string, Listener[]>()
    closed = false
    readyState = 0
    constructor(url: string, options: Record<string, unknown>) {
      this.url = url
      this.options = options
      h.sources.push(this)
    }
    addEventListener(type: string, listener: Listener) {
      this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener])
    }
    stream() {}
    close() {
      this.closed = true
    }
  },
}))

const MODELS = ['model-a', 'model-b']
const GROUPS = {
  default: { desc: 'Default group', ratio: 1 },
  vip: { desc: 'VIP group', ratio: 2 },
}

function sources(): FakeSource[] {
  return h.sources as FakeSource[]
}

function emit(
  source: FakeSource,
  type: string,
  event: { data?: string; readyState?: number } = {}
) {
  act(() => {
    for (const listener of source.listeners.get(type) ?? []) {
      listener({ readyState: source.readyState, ...event })
    }
  })
}

function chunk(content: string) {
  return JSON.stringify({
    choices: [{ index: 0, delta: { content }, finish_reason: null }],
  })
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <PlaygroundPage />
    </QueryClientProvider>
  )
}

/** The selector renders once for phones and once for desktops. */
async function modelTrigger(model: string) {
  const labels = await screen.findAllByText(model)
  const trigger = labels
    .map((label) => label.closest('[role="combobox"]'))
    .find((el): el is HTMLElement => el !== null)
  if (!trigger) throw new Error(`no selector trigger shows ${model}`)
  return trigger
}

/**
 * Replies are rendered by the markdown renderer, which splits text into
 * per-word spans while it streams, so match on the page's flattened text.
 */
async function expectPageText(text: string) {
  await waitFor(() => expect(document.body.textContent).toContain(text))
}

async function typeAndSend(text: string) {
  const box = await screen.findByPlaceholderText('Ask anything')
  await modelTrigger('model-a')
  await userEvent.type(box, text)
  await userEvent.keyboard('{Enter}')
}

beforeEach(() => {
  vi.clearAllMocks()
  h.sources.length = 0
  window.localStorage.clear()
  h.tenantGet.mockResolvedValue({ id: 7, username: 'u', role: 1 })
  h.apiGet.mockImplementation((url: string) => {
    if (url === '/api/user/models') return Promise.resolve(MODELS)
    if (url === '/api/user/self/groups') return Promise.resolve(GROUPS)
    return Promise.reject(new Error(`unexpected GET ${url}`))
  })
})

describe('PlaygroundPage', () => {
  it('takes the default model from the API list, never a baked-in name', async () => {
    renderPage()
    expect(await modelTrigger('model-a')).toBeInTheDocument()
    expect(h.apiGet).toHaveBeenCalledWith('/api/user/models', {
      params: { group: 'default' },
    })
    expect(h.apiGet).toHaveBeenCalledWith('/api/user/self/groups')
    expect(screen.getByText('Start a playground chat')).toBeInTheDocument()
  })

  it('switches the model from the list and sends it with the request', async () => {
    renderPage()
    await userEvent.click(await modelTrigger('model-a'))
    await userEvent.click(await screen.findByText('model-b'))
    await modelTrigger('model-b')

    await userEvent.type(
      screen.getByPlaceholderText('Ask anything'),
      'hello there'
    )
    await userEvent.keyboard('{Enter}')
    await waitFor(() => expect(sources()).toHaveLength(1))
    const payload = JSON.parse(sources()[0].options.payload as string)
    expect(payload.model).toBe('model-b')
    expect(payload.group).toBe('default')
    expect(payload.messages).toEqual([{ role: 'user', content: 'hello there' }])
    expect(payload.stream).toBe(true)
  })

  it('streams the reply over the session-cookie transport and finishes on [DONE]', async () => {
    renderPage()
    await typeAndSend('ping')
    await waitFor(() => expect(sources()).toHaveLength(1))
    const source = sources()[0]
    expect(source.url).toBe('/pg/chat/completions')
    expect(source.options.withCredentials).toBe(true)
    expect(source.options.method).toBe('POST')

    emit(source, 'message', { data: chunk('Hel') })
    emit(source, 'message', { data: chunk('lo world') })
    await expectPageText('Hello world')
    expect(screen.getAllByRole('button', { name: /Stop/ })).not.toHaveLength(0)

    emit(source, 'message', { data: '[DONE]' })
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: /Stop/ })).toBeNull()
    )
    expect(source.closed).toBe(true)
    await expectPageText('ping')
  })

  it('stop keeps the partial reply and ends the request', async () => {
    renderPage()
    await typeAndSend('long one')
    await waitFor(() => expect(sources()).toHaveLength(1))
    emit(sources()[0], 'message', { data: chunk('partial answer') })
    await expectPageText('partial answer')

    await userEvent.click(screen.getAllByRole('button', { name: /Stop/ })[0])
    expect(sources()[0].closed).toBe(true)
    expect(document.body.textContent).toContain('partial answer')
    expect(screen.queryByRole('button', { name: /Stop/ })).toBeNull()
  })

  it('shows a streamed relay failure inline instead of an empty reply', async () => {
    renderPage()
    await typeAndSend('hi')
    await waitFor(() => expect(sources()).toHaveLength(1))
    emit(sources()[0], 'error', {
      data: JSON.stringify({
        error: { message: 'quota exhausted', code: 'insufficient_quota' },
      }),
    })
    await expectPageText('quota exhausted')
    expect(screen.getByRole('button', { name: /Retry/ })).toBeInTheDocument()
  })

  it('non-streaming failure surfaces the relay error body from the wrapped axios error', async () => {
    window.localStorage.setItem(
      'console_playground_config:7',
      JSON.stringify({ version: 1, data: { stream: false } })
    )
    h.httpPost.mockRejectedValue(
      new ApiError('Request failed with status code 429', {
        status: 429,
        cause: {
          response: {
            data: {
              error: { message: 'rate limit reached', code: 'rate_limited' },
            },
          },
        },
      })
    )
    renderPage()
    await typeAndSend('hi')
    await expectPageText('rate limit reached')
    expect(h.httpPost).toHaveBeenCalledWith(
      '/pg/chat/completions',
      expect.objectContaining({ stream: false, model: 'model-a' }),
      expect.anything()
    )
    expect(sources()).toHaveLength(0)
  })

  it('falls back to a group the user still has when the stored one is gone', async () => {
    window.localStorage.setItem(
      'console_playground_config:7',
      JSON.stringify({ version: 1, data: { group: 'retired' } })
    )
    renderPage()
    await waitFor(() =>
      expect(h.apiGet).toHaveBeenCalledWith('/api/user/models', {
        params: { group: 'default' },
      })
    )
    expect(await modelTrigger('model-a')).toBeInTheDocument()
  })

  it('reloads the model list for the chosen group and picks a model from it', async () => {
    h.apiGet.mockImplementation(
      (url: string, config?: { params?: { group?: string } }) => {
        if (url === '/api/user/models') {
          return Promise.resolve(
            config?.params?.group === 'vip' ? ['model-v'] : MODELS
          )
        }
        if (url === '/api/user/self/groups') return Promise.resolve(GROUPS)
        return Promise.reject(new Error(`unexpected GET ${url}`))
      }
    )
    renderPage()
    await userEvent.click(await modelTrigger('model-a'))
    await userEvent.click(await screen.findByRole('button', { name: 'vip' }))
    await waitFor(() =>
      expect(h.apiGet).toHaveBeenCalledWith('/api/user/models', {
        params: { group: 'vip' },
      })
    )
    await modelTrigger('model-v')
  })

  it('scopes the saved conversation to the signed-in account', async () => {
    window.localStorage.setItem(
      'console_playground_messages:8',
      JSON.stringify({
        version: 1,
        data: [
          {
            key: 'k1',
            from: 'user',
            versions: [{ id: 'v1', content: 'someone else secret' }],
          },
        ],
      })
    )
    renderPage()
    await screen.findByText('Start a playground chat')
    expect(screen.queryByText('someone else secret')).toBeNull()
  })

  it('previews the exact request, including the unsent draft', async () => {
    renderPage()
    const box = await screen.findByPlaceholderText('Ask anything')
    await modelTrigger('model-a')
    await userEvent.type(box, 'draft text')
    await userEvent.click(screen.getByRole('button', { name: 'View request' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Request preview')).toBeInTheDocument()
    expect(dialog.textContent).toContain('model-a')
    expect(dialog.textContent).toContain('draft text')
  })

  it('clears the conversation after confirmation', async () => {
    renderPage()
    await typeAndSend('keep me?')
    await waitFor(() => expect(sources()).toHaveLength(1))
    emit(sources()[0], 'message', { data: '[DONE]' })
    await userEvent.click(
      await screen.findByRole('button', { name: 'Clear chat history' })
    )
    const confirm = await screen.findByRole('alertdialog')
    await userEvent.click(
      within(confirm).getByRole('button', { name: 'Clear' })
    )
    await waitFor(() =>
      expect(document.body.textContent).not.toContain('keep me?')
    )
    expect(screen.getByText('Start a playground chat')).toBeInTheDocument()
  })
})
