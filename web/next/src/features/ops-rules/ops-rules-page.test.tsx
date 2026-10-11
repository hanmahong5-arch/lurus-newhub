import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { OpsRulesPage } from './index'

const { get, post, put, del, tenantGet } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
  tenantGet: vi.fn(),
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: { get, post, put, delete: del },
    tenantApi: {
      get: tenantGet,
      post: vi.fn(),
      put: vi.fn(),
      delete: vi.fn(),
    },
  }
})

const RULES_URL = '/api/v2/admin/content-rules'
const TEMPLATES_URL = '/api/v2/admin/channel-templates'

function rule(over: Record<string, unknown> = {}) {
  return {
    id: 7,
    ordinal: 10,
    name: 'mask-emails',
    role_scope: 'any',
    kind: 'mask',
    pattern_type: 'builtin',
    builtin: 'email',
    pattern: '',
    replacement: '[email]',
    mode: 'observe',
    enabled: true,
    updated_at: 1700000000,
    ...over,
  }
}

function ruleList(rules: unknown[] = [rule()]) {
  return {
    rules,
    builtins: ['email', 'phone'],
    max_rules: 50,
    max_pattern_len: 512,
  }
}

function template(over: Record<string, unknown> = {}) {
  return {
    id: 3,
    name: 'tune-a',
    description: 'common tuning',
    param_override: '{"temperature":0.2}',
    header_override: '',
    version: 2,
    updated_at: 1700000000,
    ...over,
  }
}

function serve(handlers: Record<string, unknown>) {
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
      <OpsRulesPage />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  tenantGet.mockResolvedValue({
    channels: [
      { id: 11, name: 'chan-one', type: 1, status: 1 },
      { id: 12, name: 'chan-two', type: 1, status: 1 },
    ],
    total: 2,
    page: 1,
    page_size: 100,
  })
})

describe('content rules', () => {
  it('lists rules read from the platform admin endpoint', async () => {
    serve({ [RULES_URL]: ruleList() })
    renderPage()
    expect(await screen.findByText('mask-emails')).toBeInTheDocument()
    expect(screen.getByText('email')).toBeInTheDocument()
    expect(screen.getByText('Observe')).toBeInTheDocument()
    expect(screen.getByText('1 of 50 rules used')).toBeInTheDocument()
  })

  it('shows the empty state only for a genuinely empty list', async () => {
    serve({ [RULES_URL]: ruleList([]) })
    renderPage()
    expect(
      await screen.findByText('No platform content rules')
    ).toBeInTheDocument()
  })

  it('a failed read is an error, not an empty list', async () => {
    serve({ [RULES_URL]: new ApiError('rules down', { status: 500 }) })
    renderPage()
    expect(
      await screen.findByText('Could not load the content rules')
    ).toBeInTheDocument()
    expect(screen.getByText('rules down')).toBeInTheDocument()
    expect(screen.queryByText('No platform content rules')).toBeNull()
  })

  it('enforce needs a confirmation and then sends the whole rule', async () => {
    serve({ [RULES_URL]: ruleList() })
    put.mockResolvedValue(rule({ mode: 'enforce' }))
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('mask-emails')

    await user.click(
      screen.getByRole('switch', { name: 'Enforce mask-emails' })
    )
    expect(put).not.toHaveBeenCalled()
    const dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Enforce' }))

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put).toHaveBeenCalledWith(`${RULES_URL}/7`, {
      ordinal: 10,
      name: 'mask-emails',
      role_scope: 'any',
      kind: 'mask',
      pattern_type: 'builtin',
      builtin: 'email',
      pattern: '',
      replacement: '[email]',
      mode: 'enforce',
      enabled: true,
    })
  })

  it('switching back to observe needs no confirmation', async () => {
    serve({ [RULES_URL]: ruleList([rule({ mode: 'enforce' })]) })
    put.mockResolvedValue(rule())
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('mask-emails')
    await user.click(
      screen.getByRole('switch', { name: 'Enforce mask-emails' })
    )
    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put.mock.calls[0][1]).toMatchObject({ mode: 'observe' })
  })

  it('creates a regex rule without a builtin name', async () => {
    serve({ [RULES_URL]: ruleList([]) })
    post.mockResolvedValue(rule())
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('No platform content rules')

    await user.click(screen.getAllByRole('button', { name: 'Add rule' })[0])
    await user.type(screen.getByLabelText('Name'), 'secret-keys')
    await user.selectOptions(screen.getByLabelText('Pattern type'), 'regex')
    await user.click(screen.getByLabelText('Regular expression'))
    await user.paste('sk-[a-z]+')
    await user.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith(RULES_URL, {
      ordinal: 0,
      name: 'secret-keys',
      role_scope: 'any',
      kind: 'mask',
      pattern_type: 'regex',
      builtin: '',
      pattern: 'sk-[a-z]+',
      replacement: '',
      mode: 'observe',
      enabled: true,
    })
  })

  it('blocks a rule without a name and does not call the server', async () => {
    serve({ [RULES_URL]: ruleList([]) })
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('No platform content rules')
    await user.click(screen.getAllByRole('button', { name: 'Add rule' })[0])
    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Enter a name for the rule.'
    )
    expect(post).not.toHaveBeenCalled()
  })

  it('deletes a rule after confirmation', async () => {
    serve({ [RULES_URL]: ruleList() })
    del.mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('mask-emails')
    await user.click(screen.getByRole('button', { name: 'Delete' }))
    const dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(del).toHaveBeenCalledWith(`${RULES_URL}/7`))
  })
})

