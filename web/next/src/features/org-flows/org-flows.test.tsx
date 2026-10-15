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
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { OrgFlowsPage } from './index'
import { TOKEN_DRAFT_KEY, dateAfterDays } from './lib/token'

const { get, post, put, httpPost, statusGet, toastError, toastSuccess } =
  vi.hoisted(() => ({
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    httpPost: vi.fn(),
    statusGet: vi.fn(),
    toastError: vi.fn(),
    toastSuccess: vi.fn(),
  }))

vi.mock('sonner', () => ({
  toast: { error: toastError, success: toastSuccess },
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    tenantApi: { get, post, put, delete: vi.fn() },
    http: { post: httpPost },
    api: { get: statusGet, post: vi.fn(), put: vi.fn(), delete: vi.fn() },
  }
})

vi.mock('@tanstack/react-router', () => ({
  Link: (p: { to: string; children?: ReactNode; className?: string }) => (
    <a href={p.to} className={p.className}>
      {p.children}
    </a>
  ),
}))

const STATUS = { quota_per_unit: 500000, quota_display_type: 'USD' }

let client: QueryClient

function renderPage() {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  // The money config is read from the status query; seed it so the quota
  // field is usable from the first render.
  client.setQueryData(['status'], STATUS)
  return render(
    <QueryClientProvider client={client}>
      <OrgFlowsPage />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  window.localStorage.clear()
  statusGet.mockResolvedValue(STATUS)
})

const id = (testId: string) => screen.getByTestId(testId)
const type = (testId: string, value: string) =>
  fireEvent.change(id(testId), { target: { value } })

function goToCredentials() {
  fireEvent.click(id('newchannel-next'))
}

function fillCredentials(
  over: Partial<{
    name: string
    baseURL: string
    key: string
    models: string
    orgId: string
  }> = {}
) {
  const v = {
    name: 'openai/main-2',
    baseURL: '',
    key: 'sk-test-key-12345',
    models: 'model-a,model-b',
    orgId: '',
    ...over,
  }
  type('newchannel-name', v.name)
  if (v.baseURL) type('newchannel-baseurl', v.baseURL)
  type('newchannel-key', v.key)
  type('newchannel-models', v.models)
  if (v.orgId) type('newchannel-orgid', v.orgId)
}

/** A create that succeeded, so the step 3 / 4 tests start from a real id. */
async function createChannelAndReachStep3(models = 'model-a') {
  post.mockResolvedValueOnce({ id: 99, name: 'openai/main-2' })
  renderPage()
  goToCredentials()
  fillCredentials({ models })
  fireEvent.click(id('newchannel-create-btn'))
  await waitFor(() => expect(id('newchannel-discover-btn')).toBeInTheDocument())
}

describe('Flows page shell', () => {
  it('has exactly the two flow tabs and no placeholder copy', () => {
    renderPage()
    expect(id('flows-tab-newChannel')).toBeInTheDocument()
    expect(id('flows-tab-newToken')).toBeInTheDocument()
    expect(screen.getAllByTestId(/^flows-tab-/)).toHaveLength(2)
    expect(screen.queryByTestId('flows-tab-incident')).toBeNull()
    expect(screen.queryByTestId('flows-tab-retry')).toBeNull()
    expect(document.body.textContent).not.toMatch(
      /wired in v3|WORK IN PROGRESS/
    )
  })

  it('opens on the channel wizard at step 1 with the stepper', () => {
    renderPage()
    expect(screen.getByText('Step 1 of 4')).toBeInTheDocument()
    const rail = screen.getAllByLabelText('Setup progress')[0]
    const steps = within(rail).getAllByRole('listitem')
    // 4 steps + 3 connectors.
    expect(steps.filter((s) => s.hasAttribute('data-state'))).toHaveLength(4)
    expect(
      steps.find((s) => s.getAttribute('aria-current') === 'step')
    ).toHaveTextContent('Vendor')
  })

  it('keeps a half-finished channel wizard when switching tabs and back', async () => {
    const user = userEvent.setup()
    renderPage()
    goToCredentials()
    type('newchannel-name', 'keep-me')
    await user.click(id('flows-tab-newToken'))
    expect(id('newtoken-name')).toBeVisible()
    await user.click(id('flows-tab-newChannel'))
    expect(id('newchannel-name')).toHaveValue('keep-me')
  })
})

