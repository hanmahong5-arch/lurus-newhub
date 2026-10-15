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
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api-error'

import { OrgPricingPage } from './index'
import { draftKey } from './lib/draft'

const { get, post, toastError, toastSuccess } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  toastError: vi.fn(),
  toastSuccess: vi.fn(),
}))

vi.mock('sonner', () => ({
  toast: { error: toastError, success: toastSuccess },
}))
vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return { ...actual, tenantApi: { get, post, put: vi.fn(), delete: vi.fn() } }
})

const USER_ID = 7
const ROOT = { id: USER_ID, role: 100, quota: 0, used_quota: 0, remaining_quota: 0 }
const TENANT_ADMIN = { ...ROOT, role: 1, tenant_role: 'admin' }

const sheet = (pricing: unknown[], version = 5, extra: object = {}) => ({
  pricing,
  vendors: [...new Set(pricing.map((p) => (p as { vendor?: string }).vendor).filter(Boolean))],
  group_ratio: { default: 1, vip: 0.85 },
  version,
  ...extra,
})

const MODEL_A = {
  model_name: 'model-a',
  vendor: 'vendor-a',
  quota_type: 0,
  model_ratio: 2.5,
  completion_ratio: 1,
  model_price: 0,
  price_configured: true,
  enable_groups: ['default', 'vip'],
}
const MODEL_B = {
  model_name: 'model-b',
  vendor: 'vendor-b',
  quota_type: 1,
  model_ratio: 0,
  completion_ratio: 0,
  model_price: 3,
  price_configured: true,
  enable_groups: ['vip'],
}
const MODEL_C = {
  model_name: 'model-c',
  vendor: 'vendor-c',
  quota_type: 0,
  model_ratio: 1.25,
  completion_ratio: 1,
  model_price: 0,
  price_configured: true,
  enable_groups: ['default'],
}
const THREE = [MODEL_A, MODEL_B, MODEL_C]

/** Route `get` by path; `sheetFn` is re-evaluated on every pricing read. */
function serve(
  sheetFn: () => unknown | Promise<unknown>,
  user: object = ROOT
) {
  get.mockImplementation((path: string) => {
    if (path === '/user/me') return Promise.resolve(user)
    if (path === '/pricing') {
      try {
        return Promise.resolve(sheetFn())
      } catch (e) {
        return Promise.reject(e)
      }
    }
    return Promise.reject(new Error(`unexpected GET ${path}`))
  })
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <OrgPricingPage />
    </QueryClientProvider>
  )
}

const pricingReads = () =>
  get.mock.calls.filter(([path]) => path === '/pricing').length

async function ready(model = 'model-a') {
  await waitFor(() => {
    expect(screen.getByTestId('pricing-table').textContent).toContain(model)
  })
}

const edit = (testId: string, value: string) =>
  fireEvent.change(screen.getByTestId(testId), { target: { value } })

beforeEach(() => {
  vi.clearAllMocks()
  window.localStorage.clear()
  serve(() => sheet([]))
})

describe('loading', () => {
  it('reads the tenant pricing sheet and lists every model', async () => {
    serve(() => sheet(THREE))
    renderPage()
    await ready()
    expect(get).toHaveBeenCalledWith('/pricing')
    const table = screen.getByTestId('pricing-table')
    expect(table.textContent).toContain('model-b')
    expect(table.textContent).toContain('model-c')
    expect(screen.getByTestId('pricing-model-count').textContent).toBe('3 models')
    expect(screen.getByTestId('pricing-version').textContent).toContain('5')
  })

  it('shows the group ratios read-only', async () => {
    serve(() => sheet(THREE))
    renderPage()
    await ready()
    const groups = screen.getByTestId('pricing-group-ratios')
    expect(groups.textContent).toContain('default')
    expect(groups.textContent).toContain('x1.00')
    expect(groups.textContent).toContain('x0.85')
  })

  it('a failed read renders a retryable error, never an empty list', async () => {
    let fail = true
    serve(() => {
      if (fail) throw new ApiError('boom', { status: 500 })
      return sheet([])
    })
    renderPage()
    const error = await screen.findByTestId('pricing-load-error')
    expect(error.textContent).toContain('Failed to load pricing data')
    expect(error.textContent).toContain('boom')
    expect(screen.queryByText('No models')).toBeNull()
    expect(toastError).not.toHaveBeenCalled()
    // The header badge is a dash, never a count of zero.
    expect(screen.getByTestId('pricing-model-count').textContent).toBe('—')

    fail = false
    fireEvent.click(within(error).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('No models')).toBeInTheDocument()
    expect(screen.queryByTestId('pricing-load-error')).toBeNull()
  })

  it('a failed refresh keeps the table and says so', async () => {
    let fail = false
    serve(() => {
      if (fail) throw new ApiError('later boom', { status: 502 })
      return sheet(THREE)
    })
    renderPage()
    await ready()
    fail = true
    fireEvent.click(screen.getByTestId('pricing-refresh'))
    const alert = await screen.findByTestId('pricing-refresh-error')
    expect(alert.textContent).toContain('later boom')
    expect(screen.getByTestId('pricing-table').textContent).toContain('model-a')
    expect(screen.queryByTestId('pricing-load-error')).toBeNull()
  })

  it('a failed account read does not guess a role', async () => {
    get.mockImplementation((path: string) =>
      path === '/user/me'
        ? Promise.reject(new ApiError('no account', { status: 500 }))
        : Promise.resolve(sheet(THREE))
    )
    renderPage()
    expect(await screen.findByText('Failed to load your account')).toBeInTheDocument()
    expect(screen.queryByTestId('pricing-table')).toBeNull()
  })
})

