import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

import { CommandMenu } from '@/components/command-menu'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { SidebarProvider } from '@/components/ui/sidebar'
import { DirectionProvider } from '@/context/direction-provider'
import { LayoutProvider } from '@/context/layout-provider'
import { SearchProvider } from '@/context/search-provider'
import { currentUserQueryKey, type CurrentUser } from '@/lib/user'

import { AppHeader } from './app-header'
import { AppSidebar } from './app-sidebar'

const { navigate, apiGet, signOutMock, route } = vi.hoisted(() => ({
  navigate: vi.fn(),
  apiGet: vi.fn(),
  signOutMock: vi.fn(),
  route: { pathname: '/dashboard' },
}))

vi.mock('@tanstack/react-router', () => ({
  Link: ({
    to,
    children,
    ...rest
  }: { to: string; children?: ReactNode } & Record<string, unknown>) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
  useRouterState: ({
    select,
  }: {
    select: (s: { location: { pathname: string } }) => unknown
  }) => select({ location: { pathname: route.pathname } }),
  useNavigate: () => navigate,
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return {
    ...actual,
    api: { get: apiGet, post: vi.fn(), put: vi.fn(), delete: vi.fn() },
  }
})

vi.mock('@/lib/session', () => ({ signOut: signOutMock }))

function user(over: Partial<CurrentUser> = {}): CurrentUser {
  return {
    id: 7,
    username: 'dana',
    display_name: 'Dana',
    email: 'dana@example.test',
    role: 1,
    quota: 0,
    used_quota: 0,
    remaining_quota: 0,
    tenant_role: '',
    ...over,
  }
}

function renderShell(ui: ReactNode, me: CurrentUser = user()) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(currentUserQueryKey, me)
  const view = render(
    <QueryClientProvider client={client}>
      <DirectionProvider>
        <LayoutProvider>
          <SidebarProvider>
            <SearchProvider>{ui}</SearchProvider>
          </SidebarProvider>
        </LayoutProvider>
      </DirectionProvider>
    </QueryClientProvider>
  )
  return { client, ...view }
}

function mockApi(opts: { notice?: string | null; noticeFails?: boolean } = {}) {
  apiGet.mockImplementation((url: string) => {
    if (url === '/api/notice') {
      return opts.noticeFails
        ? Promise.reject(new Error('boom'))
        : Promise.resolve(opts.notice ?? '')
    }
    if (url === '/api/status') {
      return Promise.resolve({
        announcements_enabled: false,
        quota_per_unit: 500000,
        quota_display_type: 'USD',
      })
    }
    return Promise.reject(new Error(`unexpected GET ${url}`))
  })
}

beforeAll(() => {
  // jsdom lacks the Web Animations API the scroll area probes for.
  Object.defineProperty(HTMLElement.prototype, 'getAnimations', {
    configurable: true,
    value: () => [],
  })
})

beforeEach(() => {
  vi.clearAllMocks()
  window.localStorage.clear()
  route.pathname = '/dashboard'
  mockApi()
})

describe('AppSidebar', () => {
  it('shows a plain member the workbench and no classic-console group', () => {
    renderShell(<AppSidebar />)
    expect(screen.getByRole('link', { name: 'Dashboard' })).toBeTruthy()
    expect(screen.queryByText('Platform Operations')).toBeNull()
    expect(screen.queryByText('Classic console')).toBeNull()
  })

  it('marks only the most specific entry as the current page', () => {
    route.pathname = '/usage-logs/audit'
    renderShell(<AppSidebar />, user({ tenant_role: 'admin' }))
    expect(
      screen.getByRole('link', { name: 'Audit Log' }).getAttribute('aria-current')
    ).toBe('page')
    expect(
      screen.getByRole('link', { name: 'Usage Logs' }).getAttribute('aria-current')
    ).toBeNull()
  })

  it('offers root the classic links as plain anchors behind a disclosure', async () => {
    renderShell(<AppSidebar />, user({ role: 100 }))
    const toggle = screen.getByText('Classic console')
    expect(screen.queryByText('Diagnostics')).toBeNull()
    await userEvent.click(toggle)
    const link = await screen.findByRole('link', { name: /Diagnostics/ })
    expect(link.getAttribute('href')).toBe('/console/v2/admin/diagnostics')
  })

  it('shows a tenant admin only the tenant-level classic pages', async () => {
    renderShell(<AppSidebar />, user({ tenant_role: 'admin' }))
    await userEvent.click(screen.getByText('Classic console'))
    expect(await screen.findByRole('link', { name: /Flows/ })).toBeTruthy()
    expect(screen.getByRole('link', { name: /Pricing/ })).toBeTruthy()
    expect(screen.queryByRole('link', { name: /Diagnostics/ })).toBeNull()
  })
})

