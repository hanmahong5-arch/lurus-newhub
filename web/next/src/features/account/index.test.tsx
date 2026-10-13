import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api-error'

import * as api from './api'
import { AccountPage } from './index'
import {
  checkSettings,
  formFromSettings,
  parseUserSettings,
} from './lib/settings'

const toast = vi.hoisted(() => ({ error: vi.fn(), success: vi.fn() }))
vi.mock('sonner', () => ({ toast }))

const signOut = vi.hoisted(() => vi.fn())
vi.mock('@/lib/session', () => ({ signOut }))

vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({ status: {}, loading: false, error: null }),
}))
// One stable object, like the real query-backed hook: a fresh object per
// render would re-derive the form on every render.
const MONEY = vi.hoisted(() => ({
  quotaPerUnit: 500000,
  displayType: 'USD' as const,
  symbol: '$',
  rate: 1,
}))
vi.mock('@/lib/status', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/status')>()),
  useMoneyConfig: () => MONEY,
}))
// The step-up prompt itself is covered in components/step-up; here the gate
// is already open and the action just runs.
vi.mock('@/components/step-up', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/step-up')>()),
  useStepUp: () => ({ run: <T,>(a: () => Promise<T>) => a(), dialog: null }),
}))
vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  fetchProfile: vi.fn(),
  saveUserSettings: vi.fn(),
  fetchTotpStatus: vi.fn(),
  enrollTotp: vi.fn(),
  confirmTotp: vi.fn(),
  regenerateBackupCodes: vi.fn(),
  disableTotp: vi.fn(),
  fetchSessions: vi.fn(),
  revokeSession: vi.fn(),
  revokeOtherSessions: vi.fn(),
}))
vi.mock('qrcode.react', () => ({
  QRCodeSVG: (p: { value: string }) => (
    <svg data-testid='qr' data-value={p.value} />
  ),
}))

const profile = (over: Record<string, unknown> = {}) => ({
  id: 7,
  username: 'ada',
  display_name: 'Ada L',
  email: 'ada@example.com',
  role: 1,
  group: 'default',
  quota: 1_000_000,
  used_quota: 250_000,
  remaining_quota: 1_000_000,
  request_count: 42,
  tenant_slug: 'acme',
  token_count: 3,
  setting: JSON.stringify({
    notify_type: 'webhook',
    quota_warning_threshold: 500_000,
    webhook_url: 'https://hook.example.com/x',
    webhook_secret: 's3',
    record_ip_log: true,
  }),
  ...over,
})

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <AccountPage />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.mocked(api.fetchProfile).mockResolvedValue(profile() as never)
  vi.mocked(api.fetchTotpStatus).mockResolvedValue({
    enrolled: false,
    pending: false,
    backup_codes_remaining: 0,
  })
  vi.mocked(api.fetchSessions).mockResolvedValue({
    registry_enabled: true,
    items: [
      {
        id: 1,
        current: true,
        auth_method: 'session',
        created_at: 1_700_000_000,
        last_seen_at: 1_700_000_100,
        ip: '10.0.0.0',
        user_agent_family: 'Firefox',
      },
      {
        id: 2,
        current: false,
        auth_method: 'oidc',
        created_at: 1_700_000_000,
        last_seen_at: 1_700_000_200,
        ip: '10.0.1.0',
        user_agent_family: 'Chrome',
      },
    ],
  })
})