describe('unpriced models', () => {
  it('an unpriced model is an empty field labelled Not priced, never 0', async () => {
    serve(() =>
      sheet([
        { ...MODEL_A, model_ratio: 0, price_configured: false },
        { ...MODEL_C, price_configured: true },
      ])
    )
    renderPage()
    await ready()
    const unpriced = screen.getByTestId('field-model_ratio-model-a') as HTMLInputElement
    expect(unpriced.value).toBe('')
    expect(unpriced.getAttribute('placeholder')).toBe('Not priced')
    expect(screen.getByTestId('unpriced-model-a')).toBeInTheDocument()
    const priced = screen.getByTestId('field-model_ratio-model-c') as HTMLInputElement
    expect(priced.value).toBe('1.25')
    expect(priced.getAttribute('placeholder')).toBeNull()
    expect(screen.queryByTestId('unpriced-model-c')).toBeNull()
  })

  it('an unpriced per-call model has an empty price field too', async () => {
    serve(() => sheet([{ ...MODEL_B, model_price: 0, price_configured: false }]))
    renderPage()
    await ready('model-b')
    const field = screen.getByTestId('field-model_price-model-b') as HTMLInputElement
    expect(field.value).toBe('')
    expect(field.getAttribute('placeholder')).toBe('Not priced')
  })

  it('the unpriced-only filter lists just those models', async () => {
    serve(() =>
      sheet([{ ...MODEL_A, model_ratio: 0, price_configured: false }, MODEL_C])
    )
    renderPage()
    await ready()
    fireEvent.click(screen.getByTestId('filter-unpriced'))
    const table = screen.getByTestId('pricing-table')
    expect(table.textContent).toContain('model-a')
    expect(table.textContent).not.toContain('model-c')
  })
})