describe('command palette', () => {
  it('opens on Ctrl+K, lists gated entries, and navigates on select', async () => {
    renderShell(<CommandMenu />, user({ role: 100 }))
    expect(screen.queryByPlaceholderText('Type a command or search...')).toBeNull()
    await userEvent.keyboard('{Control>}k{/Control}')
    const input = await screen.findByPlaceholderText(
      'Type a command or search...'
    )
    expect(screen.getByText('Platform Operations')).toBeTruthy()
    await userEvent.type(input, 'API Keys')
    await userEvent.click(await screen.findByText('API Keys'))
    expect(navigate).toHaveBeenCalledWith({ to: '/keys' })
  })

  it('does not list platform entries for a plain member', async () => {
    renderShell(<CommandMenu />)
    await userEvent.keyboard('{Control>}k{/Control}')
    await screen.findByPlaceholderText('Type a command or search...')
    expect(screen.queryByText('Platform Operations')).toBeNull()
    expect(screen.queryByText('Classic console')).toBeNull()
  })
})

describe('notifications', () => {
  it('shows an unread count for a new notice and clears it once opened', async () => {
    mockApi({ notice: 'Maintenance tonight' })
    renderShell(<AppHeader />)
    const bell = await screen.findByRole('button', { name: 'Notifications' })
    await waitFor(() => expect(within(bell).getByText('1')).toBeTruthy())
    await userEvent.click(bell)
    expect(await screen.findByText('Maintenance tonight')).toBeTruthy()
    await waitFor(() => expect(within(bell).queryByText('1')).toBeNull())
    expect(
      window.localStorage.getItem('newhub-console:notice:v1:last-read')
    ).toBeTruthy()
  })

  it('stays quiet when this notice was already read', async () => {
    mockApi({ notice: 'Maintenance tonight' })
    const first = renderShell(<AppHeader />)
    await userEvent.click(
      await screen.findByRole('button', { name: 'Notifications' })
    )
    await screen.findByText('Maintenance tonight')
    first.unmount()
    renderShell(<AppHeader />)
    const bell = await screen.findByRole('button', { name: 'Notifications' })
    await waitFor(() => expect(apiGet).toHaveBeenCalledWith(
      '/api/notice',
      expect.anything()
    ))
    expect(within(bell).queryByText('1')).toBeNull()
  })

  it('says so when the notice cannot be loaded', async () => {
    mockApi({ noticeFails: true })
    renderShell(<AppHeader />)
    await userEvent.click(
      await screen.findByRole('button', { name: 'Notifications' })
    )
    expect(await screen.findByText('Could not load the notice')).toBeTruthy()
  })
})

describe('profile menu', () => {
  it('asks before signing out, then ends the session', async () => {
    const { client } = renderShell(<ProfileDropdown />)
    client.setQueryData(['marker'], 1)
    await userEvent.click(screen.getByRole('button', { name: 'Account' }))
    await userEvent.click(await screen.findByText('Sign out'))
    expect(signOutMock).not.toHaveBeenCalled()
    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Sign out' }))
    expect(signOutMock).toHaveBeenCalledTimes(1)
    expect(client.getQueryData(['marker'])).toBeUndefined()
  })

  it('keeps the session when the confirmation is cancelled', async () => {
    renderShell(<ProfileDropdown />)
    await userEvent.click(screen.getByRole('button', { name: 'Account' }))
    await userEvent.click(await screen.findByText('Sign out'))
    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(signOutMock).not.toHaveBeenCalled()
  })
})
