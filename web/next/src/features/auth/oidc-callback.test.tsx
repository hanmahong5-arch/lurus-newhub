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
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'
import { currentUserQueryKey } from '@/lib/user'

import { OidcCallbackPage } from './oidc-callback'

const { httpGet, replace, toastError, toastSuccess } = vi.hoisted(() => ({
  httpGet: vi.fn(),
  replace: vi.fn(),
  toastError: vi.fn(),
  toastSuccess: vi.fn(),
}))

vi.mock('sonner', () => ({
  toast: { error: toastError, success: toastSuccess },
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return { ...actual, http: { get: httpGet } }
})

vi.mock('./lib/navigate', () => ({
  navigation: { replace, assign: vi.fn(), reload: vi.fn() },
}))

vi.mock('./components/auth-layout', () => ({
  AuthLayout: (props: { children: React.ReactNode }) => (
    <div data-testid='layout'>{props.children}</div>
  ),
}))

const ORIGIN = window.location.origin
const USER = { id: 7, username: 'alice', role: 1 }

function mount() {
  const client = new QueryClient()
  client.setQueryData(currentUserQueryKey, { id: 99, username: 'stale' })
  const utils = render(
    <QueryClientProvider client={client}>
      <OidcCallbackPage />
    </QueryClientProvider>
  )
  return { client, ...utils }
}

const session = (body: Record<string, unknown>) => ({ data: body })

beforeEach(() => {
  window.localStorage.clear()
  window.history.replaceState(null, '', '/oauth/oidc')
})

afterEach(() => {
  vi.useRealTimers()
  window.history.replaceState(null, '', '/')
})

describe('OidcCallbackPage: successful session exchange', () => {
  beforeEach(() => {
    httpGet.mockResolvedValue(session({ success: true, message: '', data: USER }))
  })

  it('asks the v2 session endpoint with the sign-in redirect suppressed', async () => {
    mount()
    await waitFor(() => expect(httpGet).toHaveBeenCalledTimes(1))
    expect(httpGet).toHaveBeenCalledWith('/api/v2/auth/session-info', {
      skipAuthRedirect: true,
    })
  })

  it('drops any stale tenant slug so the session starts clean', async () => {
    window.localStorage.setItem('tenant_slug', 'old-tenant')
    mount()
    await waitFor(() => expect(replace).toHaveBeenCalled())
    expect(window.localStorage.getItem('tenant_slug')).toBeNull()
  })

  it('persists the returned user and forgets the cached signed-out user', async () => {
    const { client } = mount()
    await waitFor(() => expect(replace).toHaveBeenCalled())
    expect(JSON.parse(window.localStorage.getItem('user') ?? 'null')).toEqual(
      USER
    )
    expect(client.getQueryData(currentUserQueryKey)).toBeUndefined()
  })

  it('lands the user on the console, not the login page', async () => {
    mount()
    await waitFor(() =>
      expect(replace).toHaveBeenCalledWith(`${ORIGIN}/dashboard`)
    )
    expect(toastSuccess).toHaveBeenCalledWith('Login successful!')
    expect(toastError).not.toHaveBeenCalled()
  })

  it('honours a safe ?redirect= on the callback URL', async () => {
    window.history.replaceState(null, '', '/oauth/oidc?redirect=%2Fkeys')
    mount()
    await waitFor(() => expect(replace).toHaveBeenCalledWith(`${ORIGIN}/keys`))
  })

  it('shows progress and no error text on the happy path', async () => {
    mount()
    await waitFor(() => expect(replace).toHaveBeenCalled())
    expect(screen.getByTestId('oidc-callback-progress')).toBeInTheDocument()
    expect(
      screen.queryByText('Returning to the sign-in page...')
    ).not.toBeInTheDocument()
  })
})

describe('OidcCallbackPage: rejected session', () => {
  it('does NOT establish a session when the backend answers success:false', async () => {
    httpGet.mockResolvedValue(
      session({ success: false, message: 'account disabled', data: null })
    )
    const { client } = mount()

    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith('account disabled')
    )
    // The security-relevant invariant: a refused login must leave no trace of
    // a logged-in user anywhere.
    expect(window.localStorage.getItem('user')).toBeNull()
    expect(replace).not.toHaveBeenCalled()
    expect(toastSuccess).not.toHaveBeenCalled()
    expect(client.getQueryData(currentUserQueryKey)).toBeDefined()
  })

  it('renders the failure message and the "returning to login" notice', async () => {
    httpGet.mockResolvedValue(
      session({ success: false, message: 'account disabled' })
    )
    mount()

    expect(await screen.findByText('account disabled')).toBeInTheDocument()
    expect(screen.getByText('Returning to the sign-in page...')).toBeInTheDocument()
    expect(screen.queryByTestId('oidc-callback-progress')).not.toBeInTheDocument()
  })

  it('falls back to a generic message when the backend sends none', async () => {
    httpGet.mockResolvedValue(session({ success: false }))
    mount()
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('Login failed'))
    expect(screen.getByText('Login failed')).toBeInTheDocument()
  })

  it("prefers the server's message over the transport error text", async () => {
    httpGet.mockRejectedValue(
      new ApiError('token expired', { status: 401 })
    )
    mount()
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('token expired'))
  })

  it('reports a failure that carries no text as a generic login failure', async () => {
    httpGet.mockRejectedValue(new ApiError('', { status: 500 }))
    mount()
    await waitFor(() => expect(toastError).toHaveBeenCalledWith('Login failed'))
  })

  it('bounces back to /login after the grace period', async () => {
    vi.useFakeTimers()
    httpGet.mockResolvedValue(session({ success: false, message: 'nope' }))
    mount()
    await act(async () => {
      for (let i = 0; i < 6; i++) await Promise.resolve()
    })

    expect(replace).not.toHaveBeenCalled()
    await act(async () => {
      vi.advanceTimersByTime(2999)
    })
    expect(replace).not.toHaveBeenCalled()
    await act(async () => {
      vi.advanceTimersByTime(1)
    })
    expect(replace).toHaveBeenCalledWith('/login')
  })

  it('lets the person leave at once instead of waiting', async () => {
    httpGet.mockResolvedValue(session({ success: false, message: 'nope' }))
    const user = userEvent.setup()
    mount()
    await user.click(await screen.findByRole('button', { name: 'Back to sign in' }))
    expect(replace).toHaveBeenCalledWith('/login')
  })

  it('does not bounce after the page has been left', async () => {
    vi.useFakeTimers()
    httpGet.mockResolvedValue(session({ success: false, message: 'nope' }))
    const { unmount } = mount()
    await act(async () => {
      for (let i = 0; i < 6; i++) await Promise.resolve()
    })
    unmount()
    await act(async () => {
      vi.advanceTimersByTime(5000)
    })
    expect(replace).not.toHaveBeenCalled()
  })
})

describe('OidcCallbackPage: unmount safety', () => {
  it('does not store a login for a response that lands after unmount', async () => {
    let resolveFn: (v: unknown) => void = () => {}
    httpGet.mockReturnValue(
      new Promise((res) => {
        resolveFn = res
      })
    )

    const { unmount } = mount()
    unmount()
    await act(async () => {
      resolveFn(session({ success: true, data: USER }))
      for (let i = 0; i < 4; i++) await Promise.resolve()
    })

    expect(window.localStorage.getItem('user')).toBeNull()
    expect(replace).not.toHaveBeenCalled()
    expect(toastSuccess).not.toHaveBeenCalled()
  })
})
