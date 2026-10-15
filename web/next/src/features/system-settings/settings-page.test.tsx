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
import type { AxiosRequestConfig, AxiosResponse } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { http } from '@/lib/api'
import { ApiError } from '@/lib/api-error'

import { SystemSettingsPage } from './index'

const { navigate, toastError, toastSuccess } = vi.hoisted(() => ({
  navigate: vi.fn(),
  toastError: vi.fn(),
  toastSuccess: vi.fn(),
}))

vi.mock('@tanstack/react-router', async (orig) => {
  const actual = await orig<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => navigate,
    useBlocker: () => ({ status: 'idle' }),
  }
})

vi.mock('sonner', async (orig) => {
  const actual = await orig<typeof import('sonner')>()
  return {
    ...actual,
    toast: Object.assign(vi.fn(), actual.toast, {
      error: toastError,
      success: toastSuccess,
      info: vi.fn(),
      warning: vi.fn(),
    }),
  }
})

type Option = { key: string; value: string }

const BASE_OPTIONS: Option[] = [
  { key: 'SystemName', value: 'Gateway A' },
  { key: 'ServerAddress', value: 'https://gw.example.com' },
  { key: 'Logo', value: '' },
  { key: 'general_setting.docs_link', value: '' },
  { key: 'Footer', value: '' },
  { key: 'ModelRatio', value: '{"model-a":1.5,"model-b":0.25}' },
  { key: 'CompletionRatio', value: '{"model-a":2}' },
  { key: 'ModelPrice', value: '{"model-c":0.04}' },
  { key: 'ModelRequestRateLimitEnabled', value: 'true' },
  { key: 'ModelRequestRateLimitDurationMinutes', value: '1' },
  { key: 'ModelRequestRateLimitCount', value: '100' },
  { key: 'ModelRequestRateLimitSuccessCount', value: '50' },
  { key: 'ModelRequestRateLimitGroup', value: '{"default":[200,100]}' },
  { key: 'QuotaForNewUser', value: '500000' },
]

const STATUS = {
  quota_per_unit: 500000,
  quota_display_type: 'USD',
  usd_exchange_rate: 7.3,
}

type PutHandler = (body: { key: string; value: unknown }) => {
  status?: number
  body: unknown
}

function reply(status: number, body: unknown): AxiosResponse {
  return {
    data: body,
    status,
    statusText: '',
    headers: {},
    config: {} as AxiosResponse['config'],
  }
}

const SYNC_CHANNELS = [
  { id: 7, name: 'Source A', base_url: 'https://a.example.com', status: 1 },
  { id: -100, name: 'preset', base_url: 'https://p.example.com', status: 1 },
]

const SYNC_RESULT = {
  differences: {
    'model-a': {
      model_ratio: {
        current: 1.5,
        upstreams: { 'Source A(7)': 3 },
        confidence: { 'Source A(7)': true },
      },
    },
    'model-n': {
      model_ratio: {
        current: null,
        upstreams: { 'Source A(7)': 0.75 },
        confidence: { 'Source A(7)': false },
      },
    },
  },
  test_results: [{ name: 'Source A(7)', status: 'success' }],
}

function installApi(options: {
  list?: Option[]
  listStatus?: number
  onPut?: PutHandler
  onFetch?: (body: unknown) => unknown
}) {
  const puts: Array<{ key: string; value: unknown }> = []
  vi.spyOn(http, 'request').mockImplementation(
    async (config: AxiosRequestConfig) => {
      if (config.url === '/api/status') {
        return reply(200, { success: true, data: STATUS })
      }
      if (config.url === '/api/ratio_sync/channels') {
        return reply(200, { success: true, data: SYNC_CHANNELS })
      }
      if (config.url === '/api/ratio_sync/fetch') {
        const payload = options.onFetch?.(config.data) ?? SYNC_RESULT
        return reply(200, { success: true, data: payload })
      }
      if (config.url === '/api/option/' && config.method === 'GET') {
        if (options.listStatus && options.listStatus >= 400) {
          // What the response interceptor turns a 4xx into.
          throw new ApiError('root only', { status: options.listStatus })
        }
        return reply(200, { success: true, data: options.list ?? BASE_OPTIONS })
      }
      if (config.url === '/api/option/' && config.method === 'PUT') {
        const body = config.data as { key: string; value: unknown }
        puts.push(body)
        const out = options.onPut?.(body) ?? {
          status: 200,
          body: { success: true, message: '' },
        }
        const status = out.status ?? 200
        if (status >= 400) {
          const envelope = out.body as { message?: string }
          throw new ApiError(envelope.message ?? 'failed', { status })
        }
        return reply(status, out.body)
      }
      throw new Error(`unexpected request ${config.method} ${config.url}`)
    }
  )
  return puts
}