describe('editing and saving', () => {
  it('Preview and Save are disabled until something changed', async () => {
    serve(() => sheet(THREE))
    renderPage()
    await ready()
    expect(screen.getByTestId('pricing-save')).toBeDisabled()
    expect(screen.getByTestId('pricing-preview')).toBeDisabled()
    edit('field-model_ratio-model-a', '3.0')
    await waitFor(() => expect(screen.getByTestId('pricing-save')).toBeEnabled())
    expect(screen.getByTestId('pricing-preview')).toBeEnabled()
  })

  it('saves the changed rows with the version it read, then re-reads', async () => {
    serve(() => sheet(THREE, 5))
    post.mockResolvedValue({ updated_count: 1, new_version: 6 })
    renderPage()
    await ready()
    edit('field-model_ratio-model-a', '3.0')
    fireEvent.click(screen.getByTestId('pricing-save'))

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/pricing',
        [{ model_name: 'model-a', model_ratio: 3 }],
        { headers: { 'If-Match-Pricing-Version': '5' } }
      )
    )
    await waitFor(() => expect(pricingReads()).toBe(2))
    expect(toastSuccess).toHaveBeenCalledWith('1 models updated')
    // The buffer is spent: nothing left to save.
    await waitFor(() => expect(screen.getByTestId('pricing-save')).toBeDisabled())
    expect(screen.queryByTestId('pricing-dirty-bar')).toBeNull()
  })

  it('sends only the fields that changed, as numbers', async () => {
    serve(() => sheet(THREE, 5))
    post.mockResolvedValue({ updated_count: 2 })
    renderPage()
    await ready()
    edit('field-completion_ratio-model-a', '4')
    edit('field-cache_ratio-model-a', '0.1')
    edit('field-model_price-model-b', '4.5')
    fireEvent.click(screen.getByTestId('pricing-save'))
    await waitFor(() => expect(post).toHaveBeenCalled())
    const [, body] = post.mock.calls[0] as [string, unknown[]]
    expect(body).toEqual([
      { model_name: 'model-a', completion_ratio: 4, cache_ratio: 0.1 },
      { model_name: 'model-b', model_price: 4.5 },
    ])
  })

  it('a version conflict reloads, says so, and keeps the edits for review', async () => {
    let version = 5
    serve(() => sheet(THREE, version))
    post.mockRejectedValue(
      new ApiError('pricing has changed since you last loaded it', {
        status: 409,
        code: 'PRICING_VERSION_CONFLICT',
      })
    )
    renderPage()
    await ready()
    edit('field-model_ratio-model-a', '3.0')
    version = 6
    fireEvent.click(screen.getByTestId('pricing-save'))

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith(
        'Pricing changed since you last loaded it — refreshing'
      )
    )
    await waitFor(() => expect(pricingReads()).toBe(2))
    expect(await screen.findByTestId('pricing-conflict')).toBeInTheDocument()
    // Not treated as success, and the typed value is still there.
    expect(toastSuccess).not.toHaveBeenCalled()
    expect((screen.getByTestId('field-model_ratio-model-a') as HTMLInputElement).value).toBe('3.0')
    await waitFor(() =>
      expect(screen.getByTestId('pricing-version').textContent).toContain('6')
    )

    // Saving again pins the NEW version.
    post.mockReset()
    post.mockResolvedValue({ updated_count: 1 })
    fireEvent.click(screen.getByTestId('pricing-save'))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/pricing', expect.anything(), {
        headers: { 'If-Match-Pricing-Version': '6' },
      })
    )
    await waitFor(() => expect(screen.queryByTestId('pricing-conflict')).toBeNull())
  })

  it('any other save failure shows the server message and keeps the edits', async () => {
    serve(() => sheet(THREE))
    post.mockRejectedValue(
      new ApiError('model_ratio must be > 0', { status: 400, code: 'INVALID_RATIO' })
    )
    renderPage()
    await ready()
    edit('field-model_ratio-model-a', '3')
    fireEvent.click(screen.getByTestId('pricing-save'))
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('model_ratio must be > 0')
    )
    expect(pricingReads()).toBe(1)
    expect(screen.queryByTestId('pricing-conflict')).toBeNull()
    expect(screen.getByTestId('pricing-save')).toBeEnabled()
  })

  it('a blank field is no change, never NaN or 0 on the wire', async () => {
    serve(() => sheet(THREE))
    renderPage()
    await ready()
    edit('field-model_ratio-model-a', '')
    expect(screen.getByTestId('pricing-save')).toBeDisabled()
    expect(post).not.toHaveBeenCalled()
  })

  it('a value that is not a positive number blocks Preview and Save', async () => {
    serve(() => sheet(THREE))
    renderPage()
    await ready()
    edit('field-model_ratio-model-a', '-2')
    expect(screen.getByTestId('pricing-save')).toBeDisabled()
    expect(screen.getByTestId('pricing-preview')).toBeDisabled()
    expect(screen.getByTestId('pricing-invalid').textContent).toContain('model-a')
    expect(
      screen.getByText('Enter a number greater than 0, or leave it blank.')
    ).toBeInTheDocument()
    expect(screen.getByTestId('field-model_ratio-model-a')).toHaveAttribute('aria-invalid', 'true')
    edit('field-model_ratio-model-a', '0.5')
    await waitFor(() => expect(screen.getByTestId('pricing-save')).toBeEnabled())
    expect(screen.queryByTestId('pricing-invalid')).toBeNull()
  })

  it('prefills cache ratio from the row, blank when the model has none', async () => {
    serve(() => sheet([{ ...MODEL_A, cache_ratio: 0.42 }, MODEL_B]))
    renderPage()
    await ready()
    expect((screen.getByTestId('field-cache_ratio-model-a') as HTMLInputElement).value).toBe('0.42')
    expect((screen.getByTestId('field-cache_ratio-model-b') as HTMLInputElement).value).toBe('')
  })

  it('hides the model-price column unless a per-call model is listed', async () => {
    serve(() => sheet([MODEL_A, MODEL_C]))
    const first = renderPage()
    await ready()
    expect(screen.queryByTestId('pricing-th-model-price')).toBeNull()
    expect(screen.queryByTestId('field-model_price-model-a')).toBeNull()
    first.unmount()

    serve(() => sheet(THREE))
    renderPage()
    await ready('model-b')
    expect(screen.getByTestId('pricing-th-model-price')).toBeInTheDocument()
    expect(screen.getByTestId('field-model_price-model-b')).toBeInTheDocument()
    // Ratio columns are not applicable to a per-call model.
    expect(screen.queryByTestId('field-model_ratio-model-b')).toBeNull()
  })

  it('discarding a row or all edits restores the server values', async () => {
    serve(() => sheet(THREE))
    renderPage()
    await ready()
    edit('field-model_ratio-model-a', '9')
    edit('field-model_ratio-model-c', '8')
    expect(screen.getByTestId('pricing-dirty-bar').textContent).toContain('2 models edited')

    fireEvent.click(screen.getByTestId('reset-model-a'))
    expect((screen.getByTestId('field-model_ratio-model-a') as HTMLInputElement).value).toBe('2.5')
    expect(screen.getByTestId('pricing-dirty-bar').textContent).toContain('1 models edited')

    fireEvent.click(screen.getByTestId('pricing-discard'))
    fireEvent.click(await screen.findByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(screen.queryByTestId('pricing-dirty-bar')).toBeNull())
    expect((screen.getByTestId('field-model_ratio-model-c') as HTMLInputElement).value).toBe('1.25')
  })

  it('never sends edits for a model the server no longer lists', async () => {
    window.localStorage.setItem(
      draftKey(USER_ID),
      JSON.stringify({
        v: 1,
        t: Date.now(),
        d: { 'model-gone': { model_ratio: '2' }, 'model-a': { model_ratio: '3' } },
      })
    )
    serve(() => sheet(THREE))
    post.mockResolvedValue({ updated_count: 1 })
    renderPage()
    await ready()
    fireEvent.click(screen.getByTestId('pricing-save'))
    await waitFor(() => expect(post).toHaveBeenCalled())
    expect(post.mock.calls[0][1]).toEqual([{ model_name: 'model-a', model_ratio: 3 }])
  })
})