describe('override templates', () => {
  async function openTemplates(
    user: ReturnType<typeof userEvent.setup>,
    templates: unknown = [template()]
  ) {
    serve({ [RULES_URL]: ruleList(), [TEMPLATES_URL]: templates })
    renderPage()
    await user.click(screen.getByRole('tab', { name: 'Override templates' }))
  }

  it('lists templates with version and override kinds', async () => {
    const user = userEvent.setup()
    await openTemplates(user)
    expect(await screen.findByText('tune-a')).toBeInTheDocument()
    expect(screen.getByText('v2')).toBeInTheDocument()
    expect(screen.getByText('Parameters')).toBeInTheDocument()
    expect(screen.queryByText('Headers')).toBeNull()
  })

  it('a failed template read is an error, not an empty list', async () => {
    const user = userEvent.setup()
    await openTemplates(user, new ApiError('tpl down', { status: 500 }))
    expect(
      await screen.findByText('Could not load the templates')
    ).toBeInTheDocument()
    expect(screen.queryByText('No override templates yet')).toBeNull()
  })

  it('refuses invalid JSON and a non-string header value before any request', async () => {
    const user = userEvent.setup()
    await openTemplates(user, [])
    await user.click(
      (await screen.findAllByRole('button', { name: 'Add template' }))[0]
    )
    await user.type(screen.getByLabelText('Name'), 'bad')
    await user.click(screen.getByLabelText('Parameter overrides (JSON)'))
    await user.paste('{oops')
    await user.click(screen.getByLabelText('Header overrides (JSON)'))
    await user.paste('{"X-Test": 1}')

    expect(screen.getByText('Not valid JSON.')).toBeInTheDocument()
    expect(
      screen.getByText('Every header value must be a string.')
    ).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(post).not.toHaveBeenCalled()
  })

  it('saves valid JSON, trimmed, to the create endpoint', async () => {
    post.mockResolvedValue(template())
    const user = userEvent.setup()
    await openTemplates(user, [])
    await user.click(
      (await screen.findAllByRole('button', { name: 'Add template' }))[0]
    )
    await user.type(screen.getByLabelText('Name'), 'tune-b')
    await user.click(screen.getByLabelText('Header overrides (JSON)'))
    await user.paste('{"X-Test": "1"}')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith(TEMPLATES_URL, {
      name: 'tune-b',
      description: '',
      param_override: '',
      header_override: '{"X-Test": "1"}',
    })
  })

  it('applies to the ticked and typed channels and lists the per-channel result', async () => {
    post.mockResolvedValue({
      template_id: 3,
      template_version: 2,
      results: [
        { channel_id: 11, applied: true },
        { channel_id: 99, applied: false, error: 'channel not found' },
      ],
    })
    const user = userEvent.setup()
    await openTemplates(user)
    await screen.findByText('tune-a')
    await user.click(screen.getByRole('button', { name: 'Apply' }))
    await user.click(await screen.findByLabelText('#11 chan-one'))
    await user.type(screen.getByLabelText('More channel ids'), '99, 11')
    const dialog = screen.getByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Apply' }))

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    expect(post).toHaveBeenCalledWith(`${TEMPLATES_URL}/3/apply`, {
      channel_ids: [11, 99],
    })
    const records = await screen.findByTestId('apply-records')
    expect(within(records).getByText('Applied')).toBeInTheDocument()
    expect(within(records).getByText('Failed')).toBeInTheDocument()
    expect(within(records).getByText('channel not found')).toBeInTheDocument()
  })

  it('shows the server-side application history of a template', async () => {
    const user = userEvent.setup()
    serve({
      [RULES_URL]: ruleList(),
      [TEMPLATES_URL]: [template()],
      [`${TEMPLATES_URL}/3/applications`]: {
        applications: [
          { id: 9, channel_id: 21, template_id: 3, template_version: 2, applied_by: 5, applied_at: 1700000100 },
          { id: 8, channel_id: 22, template_id: 3, template_version: 1, applied_by: 5, applied_at: 1700000000 },
        ],
        total: 2,
        page: 1,
        page_size: 50,
      },
    })
    renderPage()
    await user.click(screen.getByRole('tab', { name: 'Override templates' }))
    await screen.findByText('tune-a')
    await user.click(screen.getByRole('button', { name: 'History' }))
    const table = await screen.findByTestId('applications-table')
    expect(within(table).getByText('#21')).toBeInTheDocument()
    expect(within(table).getByText('#22')).toBeInTheDocument()
    expect(within(table).getByText('v2')).toBeInTheDocument()
    expect(within(table).getByText('v1')).toBeInTheDocument()
    expect(get).toHaveBeenCalledWith(`${TEMPLATES_URL}/3/applications`, {
      params: { page: 1, page_size: 50 },
    })
  })

  it('a failed history read is an error, not "never applied"', async () => {
    const user = userEvent.setup()
    serve({
      [RULES_URL]: ruleList(),
      [TEMPLATES_URL]: [template()],
      [`${TEMPLATES_URL}/3/applications`]: new ApiError('log down', { status: 500 }),
    })
    renderPage()
    await user.click(screen.getByRole('tab', { name: 'Override templates' }))
    await screen.findByText('tune-a')
    await user.click(screen.getByRole('button', { name: 'History' }))
    expect(await screen.findByText('Could not load the application history')).toBeInTheDocument()
    expect(screen.queryByTestId('applications-empty')).toBeNull()
  })

  it('an empty history says so', async () => {
    const user = userEvent.setup()
    serve({
      [RULES_URL]: ruleList(),
      [TEMPLATES_URL]: [template()],
      [`${TEMPLATES_URL}/3/applications`]: { applications: [], total: 0, page: 1, page_size: 50 },
    })
    renderPage()
    await user.click(screen.getByRole('tab', { name: 'Override templates' }))
    await screen.findByText('tune-a')
    await user.click(screen.getByRole('button', { name: 'History' }))
    expect(await screen.findByTestId('applications-empty')).toBeInTheDocument()
  })

  it('apply with nothing chosen shows an error and sends nothing', async () => {
    const user = userEvent.setup()
    await openTemplates(user)
    await screen.findByText('tune-a')
    await user.click(screen.getByRole('button', { name: 'Apply' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Apply' }))
    expect(within(dialog).getByRole('alert')).toHaveTextContent(
      'Choose at least one channel.'
    )
    expect(post).not.toHaveBeenCalled()
  })

  it('a channel list failure is shown and ids can still be typed', async () => {
    tenantGet.mockRejectedValue(new ApiError('no list', { status: 500 }))
    post.mockResolvedValue({
      template_id: 3,
      template_version: 2,
      results: [],
    })
    const user = userEvent.setup()
    await openTemplates(user)
    await screen.findByText('tune-a')
    await user.click(screen.getByRole('button', { name: 'Apply' }))
    expect(
      await screen.findByText('Could not load the channel list: no list')
    ).toBeInTheDocument()
    await user.type(screen.getByLabelText('More channel ids'), '5')
    const dialog = screen.getByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Apply' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(`${TEMPLATES_URL}/3/apply`, {
        channel_ids: [5],
      })
    )
  })
})