function renderPage(section?: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <SystemSettingsPage section={section} />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  navigate.mockReset()
  toastError.mockReset()
  toastSuccess.mockReset()
})

describe('section navigation', () => {
  it('lists the sections and navigates by slug', async () => {
    installApi({})
    renderPage('branding')
    const nav = await screen.findByRole('navigation', {
      name: 'Settings sections',
    })
    const current = within(nav).getByRole('link', { name: 'Branding' })
    expect(current).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'Model prices' })).toBeVisible()

    await userEvent.click(within(nav).getByRole('link', { name: 'Rate limits' }))
    expect(navigate).toHaveBeenCalledWith({
      to: '/admin/settings/$section',
      params: { section: 'limits' },
    })
  })

  it('falls back to the first section for an unknown slug', async () => {
    installApi({})
    renderPage('does-not-exist')
    expect(await screen.findByLabelText('System Name')).toHaveValue('Gateway A')
  })

  it('tells a non-root operator why the page is empty', async () => {
    installApi({ listStatus: 403 })
    renderPage('branding')
    expect(
      await screen.findByText('System settings are limited to the root operator')
    ).toBeVisible()
  })
})

describe('option engine', () => {
  it('shows the stored values and sends only the fields that changed', async () => {
    const puts = installApi({})
    renderPage('branding')
    const name = await screen.findByLabelText('System Name')
    expect(name).toHaveValue('Gateway A')
    expect(screen.getByLabelText('Server Address')).toHaveValue(
      'https://gw.example.com'
    )

    await userEvent.clear(name)
    await userEvent.type(name, 'Gateway B')
    await userEvent.type(screen.getByLabelText('Footer'), 'Footer text')
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() => expect(puts).toHaveLength(2))
    expect(puts).toEqual([
      { key: 'SystemName', value: 'Gateway B' },
      { key: 'Footer', value: 'Footer text' },
    ])
    await waitFor(() => expect(toastSuccess).toHaveBeenCalled())
  })

  it('refuses invalid input before sending anything', async () => {
    const puts = installApi({})
    renderPage('branding')
    const name = await screen.findByLabelText('System Name')
    await userEvent.clear(name)
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    expect(await screen.findByText('This field is required')).toBeVisible()
    expect(puts).toHaveLength(0)
  })

  it('surfaces a refusal sent as HTTP 200 success:false and keeps the edit', async () => {
    const puts = installApi({
      onPut: () => ({
        status: 200,
        body: { success: false, message: 'name is reserved' },
      }),
    })
    renderPage('branding')
    const name = await screen.findByLabelText('System Name')
    await userEvent.clear(name)
    await userEvent.type(name, 'Reserved')
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    expect(await screen.findByText('name is reserved')).toBeVisible()
    expect(puts).toHaveLength(1)
    expect(toastError).toHaveBeenCalledWith('System Name: name is reserved')
    expect(toastSuccess).not.toHaveBeenCalled()
    expect(name).toHaveValue('Reserved')
    // Still dirty: the save button can be pressed again.
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
  })

  it('stops at the first refused key and leaves the rest dirty', async () => {
    const puts = installApi({
      onPut: (body) =>
        body.key === 'SystemName'
          ? { status: 400, body: { success: false, message: 'rejected' } }
          : { status: 200, body: { success: true, message: '' } },
    })
    renderPage('branding')
    const name = await screen.findByLabelText('System Name')
    await userEvent.clear(name)
    await userEvent.type(name, 'X')
    await userEvent.type(screen.getByLabelText('Footer'), 'f')
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    expect(await screen.findByText('rejected')).toBeVisible()
    expect(puts.map((p) => p.key)).toEqual(['SystemName'])
  })

  it('does not save when a section tab is clicked', async () => {
    const puts = installApi({})
    renderPage('model-prices')
    await userEvent.click(await screen.findByRole('tab', { name: 'JSON' }))
    expect(puts).toHaveLength(0)
  })
})

