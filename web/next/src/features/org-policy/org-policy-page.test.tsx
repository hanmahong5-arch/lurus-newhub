import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { exportAuditCsv } from './api'
import { OrgPolicyPage } from './index'

const { get, post, put, del, httpGet } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
  httpGet: vi.fn(),
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    tenantApi: { get, post, put, delete: del },
    http: { get: httpGet },
  }
})

const ALLOWLIST = {
  platform_allowed: ['model-a', 'model-b'],
  platform_unrestricted: false,
  tenant_selected: [],
  tenant_configured: false,
  effective: ['model-a', 'model-b'],
}

function routes(map: Record<string, unknown>) {
  get.mockImplementation((path: string) => {
    const v = map[path]
    if (v instanceof Error) return Promise.reject(v)
    if (v === undefined) return Promise.resolve({ items: [] })
    return Promise.resolve(v)
  })
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <OrgPolicyPage />
    </QueryClientProvider>
  )
}

async function openTab(name: string) {
  await userEvent.click(screen.getByRole('tab', { name }))
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('model allow-list', () => {
  it('shows platform / selected / effective and offers only the platform grant', async () => {
    routes({ '/models/allowlist': ALLOWLIST })
    renderPage()
    expect(await screen.findByText('Granted by the platform')).toBeInTheDocument()
    expect(screen.getByText('Selected by your organization')).toBeInTheDocument()
    expect(
      screen.getByText('Not narrowed yet: the platform grant applies as is.')
    ).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: 'model-a' })).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: 'model-b' })).toBeInTheDocument()
    // The platform grant is the ceiling: nothing else is routable-listed.
    expect(get).not.toHaveBeenCalledWith('/models/routable')
  })

  it('narrows to the ticked models with selected_models', async () => {
    routes({ '/models/allowlist': ALLOWLIST })
    put.mockResolvedValue({ ...ALLOWLIST, tenant_configured: true })
    renderPage()
    await userEvent.click(await screen.findByRole('checkbox', { name: 'model-b' }))
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith('/models/allowlist', {
        selected_models: ['model-a'],
      })
    )
  })

  it('refuses a hand-typed model outside the platform grant without calling the server', async () => {
    routes({ '/models/allowlist': ALLOWLIST })
    renderPage()
    const input = await screen.findByLabelText('Add a model name or prefix*')
    await userEvent.type(input, 'model-z')
    await userEvent.click(screen.getByRole('button', { name: 'Add' }))
    expect(
      await screen.findByText(
        'The platform has not granted this model to your organization.'
      )
    ).toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
  })

  it('selecting nothing needs an explicit confirmation before deny-all', async () => {
    routes({
      '/models/allowlist': {
        ...ALLOWLIST,
        tenant_selected: ['model-a'],
        tenant_configured: true,
        effective: ['model-a'],
      },
    })
    put.mockResolvedValue({ ...ALLOWLIST, tenant_configured: true })
    renderPage()
    await userEvent.click(await screen.findByRole('checkbox', { name: 'model-a' }))
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(put).not.toHaveBeenCalled()
    await userEvent.click(await screen.findByRole('button', { name: 'Block all' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith('/models/allowlist', {
        selected_models: [],
      })
    )
  })

  it('an unrestricted platform lists the routable models instead', async () => {
    routes({
      '/models/allowlist': {
        platform_allowed: [],
        platform_unrestricted: true,
        tenant_selected: [],
        tenant_configured: false,
        effective: ['*'],
      },
      '/models/routable': { items: [{ id: 'model-c' }] },
    })
    renderPage()
    expect(
      await screen.findByRole('checkbox', { name: 'model-c' })
    ).toBeInTheDocument()
    expect(screen.getAllByText('All models').length).toBeGreaterThan(0)
  })

  it('a failed read is an error with the server message, not an empty list', async () => {
    routes({ '/models/allowlist': new ApiError('Tenant admin required', { status: 403 }) })
    renderPage()
    expect(await screen.findByText('Failed to load the model allow-list')).toBeInTheDocument()
    expect(screen.getByText('Tenant admin required')).toBeInTheDocument()
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
  })

  it('a rejected save surfaces and keeps the draft', async () => {
    routes({ '/models/allowlist': ALLOWLIST })
    put.mockRejectedValue(new ApiError('model not granted', { status: 403 }))
    renderPage()
    await userEvent.click(await screen.findByRole('checkbox', { name: 'model-b' }))
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(put).toHaveBeenCalled())
    expect(screen.getByRole('checkbox', { name: 'model-b' })).toHaveAttribute(
      'aria-checked',
      'false'
    )
  })
})

