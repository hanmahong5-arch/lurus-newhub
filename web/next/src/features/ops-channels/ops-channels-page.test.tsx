import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { OpsChannelsPage } from './index'

const { get, post, put, statusGet } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  statusGet: vi.fn(),
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    tenantApi: { get, post, put, delete: vi.fn() },
    api: { get: statusGet, post: vi.fn(), put: vi.fn(), delete: vi.fn() },
  }
})

const STATUS = { quota_per_unit: 500000, quota_display_type: 'USD' }
const NOW = Math.floor(Date.now() / 1000)

const listItem = (id: number, over: Record<string, unknown> = {}) => ({
  id,
  name: `pool-${id}`,
  type: 1,
  key: 'sk-****',
  status: 1,
  group: 'default',
  models: 'model-a',
  ...over,
})

const detailBody = (over: Record<string, unknown> = {}) => ({
  id: 1,
  setting: JSON.stringify({ plan_kind: 'zhipu_coding', proxy: 'http://keep' }),
  other_info: '',
  channel_info: { is_multi_key: true, multi_key_size: 2 },
  ...over,
})

const keyBody = (index: number, over: Record<string, unknown> = {}) => ({
  index,
  fingerprint: `fp${index}`,
  routable: true,
  reasons: [],
  window: null,
  last_success_at: null,
  ...over,
})

const healthBody = (over: Record<string, unknown> = {}) => ({
  channel_id: 1,
  routable: true,
  reasons: [],
  window: null,
  last_success_at: null,
  keys: [keyBody(0), keyBody(1)],
  ...over,
})

type Handler = unknown | Error | ((params?: unknown) => unknown)
let handlers: Record<string, Handler> = {}

function respond(path: string, params?: unknown) {
  const h = handlers[path]
  if (h instanceof Error) return Promise.reject(h)
  if (typeof h === 'function') return Promise.resolve(h(params))
  if (h === undefined) return Promise.reject(new ApiError(`unrouted ${path}`, { status: 404 }))
  return Promise.resolve(h)
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <OpsChannelsPage />
    </QueryClientProvider>
  )
}

function baseHandlers(extra: Record<string, Handler> = {}) {
  handlers = {
    '/user/me': { id: 1, username: 'ops', role: 10, quota: 0, used_quota: 0, remaining_quota: 0 },
    '/channels': { channels: [listItem(1)], total: 1, page: 1, page_size: 20 },
    '/channels/1': detailBody(),
    '/channels/1/health': healthBody(),
    '/channels/1/usage': {
      window: '24h',
      keys: [
        { key_idx: 0, requests: 8, errors: 1, cost_cny4: 30000 },
        { key_idx: 1, requests: 0 },
      ],
      total: { requests: 8, errors: 1, prompt_tokens: 100, completion_tokens: 50, quota: 500000, cost_cny4: 30000 },
      plan_monthly_fee_cny4: 3_000_000,
      utilization: 1.2,
      prorated_fee_cny4: 25000,
    },
    ...extra,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  statusGet.mockResolvedValue(STATUS)
  get.mockImplementation((path: string, cfg?: { params?: unknown }) =>
    respond(path, cfg?.params)
  )
  post.mockImplementation((path: string, body?: unknown) => respond(`POST ${path}`, body))
  put.mockImplementation((path: string, body?: unknown) => respond(`PUT ${path}`, body))
  baseHandlers()
})

async function openDrawer() {
  renderPage()
  const btn = await screen.findByRole('button', { name: 'Details of pool-1' })
  await userEvent.click(btn)
}