describe('model prices', () => {
  it('lists priced models with their billing mode and filters by name', async () => {
    installApi({})
    renderPage('model-prices')
    // 1.5 ratio => 1.5 * 1M quota / 500000 per USD = $3.00 per 1M tokens.
    // Amounts need /api/status, so wait for them before touching the rows.
    expect(await screen.findByText('$3.0000 / 1M tokens')).toBeVisible()
    expect(screen.getByText('model-a')).toBeVisible()
    expect(screen.getByText('model-b')).toBeVisible()
    expect(screen.getByText('model-c')).toBeVisible()

    await userEvent.type(screen.getByPlaceholderText('Search models...'), 'model-b')
    await waitFor(() => expect(screen.queryByText('model-a')).toBeNull())
    expect(screen.getByText('model-b')).toBeVisible()
  })

  it('edits a model, marks it unsaved and saves only the changed map', async () => {
    const puts = installApi({})
    renderPage('model-prices')
    await screen.findByText('$3.0000 / 1M tokens')
    await userEvent.click(
      await screen.findByRole('button', { name: 'Edit model-a' })
    )
    const dialog = await screen.findByRole('dialog')
    const ratio = within(dialog).getByLabelText('Model ratio')
    await userEvent.clear(ratio)
    await userEvent.type(ratio, '2')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Update' }))

    expect(await screen.findByText('Unsaved')).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0].key).toBe('ModelRatio')
    expect(JSON.parse(puts[0].value as string)).toEqual({
      'model-a': 2,
      'model-b': 0.25,
    })
  })

  it('shows the server refusal when a price write is rejected', async () => {
    installApi({
      onPut: () => ({
        status: 400,
        body: { success: false, message: 'ModelRatio must be a JSON object of numbers' },
      }),
    })
    renderPage('model-prices')
    await screen.findByText('$3.0000 / 1M tokens')
    await userEvent.click(
      await screen.findByRole('button', { name: 'Delete model-b' })
    )
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    // The field is on another tab, so the refusal is shown above the form.
    expect(
      await screen.findByText(/ModelRatio must be a JSON object of numbers/)
    ).toBeVisible()
    expect(toastError).toHaveBeenCalledWith(
      'Model ratio: ModelRatio must be a JSON object of numbers'
    )
  })

  it('adds a per-request price and rejects a duplicate model name', async () => {
    const puts = installApi({})
    renderPage('model-prices')
    await userEvent.click(await screen.findByRole('button', { name: 'Add model' }))
    let dialog = await screen.findByRole('dialog')
    await userEvent.type(within(dialog).getByLabelText('Model name'), 'model-a')
    await userEvent.type(within(dialog).getByLabelText('Model ratio'), '1')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Add' }))
    expect(
      await within(dialog).findByText('A model with this name is already priced')
    ).toBeVisible()

    const name = within(dialog).getByLabelText('Model name')
    await userEvent.clear(name)
    await userEvent.type(name, 'model-d')
    await userEvent.click(within(dialog).getByRole('combobox', { name: 'Billing' }))
    await userEvent.click(await screen.findByRole('option', { name: 'Per request' }))
    dialog = screen.getByRole('dialog')
    await userEvent.type(
      within(dialog).getByLabelText('Fixed price per request (USD)'),
      '0.5'
    )
    await userEvent.click(within(dialog).getByRole('button', { name: 'Add' }))
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0].key).toBe('ModelPrice')
    expect(JSON.parse(puts[0].value as string)).toEqual({
      'model-c': 0.04,
      'model-d': 0.5,
    })
  })
})