describe('preview', () => {
  it('shows one diff row per changed field and does not save', async () => {
    serve(() => sheet(THREE))
    post.mockResolvedValue({
      version: 5,
      updated_count: 2,
      diffs: [
        { model_name: 'model-a', field: 'model_ratio', old: 2.5, new: 3, old_explicit: true },
        { model_name: 'model-b', field: 'model_price', old: 3, new: 4, old_explicit: false },
      ],
    })
    renderPage()
    await ready()
    edit('field-model_ratio-model-a', '3.0')
    edit('field-model_price-model-b', '4.0')
    fireEvent.click(screen.getByTestId('pricing-preview'))

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/pricing/preview', [
        { model_name: 'model-a', model_ratio: 3 },
        { model_name: 'model-b', model_price: 4 },
      ])
    )
    const diff = await screen.findByTestId('pricing-preview-diff')
    expect(diff.textContent).toContain('model-a')
    expect(diff.textContent).toContain('model-b')
    expect(diff.textContent).toContain('Model ratio')
    expect(diff.textContent).toContain('Model price')
    // The second old value came from a fallback, and says so.
    expect(diff.textContent).toContain('(default value)')
    // Preview never hits the write route.
    expect(post.mock.calls.every(([path]) => path === '/pricing/preview')).toBe(true)
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  it('renders a context-tiers diff as readable lines, not an object', async () => {
    serve(() => sheet(THREE))
    post.mockResolvedValue({
      diffs: [
        {
          model_name: 'model-a',
          field: 'context_tiers',
          old: null,
          new: [{ threshold_tokens: 3000, model_ratio: 2 }],
        },
      ],
    })
    renderPage()
    await ready()
    edit('field-model_ratio-model-a', '3')
    fireEvent.click(screen.getByTestId('pricing-preview'))
    const diff = await screen.findByTestId('pricing-preview-diff')
    expect(diff.textContent).toContain('No tiers')
    expect(diff.textContent).toContain('From 3000 tokens')
    expect(diff.textContent).toContain('Model ratio 2')
    expect(diff.textContent).not.toContain('[object Object]')
  })

  it('an empty diff says there is nothing to preview', async () => {
    serve(() => sheet(THREE))
    post.mockResolvedValue({ diffs: [] })
    renderPage()
    await ready()
    edit('field-model_ratio-model-a', '3')
    fireEvent.click(screen.getByTestId('pricing-preview'))
    expect(await screen.findByText('No changes to preview.')).toBeInTheDocument()
    expect(screen.getByTestId('pricing-preview-save')).toBeDisabled()
  })

  it('a failed preview toasts the server message and opens nothing', async () => {
    serve(() => sheet(THREE))
    post.mockRejectedValue(new ApiError('Root role required', { status: 403 }))
    renderPage()
    await ready()
    edit('field-model_ratio-model-a', '3')
    fireEvent.click(screen.getByTestId('pricing-preview'))
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('Root role required'))
    expect(screen.queryByTestId('pricing-preview-diff')).toBeNull()
  })

  it('saves straight from the preview with the same version header', async () => {
    serve(() => sheet(THREE, 9))
    post.mockImplementation((path: string) =>
      Promise.resolve(
        path === '/pricing/preview'
          ? { diffs: [{ model_name: 'model-a', field: 'model_ratio', old: 2.5, new: 3 }] }
          : { updated_count: 1 }
      )
    )
    renderPage()
    await ready()
    edit('field-model_ratio-model-a', '3')
    fireEvent.click(screen.getByTestId('pricing-preview'))
    fireEvent.click(await screen.findByTestId('pricing-preview-save'))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/pricing', [{ model_name: 'model-a', model_ratio: 3 }], {
        headers: { 'If-Match-Pricing-Version': '9' },
      })
    )
    await waitFor(() => expect(screen.queryByTestId('pricing-preview-diff')).toBeNull())
  })
})