describe('profile tab', () => {
  it('shows the identity read-only and the balances from the money config', async () => {
    renderPage()
    expect(
      await screen.findByRole('heading', { name: 'Ada L' })
    ).toBeInTheDocument()
    expect(screen.getByText('@ada')).toBeInTheDocument()
    // remaining 1,000,000 / 500,000 per unit = $2.00 ; used 250,000 = $0.50
    expect(screen.getByText('$2.00')).toBeInTheDocument()
    expect(screen.getByText('$0.50')).toBeInTheDocument()
    expect(screen.getByText('acme')).toBeInTheDocument()
    expect(
      screen.getByText(/managed by the platform account/i)
    ).toBeInTheDocument()
    // Identity is owned by the platform: nothing here is editable.
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  })

  it('surfaces a load failure with a retry', async () => {
    vi.mocked(api.fetchProfile).mockRejectedValueOnce(
      new ApiError('profile down', { status: 500 })
    )
    renderPage()
    expect(await screen.findByText('profile down')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(
      await screen.findByRole('heading', { name: 'Ada L' })
    ).toBeInTheDocument()
  })
})

describe('notifications tab', () => {
  async function openTab() {
    renderPage()
    await userEvent.click(
      await screen.findByRole('tab', { name: /Notifications/ })
    )
    return await screen.findByLabelText(/Quota Warning Threshold/)
  }

  it('loads the stored preferences and saves a change in quota units', async () => {
    vi.mocked(api.saveUserSettings).mockResolvedValue(undefined)
    const threshold = await openTab()
    // 500,000 quota units = $1 at the mocked rate.
    expect(threshold).toHaveValue(1)
    expect(screen.getByLabelText('Webhook URL')).toHaveValue(
      'https://hook.example.com/x'
    )

    await userEvent.clear(threshold)
    await userEvent.type(threshold, '3')
    await userEvent.click(screen.getByRole('button', { name: 'Save Settings' }))

    await waitFor(() => expect(api.saveUserSettings).toHaveBeenCalledTimes(1))
    expect(api.saveUserSettings).toHaveBeenCalledWith({
      notify_type: 'webhook',
      quota_warning_threshold: 1_500_000,
      webhook_url: 'https://hook.example.com/x',
      webhook_secret: 's3',
      accept_unset_model_ratio_model: false,
      record_ip_log: true,
    })
    await waitFor(() => expect(toast.success).toHaveBeenCalled())
  })

  it('reports a server rejection and keeps the form', async () => {
    vi.mocked(api.saveUserSettings).mockRejectedValue(
      new ApiError('webhook unreachable', { status: 200 })
    )
    const threshold = await openTab()
    await userEvent.clear(threshold)
    await userEvent.type(threshold, '2')
    await userEvent.click(screen.getByRole('button', { name: 'Save Settings' }))
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('webhook unreachable')
    )
    expect(screen.getByLabelText(/Quota Warning Threshold/)).toHaveValue(2)
  })

  it('stops a bad value before the request', async () => {
    const threshold = await openTab()
    expect(threshold).toHaveValue(1)
    await userEvent.clear(screen.getByLabelText('Webhook URL'))
    await userEvent.click(screen.getByRole('button', { name: 'Save Settings' }))
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('The webhook URL is required.')
    )
    expect(api.saveUserSettings).not.toHaveBeenCalled()
  })
})

describe('security tab: sessions', () => {
  async function openTab() {
    renderPage()
    await userEvent.click(await screen.findByRole('tab', { name: /Security/ }))
  }

  it('lists devices and revokes another one', async () => {
    vi.mocked(api.revokeSession).mockResolvedValue(undefined)
    await openTab()
    expect(await screen.findByText('Firefox')).toBeInTheDocument()
    expect(screen.getByText('Chrome')).toBeInTheDocument()
    expect(screen.getByText('Current')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Revoke' }))
    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Revoke' })
    )
    await waitFor(() => expect(api.revokeSession).toHaveBeenCalledWith(2))
    await waitFor(() => expect(toast.success).toHaveBeenCalled())
  })

  it('shows the server message when a revoke fails', async () => {
    vi.mocked(api.revokeSession).mockRejectedValue(
      new ApiError('Session not found', { status: 404 })
    )
    await openTab()
    await userEvent.click(await screen.findByRole('button', { name: 'Revoke' }))
    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Revoke' })
    )
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('Session not found')
    )
  })

  it('signs this browser out from its own row, after confirming', async () => {
    await openTab()
    const buttons = await screen.findAllByRole('button', { name: 'Sign out' })
    await userEvent.click(buttons[0])
    const dialog = await screen.findByRole('alertdialog')
    expect(signOut).not.toHaveBeenCalled()
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Sign out' })
    )
    expect(signOut).toHaveBeenCalledTimes(1)
  })

  it('has a plain sign-out for the session in this browser', async () => {
    await openTab()
    const buttons = await screen.findAllByRole('button', { name: 'Sign out' })
    await userEvent.click(buttons.at(-1) as HTMLElement)
    expect(signOut).toHaveBeenCalledTimes(1)
  })

  it('says so instead of inventing a device when the registry is off', async () => {
    vi.mocked(api.fetchSessions).mockResolvedValue({
      registry_enabled: false,
      items: [],
    })
    await openTab()
    expect(
      await screen.findByText('Per-device sessions are not tracked')
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Sign out other sessions' })
    ).toBeDisabled()
  })
})