describe('rate limits', () => {
  it('adds a group through the visual editor and saves the JSON option', async () => {
    const puts = installApi({})
    renderPage('limits')
    expect(await screen.findByText('default')).toBeVisible()

    await userEvent.click(screen.getByRole('button', { name: 'Add group' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.type(within(dialog).getByLabelText('Group Name'), 'vip')
    await userEvent.type(
      within(dialog).getByLabelText('Max Requests (including failures)'),
      '0'
    )
    await userEvent.type(within(dialog).getByLabelText('Max Successful Requests'), '900')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Add' }))
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0].key).toBe('ModelRequestRateLimitGroup')
    expect(JSON.parse(puts[0].value as string)).toEqual({
      default: [200, 100],
      vip: [0, 900],
    })
  })
})

describe('quota', () => {
  it('sends whole numbers as strings and shows the amount in the display currency', async () => {
    const puts = installApi({})
    renderPage('quota')
    const field = await screen.findByLabelText('New User Quota')
    expect(field).toHaveValue(500000)
    expect(await screen.findByText('Equals $1.00')).toBeVisible()

    await userEvent.clear(field)
    await userEvent.type(field, '1000000')
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0]).toEqual({ key: 'QuotaForNewUser', value: '1000000' })
  })
})

describe('upstream sync', () => {
  it('copies the chosen upstream value into the draft without saving', async () => {
    const fetched: unknown[] = []
    const puts = installApi({
      onFetch: (body) => {
        fetched.push(body)
        return SYNC_RESULT
      },
    })
    renderPage('model-prices')
    await screen.findByText('$3.0000 / 1M tokens')
    await userEvent.click(screen.getByRole('tab', { name: 'Upstream sync' }))
    await userEvent.click(
      await screen.findByRole('button', { name: 'Select price sources' })
    )
    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText('Official ratio preset')).toBeVisible()
    await userEvent.click(within(dialog).getByRole('checkbox', { name: /Source A/ }))
    await userEvent.click(within(dialog).getByRole('button', { name: 'Fetch prices' }))

    expect(fetched).toEqual([{ channel_ids: [7], timeout: 10 }])
    const use = await screen.findByRole('button', {
      name: 'Use 3 from Source A(7) for model-a',
    })
    // A value the upstream itself marks as doubtful is still offered, flagged.
    expect(screen.getByLabelText('Low confidence')).toBeInTheDocument()
    await userEvent.click(use)
    expect(use).toHaveAttribute('aria-pressed', 'true')
    await userEvent.click(
      screen.getByRole('button', { name: 'Copy selected into draft' })
    )
    expect(puts).toHaveLength(0)

    await userEvent.click(screen.getByRole('tab', { name: 'Models' }))
    expect(await screen.findByText('Unsaved')).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0].key).toBe('ModelRatio')
    expect(JSON.parse(puts[0].value as string)).toEqual({
      'model-a': 3,
      'model-b': 0.25,
    })
  })

  it('reports a source that could not be reached', async () => {
    installApi({
      onFetch: () => ({
        differences: {},
        test_results: [
          { name: 'Source A(7)', status: 'error', error: 'timeout' },
        ],
      }),
    })
    renderPage('model-prices')
    await userEvent.click(await screen.findByRole('tab', { name: 'Upstream sync' }))
    await userEvent.click(
      await screen.findByRole('button', { name: 'Select price sources' })
    )
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(await within(dialog).findByRole('checkbox', { name: /Source A/ }))
    await userEvent.click(within(dialog).getByRole('button', { name: 'Fetch prices' }))
    expect(await screen.findByText('Some sources failed')).toBeVisible()
    expect(screen.getByText(/timeout/)).toBeVisible()
  })
})

describe('outbound requests', () => {
  it('saves a domain list as a JSON array and keeps secrets write-only', async () => {
    const puts = installApi({
      list: [
        { key: 'fetch_setting.enable_ssrf_protection', value: 'true' },
        { key: 'fetch_setting.domain_list', value: '["a.example.com"]' },
        { key: 'fetch_setting.allowed_ports', value: '["80","443"]' },
      ],
    })
    renderPage('outbound')
    const domains = await screen.findByLabelText('Domain list')
    expect(domains).toHaveValue('a.example.com')
    expect(screen.getByLabelText('Allowed Ports')).toHaveValue('80,443')
    expect(screen.getByLabelText('Worker access key')).toHaveValue('')

    await userEvent.type(domains, '{Enter}b.example.com')
    await userEvent.type(screen.getByLabelText('Worker access key'), 's3cret')
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(puts).toHaveLength(2))
    expect(puts).toEqual([
      {
        key: 'fetch_setting.domain_list',
        value: '["a.example.com","b.example.com"]',
      },
      { key: 'WorkerValidKey', value: 's3cret' },
    ])
  })

  it('refuses a malformed port before sending', async () => {
    const puts = installApi({
      list: [
        { key: 'fetch_setting.enable_ssrf_protection', value: 'true' },
        { key: 'fetch_setting.allowed_ports', value: '["80"]' },
      ],
    })
    renderPage('outbound')
    const ports = await screen.findByLabelText('Allowed Ports')
    await userEvent.type(ports, ',http')
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    expect(await screen.findByText('Enter ports such as 80 or 8000-9000')).toBeVisible()
    expect(puts).toHaveLength(0)
  })
})