describe('context tiers', () => {
  const withTier = () =>
    sheet([{ ...MODEL_A, context_tiers: [{ threshold_tokens: 0, model_ratio: 1 }] }, MODEL_B], 5)

  it('has no tier control for a per-call model', async () => {
    serve(withTier)
    renderPage()
    await ready()
    expect(screen.getByTestId('context-tiers-toggle-model-a')).toBeInTheDocument()
    expect(screen.getByTestId('context-tiers-toggle-model-a').textContent).toContain('1 tier')
    expect(screen.queryByTestId('context-tiers-toggle-model-b')).toBeNull()
  })

  it('adds a tier, edits it, and saves the whole list', async () => {
    serve(withTier)
    post.mockResolvedValue({ updated_count: 1 })
    renderPage()
    await ready()
    fireEvent.click(screen.getByTestId('context-tiers-toggle-model-a'))
    expect(screen.getByTestId('context-tiers-editor-model-a')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('tier-add-model-a'))
    edit('tier-threshold-model-a-1', '3000')
    edit('tier-model_ratio-model-a-1', '2')
    fireEvent.click(screen.getByTestId('pricing-save'))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/pricing',
        [
          {
            model_name: 'model-a',
            context_tiers: [
              { threshold_tokens: 0, model_ratio: 1 },
              { threshold_tokens: 3000, model_ratio: 2 },
            ],
          },
        ],
        { headers: { 'If-Match-Pricing-Version': '5' } }
      )
    )
  })

  it('omits blank tier ratios instead of sending 0', async () => {
    serve(() => sheet([MODEL_A]))
    post.mockResolvedValue({ updated_count: 1 })
    renderPage()
    await ready()
    fireEvent.click(screen.getByTestId('context-tiers-toggle-model-a'))
    fireEvent.click(screen.getByTestId('tier-add-model-a'))
    edit('tier-threshold-model-a-0', '1000')
    edit('tier-completion_ratio-model-a-0', '3')
    fireEvent.click(screen.getByTestId('pricing-save'))
    await waitFor(() => expect(post).toHaveBeenCalled())
    expect(post.mock.calls[0][1]).toEqual([
      {
        model_name: 'model-a',
        context_tiers: [{ threshold_tokens: 1000, completion_ratio: 3 }],
      },
    ])
  })

  it('removing the last tier sends an explicit empty list', async () => {
    serve(withTier)
    post.mockResolvedValue({ updated_count: 1 })
    renderPage()
    await ready()
    fireEvent.click(screen.getByTestId('context-tiers-toggle-model-a'))
    fireEvent.click(screen.getByTestId('tier-remove-model-a-0'))
    fireEvent.click(screen.getByTestId('pricing-save'))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/pricing',
        [{ model_name: 'model-a', context_tiers: [] }],
        { headers: { 'If-Match-Pricing-Version': '5' } }
      )
    )
  })

  it('putting the same tiers back is not an edit', async () => {
    serve(withTier)
    renderPage()
    await ready()
    fireEvent.click(screen.getByTestId('context-tiers-toggle-model-a'))
    fireEvent.click(screen.getByTestId('tier-remove-model-a-0'))
    expect(screen.getByTestId('pricing-save')).toBeEnabled()
    fireEvent.click(screen.getByTestId('tier-add-model-a'))
    edit('tier-model_ratio-model-a-0', '1')
    // [threshold blank -> 0] differs from the server's "0", so still an edit;
    // typing the server's own value makes it identical again.
    edit('tier-threshold-model-a-0', '0')
    await waitFor(() => expect(screen.getByTestId('pricing-save')).toBeDisabled())
  })

  it('refuses thresholds that are not strictly ascending', async () => {
    serve(withTier)
    renderPage()
    await ready()
    fireEvent.click(screen.getByTestId('context-tiers-toggle-model-a'))
    fireEvent.click(screen.getByTestId('tier-add-model-a'))
    edit('tier-threshold-model-a-1', '0')
    expect(screen.getByText('Thresholds must be strictly ascending.')).toBeInTheDocument()
    expect(screen.getByTestId('pricing-save')).toBeDisabled()
    edit('tier-threshold-model-a-1', '500')
    await waitFor(() => expect(screen.getByTestId('pricing-save')).toBeEnabled())
  })

  it('stops at the server cap of eight tiers', async () => {
    serve(() => sheet([MODEL_A]))
    renderPage()
    await ready()
    fireEvent.click(screen.getByTestId('context-tiers-toggle-model-a'))
    for (let i = 0; i < 8; i += 1) fireEvent.click(screen.getByTestId('tier-add-model-a'))
    expect(screen.getByTestId('tier-add-model-a')).toBeDisabled()
  })
})