describe('content retention', () => {
  const RETENTION = {
    platform_default: 'metadata_only',
    tenant: '',
    effective: 'metadata_only',
  }

  it('disables tiers looser than the platform default and saves a stricter one', async () => {
    routes({ '/data-policy/retention': RETENTION })
    put.mockResolvedValue({ ...RETENTION, tenant: 'none', effective: 'none' })
    renderPage()
    await openTab('Content retention')
    const full = await screen.findByRole('radio', { name: 'Full' })
    expect(full).toBeDisabled()
    expect(screen.getByRole('radio', { name: 'Metadata only' })).toBeEnabled()
    await userEvent.click(screen.getByRole('radio', { name: 'No retention' }))
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith('/data-policy/retention', {
        content_retention: 'none',
      })
    )
  })

  it('each tier explains what it keeps', async () => {
    routes({ '/data-policy/retention': { ...RETENTION, platform_default: 'full', effective: 'full' } })
    renderPage()
    await openTab('Content retention')
    expect(
      await screen.findByText(/Prompts, replies, client IP and request fingerprint are not stored/)
    ).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: 'Full' })).toBeEnabled()
  })

  it('per-key retention PUTs to the key and reports the effective mode', async () => {
    routes({
      '/data-policy/retention': RETENTION,
      '/tokens': {
        items: [{ id: 9, name: 'prod', key: 'sk-ab****yz', owner_name: 'Bob' }],
        total: 1,
      },
    })
    put.mockResolvedValue({ token_id: 9, content_retention: 'none', effective: 'none' })
    renderPage()
    await openTab('Content retention')
    const keySelect = await screen.findByLabelText('Key')
    // another member's key is offered, labelled with its owner
    expect(within(keySelect).getByRole('option', { name: /prod.*Bob/ })).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith('/tokens', {
      params: { scope: 'tenant', p: 1, size: 100 },
    })
    await userEvent.selectOptions(keySelect, '9')
    const mode = screen.getByLabelText('Retention')
    // looser than the tenant's effective mode is not offered
    expect(within(mode).getByRole('option', { name: 'Full' })).toBeDisabled()
    await userEvent.selectOptions(mode, 'none')
    await userEvent.click(screen.getByRole('button', { name: 'Apply to this key' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith('/data-policy/tokens/9/retention', {
        content_retention: 'none',
      })
    )
    expect(await screen.findByText('Key #9 now keeps: No retention')).toBeInTheDocument()
  })

  it('a failed retention read is an error state', async () => {
    routes({ '/data-policy/retention': new ApiError('boom', { status: 500 }) })
    renderPage()
    await openTab('Content retention')
    expect(await screen.findByText('Failed to load the retention setting')).toBeInTheDocument()
    expect(screen.getByText('boom')).toBeInTheDocument()
  })
})