describe('logs', () => {
  it('keeps the destructive cleanup disabled until a time is chosen', async () => {
    installApi({})
    renderPage('logs')
    expect(await screen.findByRole('button', { name: 'Clean logs' })).toBeDisabled()
  })
})

const MAIL_OPTIONS: Option[] = [
  { key: 'SMTPServer', value: 'smtp.example.com' },
  { key: 'SMTPPort', value: '587' },
  { key: 'SMTPAccount', value: 'mailer@example.com' },
  { key: 'SMTPFrom', value: 'noreply@example.com' },
  // The server withholds this key; even if a stale copy reached the page it
  // must never be put in the field.
  { key: 'SMTPToken', value: 'stored-secret' },
  { key: 'SMTPSSLEnabled', value: 'false' },
]

describe('email (SMTP)', () => {
  it('is reachable from the section list under its own slug', async () => {
    installApi({})
    renderPage('branding')
    const nav = await screen.findByRole('navigation', {
      name: 'Settings sections',
    })
    await userEvent.click(within(nav).getByRole('link', { name: 'Email (SMTP)' }))
    expect(navigate).toHaveBeenCalledWith({
      to: '/admin/settings/$section',
      params: { section: 'email' },
    })
  })

  it('shows the stored values, never prefills the secret and sends only changes', async () => {
    const puts = installApi({ list: MAIL_OPTIONS })
    renderPage('email')
    expect(await screen.findByLabelText('SMTP server')).toHaveValue(
      'smtp.example.com'
    )
    expect(screen.getByLabelText('SMTP port')).toHaveValue(587)
    expect(screen.getByLabelText('SMTP account')).toHaveValue(
      'mailer@example.com'
    )
    expect(screen.getByLabelText('Sender address')).toHaveValue(
      'noreply@example.com'
    )
    const token = screen.getByLabelText('SMTP password or access code')
    expect(token).toHaveValue('')
    expect(token).toHaveAttribute('type', 'password')
    expect(screen.getByRole('switch', { name: 'Use SSL' })).not.toBeChecked()

    // Saving with the secret left blank must not write it.
    const server = screen.getByLabelText('SMTP server')
    await userEvent.clear(server)
    await userEvent.type(server, 'smtp.other.example.com')
    const port = screen.getByLabelText('SMTP port')
    await userEvent.clear(port)
    await userEvent.type(port, '465')
    await userEvent.click(screen.getByRole('switch', { name: 'Use SSL' }))
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() => expect(puts).toHaveLength(3))
    expect(puts).toEqual([
      { key: 'SMTPServer', value: 'smtp.other.example.com' },
      { key: 'SMTPPort', value: '465' },
      { key: 'SMTPSSLEnabled', value: true },
    ])
    expect(puts.some((put) => put.key === 'SMTPToken')).toBe(false)
  })

  it('writes the secret only when one is typed', async () => {
    const puts = installApi({ list: MAIL_OPTIONS })
    renderPage('email')
    await userEvent.type(
      await screen.findByLabelText('SMTP password or access code'),
      'new-code'
    )
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0]).toEqual({ key: 'SMTPToken', value: 'new-code' })
  })

  it('refuses a port outside 1-65535 before sending', async () => {
    const puts = installApi({ list: MAIL_OPTIONS })
    renderPage('email')
    const port = await screen.findByLabelText('SMTP port')
    await userEvent.clear(port)
    await userEvent.type(port, '70000')
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    expect(await screen.findByText('Value is above the maximum')).toBeVisible()
    expect(puts).toHaveLength(0)
  })

  it('shows a refusal sent as HTTP 200 success:false and keeps the edit', async () => {
    const puts = installApi({
      list: MAIL_OPTIONS,
      onPut: () => ({
        status: 200,
        body: { success: false, message: 'smtp host is not allowed' },
      }),
    })
    renderPage('email')
    const server = await screen.findByLabelText('SMTP server')
    await userEvent.clear(server)
    await userEvent.type(server, 'localhost')
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    expect(await screen.findByText('smtp host is not allowed')).toBeVisible()
    expect(puts).toHaveLength(1)
    expect(toastError).toHaveBeenCalledWith(
      'SMTP server: smtp host is not allowed'
    )
    expect(toastSuccess).not.toHaveBeenCalled()
    expect(server).toHaveValue('localhost')
  })
})