describe('New channel wizard', () => {
  it('creates a channel with the exact payload CreateChannelV2 parses, vendor type included', async () => {
    post.mockResolvedValueOnce({ id: 42, name: 'openai/main-2' })
    renderPage()
    fireEvent.click(id('newchannel-vendor-anthropic'))
    goToCredentials()
    fillCredentials({
      models: 'model-a,model-b',
      orgId: 'org-acme-prod',
    })
    fireEvent.click(id('newchannel-create-btn'))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    const [path, body] = post.mock.calls[0]
    expect(path).toBe('/channels')
    expect(body).toEqual({
      name: 'openai/main-2',
      type: 14,
      base_url: 'https://api.anthropic.com',
      key: 'sk-test-key-12345',
      models: 'model-a,model-b',
      openai_organization: 'org-acme-prod',
    })

    // Success moves to step 3: the create button is gone.
    await waitFor(() => {
      expect(screen.queryByTestId('newchannel-create-btn')).toBeNull()
      expect(id('newchannel-discover-btn')).toBeInTheDocument()
    })

    // Back on step 2: the persisted-channel note, locked fields, and the
    // upstream key is no longer held by the page.
    fireEvent.click(id('newchannel-back'))
    expect(id('newchannel-created-note')).toHaveTextContent('42')
    expect(id('newchannel-name')).toBeDisabled()
    expect(id('newchannel-key')).toHaveValue('')
  })

  it('refreshes the Channels page list after a create', async () => {
    post.mockResolvedValueOnce({ id: 1, name: 'n' })
    renderPage()
    const spy = vi.spyOn(client, 'invalidateQueries')
    goToCredentials()
    fillCredentials()
    fireEvent.click(id('newchannel-create-btn'))
    await waitFor(() =>
      expect(spy).toHaveBeenCalledWith({ queryKey: ['ops-channels'] })
    )
  })

  it('omits openai_organization when the field is left blank', async () => {
    post.mockResolvedValueOnce({ id: 1, name: 'openai/main' })
    renderPage()
    goToCredentials()
    fillCredentials({ orgId: '' })
    fireEvent.click(id('newchannel-create-btn'))
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post.mock.calls[0][1]).not.toHaveProperty('openai_organization')
  })

  it('keeps create disabled until name, key and models are all filled', () => {
    renderPage()
    goToCredentials()
    const btn = id('newchannel-create-btn')
    expect(btn).toBeDisabled()
    type('newchannel-name', 'n')
    type('newchannel-key', 'k')
    expect(btn).toBeDisabled()
    type('newchannel-models', '  ')
    expect(btn).toBeDisabled()
    type('newchannel-models', 'model-a')
    expect(btn).toBeEnabled()
  })

  it('rejects a multi-line key inline and never calls the API', async () => {
    renderPage()
    goToCredentials()
    // No leading/trailing whitespace (so trim() would not catch it): just an
    // internal newline, i.e. a pasted multi-key blob.
    fillCredentials({ key: 'sk-key-one\nsk-key-two' })
    fireEvent.click(id('newchannel-create-btn'))
    expect(
      await screen.findByTestId('newchannel-create-error')
    ).toHaveTextContent(/one key per channel/i)
    expect(post).not.toHaveBeenCalled()
    expect(id('newchannel-create-btn')).toBeInTheDocument()
  })

  it('blocks create for a vendor with no default host when base url is blank', async () => {
    renderPage()
    fireEvent.click(id('newchannel-vendor-custom'))
    goToCredentials()
    expect(
      screen.getByText('Required: this vendor has no default host.')
    ).toBeInTheDocument()
    fillCredentials({ baseURL: '' })
    fireEvent.click(id('newchannel-create-btn'))
    expect(
      await screen.findByTestId('newchannel-create-error')
    ).toHaveTextContent(/no default upstream host/i)
    expect(post).not.toHaveBeenCalled()
  })

  it.each([['azure'], ['bedrock'], ['vertex']])(
    'the %s vendor also needs an explicit base url',
    async (vendor) => {
      renderPage()
      fireEvent.click(id(`newchannel-vendor-${vendor}`))
      goToCredentials()
      fillCredentials({ baseURL: '' })
      if (vendor === 'vertex') {
        type('newchannel-other', '{"default":"us-central1"}')
      }
      fireEvent.click(id('newchannel-create-btn'))
      expect(
        await screen.findByTestId('newchannel-create-error')
      ).toHaveTextContent(/no default upstream host/i)
      expect(post).not.toHaveBeenCalled()
    }
  )

  it('re-picking a vendor overwrites a preset-derived base url from the previous vendor', async () => {
    post.mockResolvedValueOnce({ id: 7, name: 'zhipu/main' })
    renderPage()
    fireEvent.click(id('newchannel-vendor-anthropic'))
    fireEvent.click(id('newchannel-vendor-zhipu'))
    goToCredentials()
    fillCredentials({ baseURL: '' })
    fireEvent.click(id('newchannel-create-btn'))
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    const body = post.mock.calls[0][1]
    expect(body.type).toBe(26)
    expect(body.base_url).toBe('https://open.bigmodel.cn')
  })

  it('keeps a user-typed base url across a later vendor pick', async () => {
    post.mockResolvedValueOnce({ id: 8, name: 'zhipu/proxy' })
    renderPage()
    fireEvent.click(id('newchannel-vendor-anthropic'))
    goToCredentials()
    type('newchannel-baseurl', 'https://my-proxy.internal')
    fireEvent.click(id('newchannel-back'))
    fireEvent.click(id('newchannel-vendor-zhipu'))
    goToCredentials()
    fillCredentials({ baseURL: '' })
    fireEvent.click(id('newchannel-create-btn'))
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post.mock.calls[0][1].base_url).toBe('https://my-proxy.internal')
  })

  it('marks the picked vendor and sends the vendor type', async () => {
    renderPage()
    expect(id('newchannel-vendor-openai')).toHaveAttribute(
      'aria-checked',
      'true'
    )
    fireEvent.click(id('newchannel-vendor-siliconflow'))
    expect(id('newchannel-vendor-openai')).toHaveAttribute(
      'aria-checked',
      'false'
    )
    expect(id('newchannel-vendor-siliconflow')).toHaveAttribute(
      'aria-checked',
      'true'
    )
    post.mockResolvedValueOnce({ id: 3, name: 'n' })
    goToCredentials()
    fillCredentials()
    fireEvent.click(id('newchannel-create-btn'))
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post.mock.calls[0][1]).toMatchObject({
      type: 40,
      base_url: 'https://api.siliconflow.cn',
    })
  })

  it('Vertex needs a region document with a default entry and sends it as other', async () => {
    post.mockResolvedValueOnce({ id: 5, name: 'v' })
    renderPage()
    fireEvent.click(id('newchannel-vendor-vertex'))
    goToCredentials()
    fillCredentials({ baseURL: 'https://vertex.example.com' })

    fireEvent.click(id('newchannel-create-btn'))
    expect(
      await screen.findByTestId('newchannel-create-error')
    ).toHaveTextContent(/needs a deployment region/)
    type('newchannel-other', 'us-central1')
    fireEvent.click(id('newchannel-create-btn'))
    expect(
      await screen.findByTestId('newchannel-create-error')
    ).toHaveTextContent(/must be a JSON object/)
    expect(post).not.toHaveBeenCalled()

    type('newchannel-other', '{"default":"us-central1"}')
    fireEvent.click(id('newchannel-create-btn'))
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post.mock.calls[0][1].other).toBe('{"default":"us-central1"}')
  })

  it('surfaces a 403 PERMISSION_DENIED naming the missing grant', async () => {
    post.mockRejectedValueOnce(
      new ApiError('insufficient permission', {
        status: 403,
        code: 'PERMISSION_DENIED',
      })
    )
    renderPage()
    goToCredentials()
    fillCredentials()
    fireEvent.click(id('newchannel-create-btn'))
    const err = await screen.findByTestId('newchannel-create-error')
    expect(err).toHaveTextContent(/channel:sensitive_write/)
    // The bare backend message alone does not name the grant.
    expect(err.textContent).not.toBe('insufficient permission')
    // Still on step 2: nothing was created.
    expect(id('newchannel-create-btn')).toBeInTheDocument()
  })

  it('surfaces a generic 400 using the real backend message', async () => {
    post.mockRejectedValueOnce(
      new ApiError('Channel name too long (max 100 characters)', {
        status: 400,
      })
    )
    renderPage()
    goToCredentials()
    fillCredentials()
    fireEvent.click(id('newchannel-create-btn'))
    expect(
      await screen.findByTestId('newchannel-create-error')
    ).toHaveTextContent(/^Channel name too long \(max 100 characters\)$/)
  })

  it('a create answer without an id is an error, not a half-created wizard', async () => {
    post.mockResolvedValueOnce({})
    renderPage()
    goToCredentials()
    fillCredentials()
    fireEvent.click(id('newchannel-create-btn'))
    expect(
      await screen.findByTestId('newchannel-create-error')
    ).toHaveTextContent('The server did not return the new channel id')
    expect(screen.queryByTestId('newchannel-discover-btn')).toBeNull()
  })

  it('discovers upstream models and PUTs the merged list on apply', async () => {
    await createChannelAndReachStep3('model-a')
    get.mockResolvedValueOnce({
      upstream: ['model-a', 'model-b'],
      new: ['model-b'],
      missing: [],
    })
    fireEvent.click(id('newchannel-discover-btn'))
    await waitFor(() => expect(get).toHaveBeenCalledTimes(1))
    expect(get.mock.calls[0][0]).toBe('/channels/99/upstream-models')

    // The new model is pre-selected and has a checkbox; the configured one
    // has nothing to add.
    const box = await screen.findByTestId('newchannel-model-model-b')
    expect(box).toHaveAttribute('aria-checked', 'true')
    expect(screen.queryByTestId('newchannel-model-model-a')).toBeNull()
    expect(screen.getByText('Configured')).toBeInTheDocument()
    expect(screen.getByText('Not added yet')).toBeInTheDocument()

    put.mockResolvedValueOnce({})
    fireEvent.click(id('newchannel-apply-models-btn'))
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    // Merge of the original models (set at create) with the selected one,
    // never a blind overwrite.
    expect(put.mock.calls[0]).toEqual([
      '/channels/99',
      { models: 'model-a,model-b' },
    ])
    expect(await screen.findByTestId('newchannel-test-btn')).toBeInTheDocument()
  })

  it('lets the person untick a model; apply is disabled with nothing selected', async () => {
    const user = userEvent.setup()
    await createChannelAndReachStep3('model-a')
    get.mockResolvedValueOnce({
      upstream: ['model-b', 'model-c'],
      new: ['model-b', 'model-c'],
      missing: [],
    })
    fireEvent.click(id('newchannel-discover-btn'))
    await screen.findByTestId('newchannel-model-model-b')
    expect(id('newchannel-apply-models-btn')).toBeEnabled()

    await user.click(id('newchannel-model-model-b'))
    put.mockResolvedValueOnce({})
    fireEvent.click(id('newchannel-apply-models-btn'))
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put.mock.calls[0][1]).toEqual({ models: 'model-a,model-c' })
  })

  it('apply stays disabled until models are discovered and selected', async () => {
    await createChannelAndReachStep3()
    expect(id('newchannel-apply-models-btn')).toBeDisabled()
    get.mockResolvedValueOnce({ upstream: ['model-a'], new: [], missing: [] })
    fireEvent.click(id('newchannel-discover-btn'))
    await screen.findByText('Configured')
    expect(id('newchannel-apply-models-btn')).toBeDisabled()
  })

  it('shows an upstream with no models and the models it no longer serves', async () => {
    await createChannelAndReachStep3()
    get.mockResolvedValueOnce({ upstream: [], new: [], missing: ['model-a'] })
    fireEvent.click(id('newchannel-discover-btn'))
    expect(
      await screen.findByText('The upstream returned no models.')
    ).toBeInTheDocument()
    expect(id('newchannel-missing-note')).toHaveTextContent('model-a')
  })

  it('a failed discovery renders the error, never an empty list', async () => {
    await createChannelAndReachStep3()
    get.mockRejectedValueOnce(
      new ApiError('upstream returned HTTP 502', { status: 502 })
    )
    fireEvent.click(id('newchannel-discover-btn'))
    expect(
      await screen.findByTestId('newchannel-discover-error')
    ).toHaveTextContent('upstream returned HTTP 502')
    expect(screen.queryByRole('table')).toBeNull()
  })

  it('a failed model update keeps the wizard on step 3 with the error', async () => {
    await createChannelAndReachStep3('model-a')
    get.mockResolvedValueOnce({
      upstream: ['model-b'],
      new: ['model-b'],
      missing: [],
    })
    fireEvent.click(id('newchannel-discover-btn'))
    await screen.findByTestId('newchannel-model-model-b')
    put.mockRejectedValueOnce(
      new ApiError('insufficient permission', {
        status: 403,
        code: 'PERMISSION_DENIED',
      })
    )
    fireEvent.click(id('newchannel-apply-models-btn'))
    expect(
      await screen.findByTestId('newchannel-discover-error')
    ).toHaveTextContent(/channel:sensitive_write/)
    expect(screen.queryByTestId('newchannel-test-btn')).toBeNull()
    // The discovered list survives the failure so it can be retried.
    expect(id('newchannel-model-model-b')).toBeInTheDocument()
  })

  it('skip reaches the review step without any PUT', async () => {
    await createChannelAndReachStep3()
    fireEvent.click(id('newchannel-skip-models-btn'))
    expect(await screen.findByTestId('newchannel-test-btn')).toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
  })

  it('the review shows name, base url fallback and model count', async () => {
    await createChannelAndReachStep3('model-a, model-b ,model-a')
    fireEvent.click(id('newchannel-skip-models-btn'))
    const review = within(await screen.findByTestId('newchannel-review'))
    expect(review.getByText('openai/main-2')).toBeInTheDocument()
    expect(review.getByText('Provider default')).toBeInTheDocument()
    // De-duplicated, trimmed count.
    expect(review.getByText('2')).toBeInTheDocument()
  })

  it('the review of a no-default-host vendor says "Not set" only when blank', async () => {
    post.mockResolvedValueOnce({ id: 11, name: 'custom/x' })
    renderPage()
    fireEvent.click(id('newchannel-vendor-custom'))
    goToCredentials()
    fillCredentials({ baseURL: 'https://llm.example.com' })
    fireEvent.click(id('newchannel-create-btn'))
    await screen.findByTestId('newchannel-discover-btn')
    fireEvent.click(id('newchannel-skip-models-btn'))
    const review = within(await screen.findByTestId('newchannel-review'))
    expect(review.getByText('https://llm.example.com')).toBeInTheDocument()
    expect(review.queryByText('Not set')).toBeNull()
  })

  it('tests the channel: success shows latency, failure shows the error', async () => {
    await createChannelAndReachStep3()
    fireEvent.click(id('newchannel-skip-models-btn'))
    await screen.findByTestId('newchannel-test-btn')

    httpPost.mockResolvedValueOnce({ data: { success: true, latency_ms: 88 } })
    fireEvent.click(id('newchannel-test-btn'))
    await waitFor(() =>
      expect(id('newchannel-test-result')).toHaveTextContent(/88ms/)
    )
    expect(httpPost).toHaveBeenCalledWith('/api/v2/~/channels/99/test', {})

    // TestChannelV2's own failure is HTTP 200 + success:false, not a reject.
    httpPost.mockResolvedValueOnce({
      data: {
        success: false,
        latency_ms: 12,
        error: 'upstream returned HTTP 500',
      },
    })
    fireEvent.click(id('newchannel-test-btn'))
    await waitFor(() =>
      expect(id('newchannel-test-result')).toHaveTextContent(
        'Unreachable: upstream returned HTTP 500'
      )
    )
  })

  it('a rejected test call is shown as unreachable with the described error', async () => {
    await createChannelAndReachStep3()
    fireEvent.click(id('newchannel-skip-models-btn'))
    await screen.findByTestId('newchannel-test-btn')
    httpPost.mockRejectedValueOnce(
      new ApiError('Channel has no base URL configured', { status: 400 })
    )
    fireEvent.click(id('newchannel-test-btn'))
    await waitFor(() =>
      expect(id('newchannel-test-result')).toHaveTextContent(
        'Unreachable: Channel has no base URL configured'
      )
    )
  })

  it('finish resets the wizard to a blank step 1', async () => {
    await createChannelAndReachStep3()
    fireEvent.click(id('newchannel-skip-models-btn'))
    fireEvent.click(await screen.findByTestId('newchannel-finish-btn'))
    expect(screen.getByText('Step 1 of 4')).toBeInTheDocument()
    expect(id('newchannel-vendor-openai')).toBeEnabled()
    goToCredentials()
    expect(id('newchannel-name')).toHaveValue('')
    expect(id('newchannel-name')).toBeEnabled()
    expect(id('newchannel-create-btn')).toBeDisabled()
  })

  it('after a create the wizard can move forward again from step 2', async () => {
    await createChannelAndReachStep3()
    fireEvent.click(id('newchannel-back'))
    fireEvent.click(id('newchannel-continue-btn'))
    expect(id('newchannel-discover-btn')).toBeInTheDocument()
  })

  it('does not let the vendor change once the channel exists', async () => {
    await createChannelAndReachStep3()
    fireEvent.click(id('newchannel-back'))
    fireEvent.click(id('newchannel-back'))
    expect(id('newchannel-vendor-anthropic')).toBeDisabled()
  })
})