describe('content rules', () => {
  const RULES = {
    rules: [
      {
        id: 4,
        ordinal: 1,
        name: 'phones',
        role_scope: 'user',
        kind: 'mask',
        pattern_type: 'builtin',
        builtin: 'phone_cn',
        pattern: '',
        replacement: '',
        mode: 'observe',
        enabled: true,
      },
    ],
    builtins: ['phone_cn', 'id_card_cn', 'bank_card', 'email', 'secret_key'],
    max_rules: 20,
    max_pattern_len: 512,
  }

  it('lists rules with readable builtin names and the observe-first guide', async () => {
    routes({ '/data-policy/rules': RULES })
    renderPage()
    await openTab('Content rules')
    expect(await screen.findByText('phones')).toBeInTheDocument()
    expect(screen.getByText('Mainland China mobile number')).toBeInTheDocument()
    expect(screen.getByText('Observe first, then enforce')).toBeInTheDocument()
    expect(screen.getByText('1 rules, 1 still observing.', { exact: false })).toBeInTheDocument()
  })

  it('creates a builtin rule in observe mode using the server catalogue', async () => {
    routes({ '/data-policy/rules': { ...RULES, rules: [] } })
    post.mockResolvedValue({})
    renderPage()
    await openTab('Content rules')
    await userEvent.click(await screen.findByRole('button', { name: 'Add rule' }))
    const dialog = await screen.findByRole('dialog')
    // every builtin the server reports is offered
    const detect = within(dialog).getByLabelText('Detect')
    expect(within(detect).getAllByRole('option')).toHaveLength(5)
    await userEvent.type(within(dialog).getByLabelText('Name'), 'ids')
    await userEvent.selectOptions(detect, 'id_card_cn')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/data-policy/rules',
        expect.objectContaining({
          name: 'ids',
          pattern_type: 'builtin',
          builtin: 'id_card_cn',
          mode: 'observe',
          kind: 'mask',
          enabled: true,
        })
      )
    )
  })

  it('warns when switching to enforce and shows server validation inside the dialog', async () => {
    routes({ '/data-policy/rules': RULES })
    put.mockRejectedValue(new ApiError('invalid rule: bad pattern', { status: 400 }))
    renderPage()
    await openTab('Content rules')
    await userEvent.click(await screen.findByRole('button', { name: 'Edit' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.selectOptions(within(dialog).getByLabelText('Mode'), 'enforce')
    expect(
      within(dialog).getByText(/Enforce will rewrite matching content/)
    ).toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    expect(await within(dialog).findByText('invalid rule: bad pattern')).toBeInTheDocument()
    expect(put).toHaveBeenCalledWith(
      '/data-policy/rules/4',
      expect.objectContaining({ mode: 'enforce', builtin: 'phone_cn', pattern: '' })
    )
  })

  it('sends RE2-only syntax to the server and shows its rejection', async () => {
    routes({ '/data-policy/rules': { ...RULES, rules: [] } })
    post.mockRejectedValue(new ApiError('invalid rule: bad regex', { status: 400 }))
    renderPage()
    await openTab('Content rules')
    await userEvent.click(await screen.findByRole('button', { name: 'Add rule' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.type(within(dialog).getByLabelText('Name'), 'ci')
    await userEvent.selectOptions(within(dialog).getByLabelText('Match by'), 'regex')
    await userEvent.type(within(dialog).getByLabelText('Regular expression'), '(?i)secret')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
    expect(await within(dialog).findByText('invalid rule: bad regex')).toBeInTheDocument()
    expect(post).toHaveBeenCalledWith(
      '/data-policy/rules',
      expect.objectContaining({ pattern: '(?i)secret', builtin: '' })
    )
  })

  it('deletes after confirmation', async () => {
    routes({ '/data-policy/rules': RULES })
    del.mockResolvedValue(undefined)
    renderPage()
    await openTab('Content rules')
    await userEvent.click(await screen.findByRole('button', { name: 'Delete' }))
    expect(del).not.toHaveBeenCalled()
    const confirm = await screen.findByRole('alertdialog')
    await userEvent.click(within(confirm).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(del).toHaveBeenCalledWith('/data-policy/rules/4'))
  })

  it('disables Add at the rule limit and surfaces a failed read as an error', async () => {
    routes({ '/data-policy/rules': { ...RULES, max_rules: 1 } })
    const { unmount } = renderPage()
    await openTab('Content rules')
    expect(await screen.findByRole('button', { name: 'Add rule' })).toBeDisabled()
    unmount()
    routes({ '/data-policy/rules': new ApiError('down', { status: 502 }) })
    renderPage()
    await openTab('Content rules')
    expect(await screen.findByText('Failed to load content rules')).toBeInTheDocument()
  })
})

describe('audit log', () => {
  const EVENT = {
    id: 1,
    timestamp: 1790000000,
    actor_type: 'user',
    actor_id: 7,
    action: 'data_policy.retention_set',
    resource: 'data_policy',
    resource_id: 0,
    ip: '10.0.0.1',
    request_id: 'r1',
    details: '{"to":"none"}',
  }

  it('lists events, paginates and sends the filters the handler reads', async () => {
    get.mockImplementation((_p: string, cfg?: { params?: Record<string, unknown> }) =>
      Promise.resolve({
        items: [EVENT],
        total: 45,
        page: cfg?.params?.page ?? 1,
        page_size: 20,
      })
    )
    renderPage()
    await openTab('Audit log')
    expect(await screen.findByText('data_policy.retention_set')).toBeInTheDocument()
    expect(screen.getByText('45 events, page 1 of 3')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Next' }))
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith('/audit', {
        params: { page: 2, page_size: 20 },
      })
    )

    await userEvent.type(screen.getByLabelText('Action'), 'data_policy.rule_hit')
    await userEvent.type(screen.getByLabelText('Operator (user id)'), '7')
    fireEvent.change(screen.getByLabelText('From'), { target: { value: '2026-10-01' } })
    await userEvent.click(screen.getByRole('button', { name: 'Search logs' }))
    await waitFor(() => {
      const last = get.mock.calls.at(-1)
      expect(last?.[0]).toBe('/audit')
      expect(last?.[1].params).toMatchObject({
        action: 'data_policy.rule_hit',
        actor_id: 7,
        page: 1,
      })
      expect(last?.[1].params.start_time).toBe(
        Math.floor(new Date(2026, 9, 1).getTime() / 1000)
      )
    })
  })

  it('blocks a non-numeric operator filter locally', async () => {
    get.mockResolvedValue({ items: [EVENT], total: 1, page: 1, page_size: 20 })
    renderPage()
    await openTab('Audit log')
    await screen.findByText('data_policy.retention_set')
    const calls = get.mock.calls.length
    await userEvent.type(screen.getByLabelText('Operator (user id)'), 'abc')
    await userEvent.click(screen.getByRole('button', { name: 'Search logs' }))
    expect(
      await screen.findByText('The operator must be a positive whole number (user id).')
    ).toBeInTheDocument()
    expect(get.mock.calls.length).toBe(calls)
  })

  it('shows an empty state only for a real empty answer, an error otherwise', async () => {
    get.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 })
    const { unmount } = renderPage()
    await openTab('Audit log')
    expect(await screen.findByText('No audit events match')).toBeInTheDocument()
    unmount()
    get.mockRejectedValue(new ApiError('Tenant admin required', { status: 403 }))
    renderPage()
    await openTab('Audit log')
    expect(await screen.findByText('Failed to load the audit log')).toBeInTheDocument()
    expect(screen.queryByText('No audit events match')).not.toBeInTheDocument()
  })

  it('exports the applied filters as a CSV download', async () => {
    get.mockResolvedValue({ items: [EVENT], total: 1, page: 1, page_size: 20 })
    httpGet.mockResolvedValue({ data: 'id\n1\n', headers: {} })
    const create = vi.fn(() => 'blob:x')
    const revoke = vi.fn()
    Object.assign(URL, { createObjectURL: create, revokeObjectURL: revoke })
    renderPage()
    await openTab('Audit log')
    await screen.findByText('data_policy.retention_set')
    await userEvent.click(screen.getByRole('button', { name: 'Export CSV' }))
    await waitFor(() => expect(create).toHaveBeenCalled())
    expect(httpGet).toHaveBeenCalledWith(
      '/api/v2/~/audit/export.csv',
      expect.objectContaining({ responseType: 'text', params: {} })
    )
    expect(revoke).toHaveBeenCalled()
  })
})

describe('exportAuditCsv', () => {
  const none = { action: '', actorId: '', from: '', to: '' }

  it('follows X-Next-Cursor and stitches pages under one header', async () => {
    httpGet
      .mockResolvedValueOnce({ data: 'id\n1\n', headers: { 'x-next-cursor': '55' } })
      .mockResolvedValueOnce({ data: 'id\n2\n', headers: {} })
    const out = await exportAuditCsv(none)
    expect(out).toEqual({ csv: 'id\n1\n2\n', truncated: false })
    expect(httpGet.mock.calls[1][1].params).toEqual({ cursor: '55' })
  })

  it('reports truncation when the page cap is hit', async () => {
    httpGet.mockResolvedValue({ data: 'id\n1\n', headers: { 'x-next-cursor': '9' } })
    const out = await exportAuditCsv(none, 2)
    expect(out.truncated).toBe(true)
    expect(httpGet).toHaveBeenCalledTimes(2)
  })
})

describe('data archiving consent', () => {
  it('reads off by default and only writes after confirmation', async () => {
    routes({ '/data-policy/sedimentation': { consent: false } })
    put.mockResolvedValue({ consent: true })
    renderPage()
    await openTab('Data archiving')
    const sw = await screen.findByRole('switch', {
      name: 'Archive request and reply text',
    })
    expect(sw).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByText('Off')).toBeInTheDocument()

    await userEvent.click(sw)
    // Nothing is sent until the dialog is confirmed.
    expect(put).not.toHaveBeenCalled()
    await userEvent.click(
      await screen.findByRole('button', { name: 'Start archiving' })
    )
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith('/data-policy/sedimentation', {
        consent: true,
      })
    )
  })

  it('cancelling the confirmation leaves the setting untouched', async () => {
    routes({ '/data-policy/sedimentation': { consent: true } })
    renderPage()
    await openTab('Data archiving')
    const sw = await screen.findByRole('switch', {
      name: 'Archive request and reply text',
    })
    await userEvent.click(sw)
    await userEvent.click(await screen.findByRole('button', { name: 'Cancel' }))
    expect(put).not.toHaveBeenCalled()
  })

  it('withdrawing consent sends false', async () => {
    routes({ '/data-policy/sedimentation': { consent: true } })
    put.mockResolvedValue({ consent: false })
    renderPage()
    await openTab('Data archiving')
    await userEvent.click(
      await screen.findByRole('switch', { name: 'Archive request and reply text' })
    )
    await userEvent.click(
      await screen.findByRole('button', { name: 'Stop archiving' })
    )
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith('/data-policy/sedimentation', {
        consent: false,
      })
    )
  })

  it('a failed read shows an error, not an off switch', async () => {
    routes({ '/data-policy/sedimentation': new ApiError('down', { status: 500 }) })
    renderPage()
    await openTab('Data archiving')
    expect(
      await screen.findByText('Failed to load the data archiving setting')
    ).toBeInTheDocument()
    expect(screen.queryByRole('switch')).toBeNull()
  })
})