describe('drawing', () => {
  const DRAWING_OPTIONS: Option[] = [
    { key: 'DrawingEnabled', value: 'true' },
    { key: 'MjNotifyEnabled', value: 'false' },
    { key: 'MjAccountFilterEnabled', value: 'false' },
    { key: 'MjModeClearEnabled', value: 'true' },
    { key: 'MjForwardUrlEnabled', value: 'false' },
    { key: 'MjActionCheckSuccessEnabled', value: 'false' },
  ]

  it('is reachable from the section list under its own slug', async () => {
    installApi({})
    renderPage('branding')
    const nav = await screen.findByRole('navigation', {
      name: 'Settings sections',
    })
    await userEvent.click(within(nav).getByRole('link', { name: 'Drawing' }))
    expect(navigate).toHaveBeenCalledWith({
      to: '/admin/settings/$section',
      params: { section: 'drawing' },
    })
  })

  it('shows the stored switches and sends only the ones flipped', async () => {
    const puts = installApi({ list: DRAWING_OPTIONS })
    renderPage('drawing')
    expect(
      await screen.findByRole('switch', { name: 'Enable drawing' })
    ).toBeChecked()
    expect(
      screen.getByRole('switch', { name: 'Allow callbacks' })
    ).not.toBeChecked()
    expect(
      screen.getByRole('switch', { name: 'Strip speed modes from prompts' })
    ).toBeChecked()

    await userEvent.click(screen.getByRole('switch', { name: 'Allow callbacks' }))
    await userEvent.click(
      screen.getByRole('switch', { name: 'Strip speed modes from prompts' })
    )
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() => expect(puts).toHaveLength(2))
    expect(puts).toEqual([
      { key: 'MjNotifyEnabled', value: true },
      { key: 'MjModeClearEnabled', value: false },
    ])
  })

  it('shows a server refusal as an error', async () => {
    installApi({
      list: DRAWING_OPTIONS,
      onPut: () => ({
        status: 200,
        body: { success: false, message: 'drawing is locked' },
      }),
    })
    renderPage('drawing')
    await userEvent.click(
      await screen.findByRole('switch', { name: 'Enable drawing' })
    )
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    expect(await screen.findByText('drawing is locked')).toBeVisible()
    expect(toastError).toHaveBeenCalledWith('Enable drawing: drawing is locked')
  })
})

describe('pre-consumed quota', () => {
  it('shows the stored amount and saves a changed one as a string', async () => {
    const puts = installApi({
      list: [{ key: 'PreConsumedQuota', value: '500' }],
    })
    renderPage('quota')
    const field = await screen.findByLabelText('Pre-Consumed Quota')
    expect(field).toHaveValue(500)
    await userEvent.clear(field)
    await userEvent.type(field, '2000')
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0]).toEqual({ key: 'PreConsumedQuota', value: '2000' })
  })

  it('refuses a negative amount and shows a server refusal', async () => {
    const puts = installApi({
      list: [{ key: 'PreConsumedQuota', value: '500' }],
      onPut: () => ({
        status: 200,
        body: { success: false, message: 'too large' },
      }),
    })
    renderPage('quota')
    const field = await screen.findByLabelText('Pre-Consumed Quota')
    await userEvent.clear(field)
    await userEvent.type(field, '-1')
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    expect(await screen.findByText('Value is below the minimum')).toBeVisible()
    expect(puts).toHaveLength(0)

    await userEvent.clear(field)
    await userEvent.type(field, '99')
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    expect(await screen.findByText('too large')).toBeVisible()
    expect(puts).toHaveLength(1)
  })
})