describe('security tab: two-factor', () => {
  async function openTab() {
    renderPage()
    await userEvent.click(await screen.findByRole('tab', { name: /Security/ }))
  }

  it('enrols: scan, confirm a code, then shows the one-time backup codes', async () => {
    vi.mocked(api.enrollTotp).mockResolvedValue({
      secret: 'ABCDSECRET',
      otpauth_url: 'otpauth://totp/x?secret=ABCDSECRET',
    })
    vi.mocked(api.confirmTotp).mockResolvedValue({
      backup_codes: ['aaaa-1111', 'bbbb-2222'],
    })
    await openTab()
    await userEvent.click(await screen.findByRole('button', { name: 'Enable' }))
    expect(await screen.findByText('ABCDSECRET')).toBeInTheDocument()
    expect(screen.getByTestId('qr')).toHaveAttribute(
      'data-value',
      'otpauth://totp/x?secret=ABCDSECRET'
    )
    await userEvent.click(screen.getByRole('button', { name: 'Next' }))
    await userEvent.type(screen.getByLabelText('Verification Code'), '123456')
    await userEvent.click(screen.getByRole('button', { name: 'Enable 2FA' }))
    await waitFor(() => expect(api.confirmTotp).toHaveBeenCalledWith('123456'))
    expect(await screen.findByText('aaaa-1111')).toBeInTheDocument()
    expect(screen.getByText('bbbb-2222')).toBeInTheDocument()
  })

  it('keeps the dialog open and shows the reason when the code is wrong', async () => {
    vi.mocked(api.enrollTotp).mockResolvedValue({
      secret: 'ABCDSECRET',
      otpauth_url: 'otpauth://x',
    })
    vi.mocked(api.confirmTotp).mockRejectedValue(
      new ApiError('Invalid code, try again', { status: 400 })
    )
    await openTab()
    await userEvent.click(await screen.findByRole('button', { name: 'Enable' }))
    await screen.findByText('ABCDSECRET')
    await userEvent.click(screen.getByRole('button', { name: 'Next' }))
    await userEvent.type(screen.getByLabelText('Verification Code'), '000000')
    await userEvent.click(screen.getByRole('button', { name: 'Enable 2FA' }))
    expect(
      await screen.findByText('Invalid code, try again')
    ).toBeInTheDocument()
    expect(screen.queryByText('aaaa-1111')).not.toBeInTheDocument()
  })

  it('disables two-factor after the acknowledgement, and reports a failure', async () => {
    vi.mocked(api.fetchTotpStatus).mockResolvedValue({
      enrolled: true,
      pending: false,
      backup_codes_remaining: 4,
    })
    vi.mocked(api.disableTotp).mockRejectedValueOnce(
      new ApiError('verification expired', { status: 403 })
    )
    vi.mocked(api.disableTotp).mockResolvedValueOnce(undefined)
    await openTab()
    expect(
      await screen.findByText('Backup codes remaining: 4')
    ).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Disable 2FA' }))
    const dialog = await screen.findByRole('alertdialog')
    const confirm = within(dialog).getByRole('button', { name: 'Disable 2FA' })
    expect(confirm).toBeDisabled()
    await userEvent.click(within(dialog).getByRole('checkbox'))
    await userEvent.click(confirm)
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith('verification expired')
    )
    // The confirmation is shown again; a second attempt goes through.
    const again = await screen.findByRole('alertdialog')
    await userEvent.click(
      within(again).getByRole('button', { name: 'Disable 2FA' })
    )
    await waitFor(() => expect(api.disableTotp).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(toast.success).toHaveBeenCalled())
  })

  it('regenerates backup codes and shows them once', async () => {
    vi.mocked(api.fetchTotpStatus).mockResolvedValue({
      enrolled: true,
      pending: false,
      backup_codes_remaining: 1,
    })
    vi.mocked(api.regenerateBackupCodes).mockResolvedValue({
      backup_codes: ['new-1', 'new-2'],
    })
    await openTab()
    await userEvent.click(
      await screen.findByRole('button', { name: 'Regenerate Backup Codes' })
    )
    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(
      within(dialog).getByRole('button', { name: 'Generate New Codes' })
    )
    expect(await screen.findByText('new-1')).toBeInTheDocument()
    expect(screen.getByText('new-2')).toBeInTheDocument()
  })
})

describe('lib', () => {
  const t = ((k: string) => k) as never

  it('round-trips the stored setting through the form', () => {
    const settings = parseUserSettings(profile().setting)
    const form = formFromSettings(settings, (q) => q / 500_000)
    expect(form.threshold).toBe('1')
    expect(form.notifyType).toBe('webhook')
    const checked = checkSettings(form, (a) => a * 500_000, t)
    expect(checked.ok).toBe(true)
    if (checked.ok) {
      expect(checked.body.quota_warning_threshold).toBe(500_000)
      expect(checked.body.record_ip_log).toBe(true)
    }
  })

  it('treats unreadable settings as empty and leaves the threshold blank', () => {
    expect(parseUserSettings('not json')).toEqual({})
    const form = formFromSettings({}, () => null)
    expect(form.threshold).toBe('')
    const checked = checkSettings(form, () => null, t)
    expect(checked.ok).toBe(false)
  })

  it('normalises the session list without inventing rows', () => {
    expect(api.normalizeSessions(null)).toEqual({
      registry_enabled: false,
      items: [],
    })
    const list = api.normalizeSessions({
      registry_enabled: true,
      items: [{ id: 'current', current: true, last_seen: 5 }],
    })
    expect(list.items[0]).toMatchObject({
      id: 'current',
      current: true,
      last_seen_at: 5,
    })
  })
})