describe('channel list', () => {
  it('shows status, plan, key count, expiry and routability per channel', async () => {
    baseHandlers({
      '/channels/1': detailBody({
        setting: JSON.stringify({ plan_kind: 'minimax', expires_at: NOW - 86400 }),
      }),
      '/channels/1/health': healthBody({ routable: false, reasons: ['balance_low'] }),
    })
    renderPage()
    expect(await screen.findByText('pool-1')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('channel-1-plan')).toHaveTextContent('MiniMax plan'))
    expect(screen.getByTestId('channel-1-keys')).toHaveTextContent('2')
    expect(screen.getByTestId('channel-1-expires')).toHaveTextContent('(expired)')
    expect(await screen.findByText('Not routable')).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith('/channels', { params: { page: 1, page_size: 20 } })
  })

  it('shows an error, not an empty state, when the list fails', async () => {
    baseHandlers({ '/channels': new ApiError('boom', { status: 500 }) })
    renderPage()
    expect(await screen.findByText('Could not load channels')).toBeInTheDocument()
    expect(screen.getByText('boom')).toBeInTheDocument()
    expect(screen.queryByText('No channels yet')).not.toBeInTheDocument()
  })

  it('shows the empty state for a genuinely empty list', async () => {
    baseHandlers({ '/channels': { channels: [], total: 0, page: 1, page_size: 20 } })
    renderPage()
    expect(await screen.findByText('No channels yet')).toBeInTheDocument()
  })

  it('marks one row unknown when its health call fails instead of claiming it routable', async () => {
    baseHandlers({ '/channels/1/health': new ApiError('upstream down', { status: 500 }) })
    renderPage()
    expect(await screen.findByText('pool-1')).toBeInTheDocument()
    await waitFor(() => expect(screen.getAllByText('Unknown').length).toBeGreaterThan(0))
    expect(screen.queryByText('Routable')).not.toBeInTheDocument()
  })

  it('hides the import button from non-staff users', async () => {
    baseHandlers({ '/user/me': { id: 2, username: 'x', role: 1, quota: 0, used_quota: 0, remaining_quota: 0 } })
    renderPage()
    expect(await screen.findByText('pool-1')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Bulk import/ })).not.toBeInTheDocument()
  })
})