describe('search-unit prices', () => {
  it('tells not-configured, free and priced apart', async () => {
    serve(() =>
      sheet([
        { ...MODEL_A, model_name: 'rerank-a', search_unit_price: 0.002 },
        { ...MODEL_A, model_name: 'rerank-free', search_unit_price: 0 },
        { ...MODEL_C },
      ])
    )
    renderPage()
    await ready('rerank-a')
    expect(screen.getByTestId('search-unit-price').textContent).toBe('0.002')
    expect(screen.getByTestId('search-unit-free').textContent).toBe('Free')
    expect(screen.getByTestId('search-unit-unset').textContent).toBe('Not configured')
  })

  it('has no search-unit column when no model carries the field', async () => {
    serve(() => sheet(THREE))
    renderPage()
    await ready()
    expect(screen.queryByText('Search unit price')).toBeNull()
  })
})

describe('filters', () => {
  it('searches by name and filters by vendor and billing type', async () => {
    serve(() => sheet(THREE))
    renderPage()
    await ready()
    fireEvent.change(screen.getByLabelText('Search models'), { target: { value: 'MODEL-C' } })
    let table = screen.getByTestId('pricing-table')
    expect(table.textContent).toContain('model-c')
    expect(table.textContent).not.toContain('model-a')
    expect(screen.getByTestId('pricing-model-count').textContent).toBe('1 model')

    fireEvent.change(screen.getByLabelText('Search models'), { target: { value: '' } })
    fireEvent.change(screen.getByLabelText('Vendor'), { target: { value: 'vendor-b' } })
    table = screen.getByTestId('pricing-table')
    expect(table.textContent).toContain('model-b')
    expect(table.textContent).not.toContain('model-a')

    fireEvent.change(screen.getByLabelText('Vendor'), { target: { value: '' } })
    fireEvent.change(screen.getByLabelText('Billing type'), { target: { value: 'per-call' } })
    table = screen.getByTestId('pricing-table')
    expect(table.textContent).toContain('model-b')
    expect(table.textContent).not.toContain('model-c')
  })

  it('says when the filters leave nothing, and keeps edits of hidden rows', async () => {
    serve(() => sheet(THREE))
    renderPage()
    await ready()
    edit('field-model_ratio-model-a', '9')
    fireEvent.change(screen.getByLabelText('Search models'), { target: { value: 'zzz' } })
    expect(screen.getByText('No models match the filters')).toBeInTheDocument()
    expect(screen.getByTestId('pricing-save')).toBeEnabled()
  })

  it('pages a long list', async () => {
    const many = Array.from({ length: 120 }, (_, i) => ({
      ...MODEL_C,
      model_name: `bulk-${String(i).padStart(3, '0')}`,
    }))
    serve(() => sheet(many))
    renderPage()
    await ready('bulk-000')
    expect(screen.queryByText('bulk-050')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))
    expect(screen.getByTestId('pricing-table').textContent).toContain('bulk-050')
    expect(screen.getByTestId('pricing-table').textContent).not.toContain('bulk-000')
  })
})

