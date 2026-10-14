import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ProfileDropdown } from '@/components/profile-dropdown'
import { DirectionProvider } from '@/context/direction-provider'
import { currentUserQueryKey, type CurrentUser } from '@/lib/user'

const { httpPost, navigate } = vi.hoisted(() => ({
  httpPost: vi.fn(),
  navigate: vi.fn(),
}))

vi.mock('@tanstack/react-router', () => ({
  Link: ({
    to,
    children,
    ...rest
  }: { to: string; children?: React.ReactNode } & Record<string, unknown>) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
  useNavigate: () => navigate,
}))

// The real lib/session is used on purpose: only the HTTP edge is faked.
vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return { ...actual, http: { post: httpPost } }
})

const me: CurrentUser = {
  id: 7,
  username: 'dana',
  display_name: 'Dana',
  email: 'dana@example.test',
  role: 1,
  quota: 0,
  used_quota: 0,
  remaining_quota: 0,
  tenant_role: '',
}

describe('sign out when the logout request fails', () => {
  const assign = vi.fn()

  beforeEach(() => {
    vi.clearAllMocks()
    window.localStorage.clear()
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { ...window.location, assign },
    })
  })

  it('still clears local state and leaves for the login page', async () => {
    httpPost.mockRejectedValue(new Error('network down'))
    window.localStorage.setItem('user', '{"id":7}')
    window.localStorage.setItem('tenant_slug', 'acme')
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    client.setQueryData(currentUserQueryKey, me)
    render(
      <QueryClientProvider client={client}>
        <DirectionProvider>
          <ProfileDropdown />
        </DirectionProvider>
      </QueryClientProvider>
    )
    await userEvent.click(screen.getByRole('button', { name: 'Account' }))
    await userEvent.click(await screen.findByText('Sign out'))
    const dialog = await screen.findByRole('alertdialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Sign out' }))

    await waitFor(() => expect(assign).toHaveBeenCalledWith('/login'))
    expect(httpPost).toHaveBeenCalledTimes(1)
    expect(window.localStorage.getItem('user')).toBeNull()
    expect(window.localStorage.getItem('tenant_slug')).toBeNull()
  })
})