// ---------------------------------------------------------------------------

async function openTokenTab() {
  renderPage()
  const user = userEvent.setup()
  await user.click(id('flows-tab-newToken'))
  return user
}

function fillName(name: string, group = '') {
  type('newtoken-name', name)
  if (group) type('newtoken-group', group)
}

function nextToken() {
  fireEvent.click(id('newtoken-next'))
}

async function reachReview(name = 'my-key') {
  const user = await openTokenTab()
  fillName(name)
  nextToken()
  nextToken()
  await screen.findByTestId('newtoken-open-confirm')
  return user
}

function confirmDialog() {
  return within(screen.getByRole('alertdialog'))
}

describe('New key wizard', () => {
  it('walks 3 steps, confirms by typing the name, posts and shows the key once', async () => {
    post.mockResolvedValueOnce({ id: 7, name: 'my-key', key: 'sk-abc123' })
    const user = await openTokenTab()
    expect(screen.getByText('Step 1 of 3')).toBeInTheDocument()

    fillName('my-key', 'team-a')
    nextToken()

    // Step 2: limited quota of $0.50 -> 250000 internal units.
    await user.click(id('newtoken-unlimited'))
    type('newtoken-quota', '0.5')
    nextToken()

    // Step 3: review rows.
    const review = within(await screen.findByTestId('newtoken-review'))
    expect(review.getByText('my-key')).toBeInTheDocument()
    expect(review.getByText('team-a')).toBeInTheDocument()
    expect(review.getByText('$0.50')).toBeInTheDocument()
    expect(review.getByText('Never')).toBeInTheDocument()

    await user.click(id('newtoken-open-confirm'))
    const dialog = confirmDialog()
    await user.type(dialog.getByTestId('newtoken-confirm-input'), 'my-key')
    await user.click(dialog.getByRole('button', { name: 'Create key' }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post.mock.calls[0]).toEqual([
      '/tokens',
      {
        name: 'my-key',
        group: 'team-a',
        unlimited_quota: false,
        remain_quota: 250000,
        expired_time: -1,
      },
    ])

    expect(await screen.findByTestId('newtoken-key')).toHaveTextContent(
      'sk-abc123'
    )
    expect(screen.getByRole('button', { name: 'Copy key' })).toBeInTheDocument()
    // Draft is gone once the key is issued.
    expect(window.localStorage.getItem(TOKEN_DRAFT_KEY)).toBeNull()
  })

  it('sends the default group and zero quota for an unlimited key', async () => {
    post.mockResolvedValueOnce({ id: 1, name: 'k', key: 'sk-x' })
    const user = await reachReview('k')
    await user.click(id('newtoken-open-confirm'))
    const dialog = confirmDialog()
    await user.type(dialog.getByTestId('newtoken-confirm-input'), 'k')
    await user.click(dialog.getByRole('button', { name: 'Create key' }))
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post.mock.calls[0][1]).toEqual({
      name: 'k',
      group: 'default',
      unlimited_quota: true,
      remain_quota: 0,
      expired_time: -1,
    })
  })

  it('sends the expiry as the end of that local day in unix seconds', async () => {
    post.mockResolvedValueOnce({ id: 1, name: 'k', key: 'sk-x' })
    const user = await openTokenTab()
    fillName('k')
    nextToken()
    const day = dateAfterDays(new Date(), 30)
    type('newtoken-expires', day)
    nextToken()
    await user.click(await screen.findByTestId('newtoken-open-confirm'))
    const dialog = confirmDialog()
    await user.type(dialog.getByTestId('newtoken-confirm-input'), 'k')
    await user.click(dialog.getByRole('button', { name: 'Create key' }))
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post.mock.calls[0][1].expired_time).toBe(
      Math.floor(new Date(`${day}T23:59:59`).getTime() / 1000)
    )
  })

  it('persists the draft across step navigation and a remount', async () => {
    const user = await openTokenTab()
    fillName('persistent-key')
    nextToken()
    await user.click(id('newtoken-back'))
    expect(id('newtoken-name')).toHaveValue('persistent-key')
    expect(
      JSON.parse(window.localStorage.getItem(TOKEN_DRAFT_KEY) ?? '{}')
    ).toMatchObject({
      name: 'persistent-key',
    })
  })

  it('restores a saved draft on a fresh mount', async () => {
    window.localStorage.setItem(
      TOKEN_DRAFT_KEY,
      JSON.stringify({
        name: 'saved',
        group: 'g1',
        unlimited: true,
        cap: '',
        expires: '',
      })
    )
    await openTokenTab()
    expect(id('newtoken-name')).toHaveValue('saved')
    expect(id('newtoken-group')).toHaveValue('g1')
  })

  it('ignores a corrupt saved draft', async () => {
    window.localStorage.setItem(TOKEN_DRAFT_KEY, '{not json')
    await openTokenTab()
    expect(id('newtoken-name')).toHaveValue('')
  })

  it('blocks submit unless the typed name matches exactly', async () => {
    const user = await reachReview('exact-name')
    await user.click(id('newtoken-open-confirm'))
    const dialog = confirmDialog()
    const confirm = dialog.getByRole('button', { name: 'Create key' })
    expect(confirm).toBeDisabled()
    const input = dialog.getByTestId('newtoken-confirm-input')
    await user.type(input, 'wrong-name')
    expect(confirm).toBeDisabled()
    await user.clear(input)
    await user.type(input, 'exact')
    expect(confirm).toBeDisabled()
    expect(post).not.toHaveBeenCalled()
    await user.type(input, '-name')
    expect(confirm).toBeEnabled()
  })

  it('states the consequences in the confirmation', async () => {
    const user = await reachReview()
    await user.click(id('newtoken-open-confirm'))
    const dialog = confirmDialog()
    expect(
      dialog.getByText('The key is shown only once after creation.')
    ).toBeInTheDocument()
    expect(
      dialog.getByText('Once created it counts against your quota immediately.')
    ).toBeInTheDocument()
  })

  it('needs a name before leaving step 1', async () => {
    await openTokenTab()
    nextToken()
    expect(await screen.findByTestId('newtoken-step-error')).toHaveTextContent(
      'Enter a name for the key.'
    )
    expect(screen.getByText('Step 1 of 3')).toBeInTheDocument()
    fillName('   ')
    nextToken()
    expect(screen.getByText('Step 1 of 3')).toBeInTheDocument()
  })

  it('rejects a name over 50 bytes (a Chinese character takes 3)', async () => {
    await openTokenTab()
    fillName('密'.repeat(17))
    nextToken()
    expect(await screen.findByTestId('newtoken-step-error')).toHaveTextContent(
      /longer than 50 bytes/
    )
    fillName('密'.repeat(16))
    nextToken()
    expect(screen.getByText('Step 2 of 3')).toBeInTheDocument()
  })

  it.each([
    ['', 'Enter a quota greater than zero'],
    ['0', 'Enter a quota greater than zero'],
    ['-3', 'Enter a quota greater than zero'],
    ['abc', 'Enter a quota greater than zero'],
    ['9999999999999', 'The quota exceeds the maximum allowed.'],
  ])('a limited key with quota %j is rejected', async (value, message) => {
    const user = await openTokenTab()
    fillName('k')
    nextToken()
    await user.click(id('newtoken-unlimited'))
    type('newtoken-quota', value)
    nextToken()
    expect(await screen.findByTestId('newtoken-step-error')).toHaveTextContent(
      message
    )
    expect(screen.getByText('Step 2 of 3')).toBeInTheDocument()
  })

  it('rejects an expiry in the past', async () => {
    await openTokenTab()
    fillName('k')
    nextToken()
    type('newtoken-expires', '2001-01-01')
    nextToken()
    expect(await screen.findByTestId('newtoken-step-error')).toHaveTextContent(
      'The expiry date must be in the future.'
    )
  })

  it('a limited key cannot be set while the currency settings are missing', async () => {
    const user = await openTokenTab()
    client.setQueryData(['status'], {})
    fillName('k')
    nextToken()
    await user.click(id('newtoken-unlimited'))
    type('newtoken-quota', '5')
    nextToken()
    expect(await screen.findByTestId('newtoken-step-error')).toHaveTextContent(
      /currency settings have not loaded/
    )
  })

  it('expiry quick presets fill the date', async () => {
    const user = await openTokenTab()
    fillName('k')
    nextToken()
    await user.click(screen.getByRole('button', { name: '30 days' }))
    expect(id('newtoken-expires')).toHaveValue(dateAfterDays(new Date(), 30))
    await user.click(screen.getByRole('button', { name: 'Never' }))
    expect(id('newtoken-expires')).toHaveValue('')
  })

  it('a failed create shows the server message, toasts and shows no key', async () => {
    post.mockRejectedValueOnce(
      new ApiError('Token name already exists', { status: 400 })
    )
    const user = await reachReview('dup')
    await user.click(id('newtoken-open-confirm'))
    const dialog = confirmDialog()
    await user.type(dialog.getByTestId('newtoken-confirm-input'), 'dup')
    await user.click(dialog.getByRole('button', { name: 'Create key' }))

    expect(
      await screen.findByTestId('newtoken-create-error')
    ).toHaveTextContent('Token name already exists')
    expect(toastError).toHaveBeenCalledWith('Token name already exists')
    expect(screen.queryByTestId('newtoken-key')).toBeNull()
    expect(screen.queryByRole('alertdialog')).toBeNull()
    // The draft is kept so it can be retried.
    expect(window.localStorage.getItem(TOKEN_DRAFT_KEY)).toContain('dup')
  })

  it('an answer without a key is an error, never an empty success screen', async () => {
    post.mockResolvedValueOnce({ id: 3, name: 'k' })
    const user = await reachReview('k')
    await user.click(id('newtoken-open-confirm'))
    const dialog = confirmDialog()
    await user.type(dialog.getByTestId('newtoken-confirm-input'), 'k')
    await user.click(dialog.getByRole('button', { name: 'Create key' }))
    expect(
      await screen.findByTestId('newtoken-create-error')
    ).toHaveTextContent('The server did not return the new key')
    expect(screen.queryByTestId('newtoken-success')).toBeNull()
  })

  it('refreshes the Keys page list after issuing', async () => {
    post.mockResolvedValueOnce({ id: 3, name: 'k', key: 'sk-1' })
    const user = await reachReview('k')
    const spy = vi.spyOn(client, 'invalidateQueries')
    await user.click(id('newtoken-open-confirm'))
    const dialog = confirmDialog()
    await user.type(dialog.getByTestId('newtoken-confirm-input'), 'k')
    await user.click(dialog.getByRole('button', { name: 'Create key' }))
    await screen.findByTestId('newtoken-key')
    expect(spy).toHaveBeenCalledWith({ queryKey: ['keys'] })
  })

  it('"Create another key" drops the secret and starts clean', async () => {
    post.mockResolvedValueOnce({ id: 3, name: 'k', key: 'sk-secret-1' })
    const user = await reachReview('k')
    await user.click(id('newtoken-open-confirm'))
    const dialog = confirmDialog()
    await user.type(dialog.getByTestId('newtoken-confirm-input'), 'k')
    await user.click(dialog.getByRole('button', { name: 'Create key' }))
    await screen.findByTestId('newtoken-key')

    await user.click(id('newtoken-another-btn'))
    expect(screen.queryByText('sk-secret-1')).toBeNull()
    expect(id('newtoken-name')).toHaveValue('')
    expect(screen.getByText('Step 1 of 3')).toBeInTheDocument()
  })

  it('leaving the tab discards the issued key (shown once, never again)', async () => {
    post.mockResolvedValueOnce({ id: 3, name: 'k', key: 'sk-secret-2' })
    const user = await reachReview('k')
    await user.click(id('newtoken-open-confirm'))
    const dialog = confirmDialog()
    await user.type(dialog.getByTestId('newtoken-confirm-input'), 'k')
    await user.click(dialog.getByRole('button', { name: 'Create key' }))
    await screen.findByTestId('newtoken-key')

    await user.click(id('flows-tab-newChannel'))
    await user.click(id('flows-tab-newToken'))
    expect(screen.queryByText('sk-secret-2')).toBeNull()
    expect(screen.queryByTestId('newtoken-success')).toBeNull()
    expect(id('newtoken-name')).toHaveValue('')
  })

  it('links the success screen to the Keys page', async () => {
    post.mockResolvedValueOnce({ id: 3, name: 'k', key: 'sk-3' })
    const user = await reachReview('k')
    await user.click(id('newtoken-open-confirm'))
    const dialog = confirmDialog()
    await user.type(dialog.getByTestId('newtoken-confirm-input'), 'k')
    await user.click(dialog.getByRole('button', { name: 'Create key' }))
    await screen.findByTestId('newtoken-key')
    expect(screen.getByRole('link', { name: 'Go to Keys' })).toHaveAttribute(
      'href',
      '/keys'
    )
  })
})