describe('draft persistence', () => {
  it('keeps unsaved edits across a reload and drops them once saved', async () => {
    serve(() => sheet(THREE))
    post.mockResolvedValue({ updated_count: 1 })
    const first = renderPage()
    await ready()
    edit('field-model_ratio-model-a', '7')
    await waitFor(() =>
      expect(window.localStorage.getItem(draftKey(USER_ID))).toContain('"model_ratio":"7"')
    )
    first.unmount()

    renderPage()
    await ready()
    expect((screen.getByTestId('field-model_ratio-model-a') as HTMLInputElement).value).toBe('7')
    expect(screen.getByTestId('pricing-save')).toBeEnabled()

    fireEvent.click(screen.getByTestId('pricing-save'))
    await waitFor(() => expect(window.localStorage.getItem(draftKey(USER_ID))).toBeNull())
  })

  it('another account on the same browser does not see the draft', async () => {
    window.localStorage.setItem(
      draftKey(999),
      JSON.stringify({ v: 1, t: Date.now(), d: { 'model-a': { model_ratio: '7' } } })
    )
    serve(() => sheet(THREE))
    renderPage()
    await ready()
    expect((screen.getByTestId('field-model_ratio-model-a') as HTMLInputElement).value).toBe('2.5')
  })
})

describe('read-only viewers', () => {
  it('a tenant admin who is not root sees the table but cannot edit', async () => {
    serve(
      () =>
        sheet([
          { ...MODEL_A, model_ratio: 0, price_configured: false },
          { ...MODEL_C, context_tiers: [{ threshold_tokens: 100, model_ratio: 2 }] },
          MODEL_B,
        ]),
      TENANT_ADMIN
    )
    renderPage()
    await ready()
    expect(screen.getByTestId('pricing-read-only')).toBeInTheDocument()
    expect(screen.queryByTestId('pricing-save')).toBeNull()
    expect(screen.queryByTestId('pricing-preview')).toBeNull()
    expect(screen.queryByTestId('field-model_ratio-model-c')).toBeNull()
    expect(screen.queryByTestId('field-model_price-model-b')).toBeNull()
    const row = screen.getByTestId('pricing-row-model-c')
    expect(row.textContent).toContain('1.25')
    // An unpriced model still says so rather than showing 0.
    const unpriced = screen.getByTestId('pricing-row-model-a')
    expect(unpriced.textContent).toContain('Not priced')
    expect(unpriced.textContent).not.toMatch(/\b0\b/)

    // Tiers can be looked at, not changed.
    fireEvent.click(screen.getByTestId('context-tiers-toggle-model-c'))
    expect(screen.getByTestId('tier-threshold-model-c-0')).toBeDisabled()
    expect(screen.queryByTestId('tier-add-model-c')).toBeNull()
    expect(screen.queryByTestId('tier-remove-model-c-0')).toBeNull()
    expect(post).not.toHaveBeenCalled()
  })

  it('ignores a draft left by an earlier root session', async () => {
    window.localStorage.setItem(
      draftKey(USER_ID),
      JSON.stringify({ v: 1, t: Date.now(), d: { 'model-a': { model_ratio: '7' } } })
    )
    serve(() => sheet(THREE), TENANT_ADMIN)
    renderPage()
    await ready()
    expect(screen.getByTestId('pricing-row-model-a').textContent).toContain('2.5')
    expect(screen.queryByTestId('pricing-dirty-bar')).toBeNull()
  })
})