describe('channel drawer', () => {
  it('explains why a channel is not routable and counts the cooldown down', async () => {
    baseHandlers({
      '/channels/1/health': healthBody({
        routable: false,
        reasons: ['cooling_429', 'plan_window_exhausted'],
        cooldown_until: NOW + 125,
      }),
    })
    await openDrawer()
    const tab = await screen.findByTestId('health-tab')
    expect(within(tab).getByText('Cooling down after rate limit (429)')).toBeInTheDocument()
    expect(within(tab).getByText('Plan window used up')).toBeInTheDocument()
    expect(within(tab).getByTestId('cooldown-countdown')).toHaveTextContent(/^2m \d\ds$/)
  })

  it('draws the plan window bar from the last plan-quota probe', async () => {
    baseHandlers({
      '/channels/1': detailBody({
        other_info: JSON.stringify({
          plan_quota: { windows: [{ name: '5h', used_pct: 80, reset_at: NOW + 3600 }] },
        }),
      }),
    })
    await openDrawer()
    const bars = await screen.findByTestId('window-bars')
    expect(within(bars).getByText('5-hour window')).toBeInTheDocument()
    expect(within(bars).getByRole('progressbar')).toHaveAttribute('aria-valuenow', '80')
  })

  it('shows an error when health cannot be loaded', async () => {
    baseHandlers({ '/channels/1/health': new ApiError('no health', { status: 500 }) })
    await openDrawer()
    expect(await screen.findByText('Could not load channel health')).toBeInTheDocument()
  })

  it('tests a blocked key and restores it with the returned proof', async () => {
    baseHandlers({
      '/channels/1/health': healthBody({
        routable: false,
        keys: [keyBody(0, { routable: false, reasons: ['auth_failed'] }), keyBody(1)],
      }),
      'POST /channels/1/keys/0/test': {
        ok: true,
        latency_ms: 120,
        key_index: 0,
        restore_proof: '17.deadbeef',
        restore_proof_expires_at: NOW + 900,
      },
      'POST /channels/1/keys/0/restore': { restored: true, key_index: 0 },
    })
    await openDrawer()
    await userEvent.click(await screen.findByRole('tab', { name: 'Keys' }))
    const key0 = await screen.findByTestId('key-0')
    expect(within(key0).getByText('Authentication failed')).toBeInTheDocument()
    expect(within(key0).queryByRole('button', { name: 'Restore with proof' })).not.toBeInTheDocument()

    await userEvent.click(within(key0).getByRole('button', { name: 'Test key' }))
    expect(await screen.findByTestId('key-0-probe')).toHaveTextContent('Test passed (120 ms)')
    await userEvent.click(within(key0).getByRole('button', { name: 'Restore with proof' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/channels/1/keys/0/restore', { proof: '17.deadbeef' })
    )
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Restore with proof' })).not.toBeInTheDocument()
    )
  })

  it('offers no restore when the key test fails', async () => {
    baseHandlers({
      'POST /channels/1/keys/1/test': { ok: false, error: 'invalid api key', key_index: 1 },
    })
    await openDrawer()
    await userEvent.click(await screen.findByRole('tab', { name: 'Keys' }))
    const key1 = await screen.findByTestId('key-1')
    await userEvent.click(within(key1).getByRole('button', { name: 'Test key' }))
    expect(await screen.findByTestId('key-1-probe')).toHaveTextContent('Test failed: invalid api key')
    expect(screen.queryByRole('button', { name: 'Restore with proof' })).not.toBeInTheDocument()
  })

  it('edits a key proxy and sends only the changed field', async () => {
    baseHandlers({
      'PUT /channels/1/keys/1/settings': {},
    })
    await openDrawer()
    await userEvent.click(await screen.findByRole('tab', { name: 'Keys' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Edit key #1' }))
    const input = await screen.findByLabelText('Proxy URL')
    expect(screen.getByText(/safety check on save/)).toBeInTheDocument()
    await userEvent.type(input, 'http://proxy.example.com:8080')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith('/channels/1/keys/1/settings', {
        proxy: 'http://proxy.example.com:8080',
      })
    )
  })

  it('surfaces the server message when a proxy is rejected', async () => {
    baseHandlers({
      'PUT /channels/1/keys/1/settings': new ApiError('key proxy rejected: private address', { status: 400 }),
    })
    await openDrawer()
    await userEvent.click(await screen.findByRole('tab', { name: 'Keys' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Edit key #1' }))
    await userEvent.type(await screen.findByLabelText('Proxy URL'), 'http://10.0.0.1')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(put).toHaveBeenCalled())
    // the dialog stays open so the operator can fix the address
    expect(screen.getByLabelText('Proxy URL')).toBeInTheDocument()
  })

  it('does not offer key actions to non-staff users', async () => {
    baseHandlers({ '/user/me': { id: 2, username: 'x', role: 1, quota: 0, used_quota: 0, remaining_quota: 0 } })
    await openDrawer()
    await userEvent.click(await screen.findByRole('tab', { name: 'Keys' }))
    await screen.findByTestId('key-0')
    expect(screen.queryByRole('button', { name: 'Test key' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Edit key #0' })).not.toBeInTheDocument()
  })

  it('hides weight and proxy editing on a single-key channel', async () => {
    baseHandlers({
      '/channels/1': detailBody({ channel_info: { is_multi_key: false } }),
      '/channels/1/health': healthBody({ keys: [keyBody(0)] }),
    })
    await openDrawer()
    await userEvent.click(await screen.findByRole('tab', { name: 'Keys' }))
    await screen.findByTestId('key-0')
    expect(screen.getByText(/single key/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Edit key #0' })).not.toBeInTheDocument()
  })

  it('shows usage with utilization over 100 percent flagged, and refetches per window', async () => {
    await openDrawer()
    await userEvent.click(await screen.findByRole('tab', { name: 'Usage' }))
    expect(await screen.findByTestId('usage-utilization')).toHaveTextContent('120.0%')
    expect(screen.getByText('Cost exceeds the plan fee for this window.')).toBeInTheDocument()
    expect(screen.getByTestId('usage-cost')).toHaveTextContent('¥3.00')
    expect(get).toHaveBeenCalledWith('/channels/1/usage', { params: { window: '24h' } })
    await userEvent.click(screen.getByRole('button', { name: 'Last 7 days' }))
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith('/channels/1/usage', { params: { window: '7d' } })
    )
  })

  it('shows a usage error instead of zeros', async () => {
    baseHandlers({ '/channels/1/usage': new ApiError('agg failed', { status: 500 }) })
    await openDrawer()
    await userEvent.click(await screen.findByRole('tab', { name: 'Usage' }))
    expect(await screen.findByText('Could not load channel usage')).toBeInTheDocument()
  })

  it('saves plan settings while keeping unrelated setting members', async () => {
    baseHandlers({ 'PUT /channels/1': { id: 1 } })
    await openDrawer()
    await userEvent.click(await screen.findByRole('tab', { name: 'Plan settings' }))
    const fee = await screen.findByLabelText('Monthly plan fee (CNY)')
    await userEvent.type(fee, '150')
    await userEvent.type(screen.getByLabelText('Park threshold (%)'), '85')
    await userEvent.selectOptions(screen.getByLabelText('Scheduled test mode'), 'none')
    await userEvent.click(screen.getByRole('button', { name: 'Save plan settings' }))
    await waitFor(() => expect(put).toHaveBeenCalled())
    const [path, body] = put.mock.calls[0] as [string, { setting: string }]
    expect(path).toBe('/channels/1')
    expect(JSON.parse(body.setting)).toEqual({
      plan_kind: 'zhipu_coding',
      proxy: 'http://keep',
      plan_threshold_pct: 85,
      plan_monthly_fee_cny4: 1_500_000,
      test_mode: 'none',
    })
  })

  it('refuses an invalid threshold without calling the server', async () => {
    await openDrawer()
    await userEvent.click(await screen.findByRole('tab', { name: 'Plan settings' }))
    await userEvent.type(await screen.findByLabelText('Park threshold (%)'), '150')
    await userEvent.click(screen.getByRole('button', { name: 'Save plan settings' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('between 1 and 100')
    expect(put).not.toHaveBeenCalled()
  })
})

describe('import wizard', () => {
  const dryOutcome = {
    dry_run: true,
    summary: { ready: 2, duplicate: 1, invalid: 1, probe_failed: 0, imported: 0 },
    results: [
      { index: 0, status: 'ready', fingerprint: 'aaaaaaaa11' },
      { index: 1, status: 'ready', fingerprint: 'bbbbbbbb22' },
      { index: 2, status: 'duplicate', error_code: 'duplicate_existing_key', existing_channel_id: 4, fingerprint: 'cc' },
      { index: 3, status: 'invalid', error_code: 'empty_key' },
    ],
  }

  it('previews per-row status, then imports with the probe flag and shows the result', async () => {
    baseHandlers({
      'POST /channels/import': (body: unknown) => {
        const b = body as { dry_run: boolean }
        if (b.dry_run) return dryOutcome
        return {
          dry_run: false,
          summary: { ready: 1, duplicate: 1, invalid: 1, probe_failed: 1, imported: 1 },
          results: [
            { index: 0, status: 'ready', imported: true, channel_id: 21, probe: { ok: true, latency_ms: 90 } },
            { index: 1, status: 'probe_failed', error_code: 'probe_failed', probe: { ok: false, error: 'HTTP 401' } },
            dryOutcome.results[2],
            dryOutcome.results[3],
          ],
        }
      },
    })
    renderPage()
    await userEvent.click(await screen.findByRole('button', { name: /Bulk import/ }))
    await userEvent.type(await screen.findByLabelText('Keys'), 'k1{enter}k2')
    await userEvent.selectOptions(screen.getByLabelText('Channel type'), '1')
    await userEvent.type(screen.getByLabelText('Models (comma-separated)'), 'model-a')
    await userEvent.click(screen.getByRole('button', { name: 'Preview' }))

    expect(await screen.findByTestId('import-summary')).toHaveTextContent('2 ready, 1 duplicate, 1 invalid')
    expect(within(screen.getByTestId('import-row-2')).getByText('Duplicate')).toBeInTheDocument()
    expect(within(screen.getByTestId('import-row-2')).getByText(/Already exists in this workspace/)).toBeInTheDocument()
    expect(within(screen.getByTestId('import-row-3')).getByText('Invalid')).toBeInTheDocument()
    expect(post).toHaveBeenCalledWith(
      '/channels/import',
      expect.objectContaining({ dry_run: true, probe: false, type: 1, models: 'model-a' })
    )
    // the slow-probe warning is visible next to the checkbox
    expect(screen.getByText(/one at a time with a random pause/)).toBeInTheDocument()

    await userEvent.click(screen.getByRole('checkbox', { name: 'Test each key before importing' }))
    await userEvent.click(screen.getByRole('button', { name: 'Import 2 keys' }))
    expect(await screen.findByTestId('import-result-summary')).toHaveTextContent('1 imported, 1 test failed, 2 skipped')
    expect(post).toHaveBeenLastCalledWith(
      '/channels/import',
      expect.objectContaining({ dry_run: false, probe: true })
    )
    expect(within(screen.getByTestId('import-row-0')).getByText('Imported')).toBeInTheDocument()
    expect(within(screen.getByTestId('import-row-1')).getByText(/HTTP 401/)).toBeInTheDocument()
  })

  it('blocks the preview for malformed JSON and a missing type before any request', async () => {
    renderPage()
    await userEvent.click(await screen.findByRole('button', { name: /Bulk import/ }))
    const keys = await screen.findByLabelText('Keys')
    await userEvent.type(keys, '[[not json')
    await userEvent.click(screen.getByRole('button', { name: 'Preview' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('not a valid JSON array')
    await userEvent.clear(keys)
    await userEvent.type(keys, 'k1')
    await userEvent.click(screen.getByRole('button', { name: 'Preview' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Pick a channel type')
    expect(post).not.toHaveBeenCalled()
  })

  it('disables the import button when nothing is ready', async () => {
    baseHandlers({
      'POST /channels/import': {
        dry_run: true,
        summary: { ready: 0, duplicate: 1, invalid: 0, probe_failed: 0, imported: 0 },
        results: [{ index: 0, status: 'duplicate', error_code: 'duplicate_in_batch' }],
      },
    })
    renderPage()
    await userEvent.click(await screen.findByRole('button', { name: /Bulk import/ }))
    await userEvent.type(await screen.findByLabelText('Keys'), 'k1')
    await userEvent.selectOptions(screen.getByLabelText('Channel type'), '1')
    await userEvent.click(screen.getByRole('button', { name: 'Preview' }))
    expect(await screen.findByRole('button', { name: 'Import 0 keys' })).toBeDisabled()
  })

  it('appends to an existing multi-key channel without sending type fields', async () => {
    baseHandlers({
      'POST /channels/import': { dry_run: true, summary: { ready: 1 }, results: [{ index: 0, status: 'ready' }] },
    })
    renderPage()
    await userEvent.click(await screen.findByRole('button', { name: /Bulk import/ }))
    await userEvent.type(await screen.findByLabelText('Keys'), 'k1')
    const dest = screen.getByLabelText('Destination')
    await waitFor(() => expect(within(dest).getByText('Append to pool-1')).toBeInTheDocument())
    await userEvent.selectOptions(dest, '1')
    await userEvent.click(screen.getByRole('button', { name: 'Preview' }))
    await waitFor(() => expect(post).toHaveBeenCalled())
    expect(post.mock.calls[0][1]).toEqual({ dry_run: true, probe: false, keys: 'k1', channel_id: 1 })
  })
})