describe('currency conversion', () => {
  const CURRENCY_OPTIONS: Option[] = [
    { key: 'general_setting.quota_display_type', value: 'USD' },
    { key: 'USDExchangeRate', value: '7.3' },
    { key: 'QuotaPerUnit', value: '500000' },
    { key: 'Price', value: '7.3' },
  ]
  const PRICE_LABEL = 'Display exchange price (CNY per USD)'

  it('shows the current values and warns before they are edited', async () => {
    installApi({ list: CURRENCY_OPTIONS })
    renderPage('currency')
    expect(await screen.findByLabelText('Quota per USD')).toHaveValue(500000)
    expect(screen.getByLabelText(PRICE_LABEL)).toHaveValue(7.3)
    expect(screen.getByRole('note')).toHaveTextContent(
      /change what every account sees and is charged/
    )
  })

  it('asks for confirmation, sends nothing on cancel and writes on confirm', async () => {
    const puts = installApi({ list: CURRENCY_OPTIONS })
    renderPage('currency')
    const field = await screen.findByLabelText('Quota per USD')
    await userEvent.clear(field)
    await userEvent.type(field, '1000000')
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))

    let dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText('Change money conversion?')).toBeVisible()
    expect(within(dialog).getByText(/500000 -> 1000000/)).toBeVisible()
    expect(puts).toHaveLength(0)

    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(puts).toHaveLength(0)
    expect(field).toHaveValue(1000000)

    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    dialog = await screen.findByRole('alertdialog')
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Change conversion' })
    )
    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0]).toEqual({ key: 'QuotaPerUnit', value: '1000000' })
    await waitFor(() => expect(toastSuccess).toHaveBeenCalled())
  })

  it('confirms the exchange price too', async () => {
    const puts = installApi({ list: CURRENCY_OPTIONS })
    renderPage('currency')
    const field = await screen.findByLabelText(PRICE_LABEL)
    await userEvent.clear(field)
    await userEvent.type(field, '7.1')
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(puts).toHaveLength(0)
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Change conversion' })
    )
    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0]).toEqual({ key: 'Price', value: '7.1' })
  })

  it.each(['0', '-5'])('rejects %s without asking or sending', async (bad) => {
    const puts = installApi({ list: CURRENCY_OPTIONS })
    renderPage('currency')
    const field = await screen.findByLabelText('Quota per USD')
    await userEvent.clear(field)
    await userEvent.type(field, bad)
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    expect(await screen.findByText('Must be greater than 0')).toBeVisible()
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(puts).toHaveLength(0)
  })

  it('rejects a non-positive exchange price too', async () => {
    const puts = installApi({ list: CURRENCY_OPTIONS })
    renderPage('currency')
    const field = await screen.findByLabelText(PRICE_LABEL)
    await userEvent.clear(field)
    await userEvent.type(field, '0')
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    expect(await screen.findByText('Must be greater than 0')).toBeVisible()
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(puts).toHaveLength(0)
  })

  it('does not ask when only an unguarded field changed', async () => {
    const puts = installApi({ list: CURRENCY_OPTIONS })
    renderPage('currency')
    await userEvent.click(
      await screen.findByRole('switch', { name: 'Display Token Statistics' })
    )
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0]).toEqual({ key: 'DisplayTokenStatEnabled', value: true })
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })

  it('shows a refusal after the confirmation and keeps the edit', async () => {
    const puts = installApi({
      list: CURRENCY_OPTIONS,
      onPut: () => ({
        status: 200,
        body: { success: false, message: 'QuotaPerUnit is out of range' },
      }),
    })
    renderPage('currency')
    const field = await screen.findByLabelText('Quota per USD')
    await userEvent.clear(field)
    await userEvent.type(field, '2000000000')
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    // Over the client-side ceiling: refused before any dialog.
    expect(await screen.findByText('Value is above the maximum')).toBeVisible()
    expect(puts).toHaveLength(0)

    await userEvent.clear(field)
    await userEvent.type(field, '300')
    await userEvent.click(screen.getByRole('button', { name: 'Save Changes' }))
    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Change conversion' })
    )
    expect(await screen.findByText('QuotaPerUnit is out of range')).toBeVisible()
    expect(puts).toHaveLength(1)
    expect(toastError).toHaveBeenCalledWith(
      'Quota per USD: QuotaPerUnit is out of range'
    )
    expect(field).toHaveValue(300)
  })
})
