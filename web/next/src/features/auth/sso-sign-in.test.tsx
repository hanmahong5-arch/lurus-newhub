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
import { act, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { inviteLink } from './lib/invite'
import { LoginPage, RegisterPage } from './sso-sign-in'

const { apiPost, apiGet, replace, assign } = vi.hoisted(() => ({
  apiPost: vi.fn(),
  apiGet: vi.fn(),
  replace: vi.fn(),
  assign: vi.fn(),
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return { ...actual, api: { get: apiGet, post: apiPost } }
})

vi.mock('./lib/navigate', () => ({
  navigation: { replace, assign, reload: vi.fn() },
}))

// The brand header reads /api/status through the shared query; this suite is
// about the flow, so the frame is a plain wrapper.
vi.mock('./components/auth-layout', () => ({
  AuthLayout: (props: { children: React.ReactNode }) => (
    <div data-testid='layout'>{props.children}</div>
  ),
}))

const ORIGIN = window.location.origin

/** What /api/status says this deployment can sign someone in with. */
function capability(sso?: boolean, bridge?: boolean) {
  const login_methods: Record<string, unknown> = {}
  if (sso !== undefined) login_methods.sso = { enabled: sso }
  if (bridge !== undefined) login_methods.bridge = { enabled: bridge }
  return { login_methods }
}

async function flush() {
  await act(async () => {
    for (let i = 0; i < 8; i++) await Promise.resolve()
  })
}

function mount(node: React.ReactElement = <LoginPage />) {
  return render(node)
}

function loginUrl(returnTo: string) {
  return `/api/v2/auth/zita-login?return_to=${encodeURIComponent(returnTo)}`
}

beforeEach(() => {
  window.localStorage.clear()
  window.history.replaceState(null, '', '/login')
  apiGet.mockResolvedValue(capability(true, false))
})

afterEach(() => {
  vi.useRealTimers()
  window.history.replaceState(null, '', '/')
})

describe('sign-in flow: session bridge', () => {
  it('shows the progress card while the bridge call is in flight', () => {
    apiPost.mockReturnValue(new Promise(() => {}))
    mount()
    expect(screen.getByTestId('sso-progress')).toBeInTheDocument()
    // The slow-redirect copy must NOT be visible immediately.
    expect(
      screen.queryByText(/Redirecting to single sign-on/)
    ).not.toBeInTheDocument()
  })

  it('calls the bridge with the sign-in redirect suppressed', async () => {
    apiPost.mockRejectedValue(new ApiError('nope', { status: 401 }))
    mount()
    await flush()
    expect(apiPost).toHaveBeenCalledWith(
      '/api/v2/auth/zita-bootstrap',
      {},
      { skipAuthRedirect: true }
    )
  })

  it('forwards ?invite= to the bootstrap POST and carries it in return_to', async () => {
    window.history.replaceState(null, '', '/login?invite=abc123XYZ_-')
    apiPost.mockRejectedValue(new ApiError('nope', { status: 401 }))
    mount()
    await flush()

    expect(apiPost).toHaveBeenCalledWith(
      '/api/v2/auth/zita-bootstrap?invite=abc123XYZ_-',
      {},
      { skipAuthRedirect: true }
    )
    expect(assign).toHaveBeenCalledTimes(1)
    // The return_to must be exactly what the issuing side builds for the same
    // code: both go through inviteLink.
    expect(assign).toHaveBeenCalledWith(
      loginUrl(inviteLink(ORIGIN, 'abc123XYZ_-'))
    )
  })

  it('ignores a malformed invite code: bare call, dashboard return_to', async () => {
    window.history.replaceState(
      null,
      '',
      `/login?invite=${encodeURIComponent('not valid!')}`
    )
    apiPost.mockRejectedValue(new ApiError('nope', { status: 401 }))
    mount()
    await flush()

    expect(apiPost).toHaveBeenCalledWith(
      '/api/v2/auth/zita-bootstrap',
      {},
      { skipAuthRedirect: true }
    )
    expect(assign).toHaveBeenCalledWith(loginUrl(`${ORIGIN}/dashboard`))
  })

  it('stores the bridged user, persists the tenant slug and lands on the dashboard', async () => {
    apiPost.mockResolvedValue({ id: 42, username: 'bob', tenant_slug: 'acme' })
    mount()
    await flush()

    expect(JSON.parse(window.localStorage.getItem('user') ?? 'null')).toEqual({
      id: 42,
      username: 'bob',
      tenant_slug: 'acme',
    })
    expect(window.localStorage.getItem('tenant_slug')).toBe('acme')
    expect(replace).toHaveBeenCalledWith(`${ORIGIN}/dashboard`)
    // Having succeeded, it must not also kick off the identity login redirect.
    expect(assign).not.toHaveBeenCalled()
  })

  it('honours a safe ?redirect= after signing in', async () => {
    window.history.replaceState(null, '', '/login?redirect=%2Fkeys%3Fp%3D2')
    apiPost.mockResolvedValue({ id: 42, username: 'bob' })
    mount()
    await flush()
    expect(replace).toHaveBeenCalledWith(`${ORIGIN}/keys?p=2`)
  })

  it('refuses an off-site ?redirect= and lands on the dashboard', async () => {
    window.history.replaceState(
      null,
      '',
      '/login?redirect=https%3A%2F%2Fevil.example%2F'
    )
    apiPost.mockResolvedValue({ id: 42, username: 'bob' })
    mount()
    await flush()
    expect(replace).toHaveBeenCalledWith(`${ORIGIN}/dashboard`)
  })

  it('does not write a tenant slug when the bridge omits one', async () => {
    apiPost.mockResolvedValue({ id: 7 })
    mount()
    await flush()
    expect(window.localStorage.getItem('tenant_slug')).toBeNull()
    expect(replace).toHaveBeenCalledWith(`${ORIGIN}/dashboard`)
  })

  it('treats a success envelope with no user id as "not signed in" and goes to identity login', async () => {
    apiPost.mockResolvedValue({})
    mount()
    await flush()

    expect(window.localStorage.getItem('user')).toBeNull()
    expect(replace).not.toHaveBeenCalled()
    expect(assign).toHaveBeenCalledWith(loginUrl(`${ORIGIN}/dashboard`))
  })

  it('treats a business failure on a 200 as "not signed in" too', async () => {
    apiPost.mockRejectedValue(new ApiError('no cookie', { status: 200 }))
    mount()
    await flush()
    expect(window.localStorage.getItem('user')).toBeNull()
    expect(assign).toHaveBeenCalledTimes(1)
  })

  it('sends an unauthenticated visitor to identity login rather than looping', async () => {
    apiPost.mockRejectedValue(new ApiError('nope', { status: 401 }))
    mount()
    await flush()

    expect(assign).toHaveBeenCalledTimes(1)
    expect(assign.mock.calls[0][0]).toContain(
      '/api/v2/auth/zita-login?return_to='
    )
    expect(replace).not.toHaveBeenCalled()
  })

  it('sends a network failure to identity login too (no user cached)', async () => {
    apiPost.mockRejectedValue(new ApiError('offline'))
    mount()
    await flush()

    expect(window.localStorage.getItem('user')).toBeNull()
    expect(assign).toHaveBeenCalledTimes(1)
  })
})

describe('sign-in flow: instances without single sign-on', () => {
  // The isolated acceptance instance runs with sign-on off on purpose, and
  // /api/v2/auth/zita-login answers 503 there. Navigating anyway left the
  // browser on a raw JSON error with no way back.
  it('stops instead of redirecting where the instance reports no single sign-on', async () => {
    apiPost.mockRejectedValue(new ApiError('nope', { status: 401 }))
    apiGet.mockResolvedValue(capability(false, true))
    mount()
    await flush()

    expect(assign).not.toHaveBeenCalled()
    expect(
      screen.getByText('Single sign-on is not enabled on this instance')
    ).toBeInTheDocument()
    const link = screen.getByRole('link', {
      name: 'Go to acceptance sign-in',
    })
    expect(link).toHaveAttribute('href', '/bridge-login')
  })

  it('carries a safe ?redirect= over to the acceptance sign-in', async () => {
    window.history.replaceState(null, '', '/login?redirect=%2Fkeys')
    apiPost.mockRejectedValue(new ApiError('nope', { status: 401 }))
    apiGet.mockResolvedValue(capability(false, true))
    mount()
    await flush()
    expect(
      screen.getByRole('link', { name: 'Go to acceptance sign-in' })
    ).toHaveAttribute('href', '/bridge-login?redirect=%2Fkeys')
  })

  it('omits the acceptance route where that instance has no bridge either', async () => {
    apiPost.mockRejectedValue(new ApiError('nope', { status: 401 }))
    apiGet.mockResolvedValue(capability(false, false))
    mount()
    await flush()

    expect(assign).not.toHaveBeenCalled()
    expect(
      screen.getByText('Single sign-on is not enabled on this instance')
    ).toBeInTheDocument()
    expect(
      screen.queryByText('Go to acceptance sign-in')
    ).not.toBeInTheDocument()
  })

  it('redirects as usual when the payload carries no sso entry at all', async () => {
    apiPost.mockRejectedValue(new ApiError('nope', { status: 401 }))
    apiGet.mockResolvedValue(capability())
    mount()
    await flush()

    expect(assign).toHaveBeenCalledTimes(1)
    expect(assign.mock.calls[0][0]).toContain(
      '/api/v2/auth/zita-login?return_to='
    )
  })

  // A status endpoint that cannot be reached must not become a third outcome:
  // the redirect is still the only path worth trying.
  it('redirects as usual when the capability probe itself fails', async () => {
    apiPost.mockRejectedValue(new ApiError('nope', { status: 401 }))
    apiGet.mockRejectedValue(new Error('offline'))
    mount()
    await flush()
    expect(assign).toHaveBeenCalledTimes(1)
  })

  it('asks /api/status without bouncing to sign-in on a 401', async () => {
    apiPost.mockRejectedValue(new ApiError('nope', { status: 401 }))
    mount()
    await flush()
    expect(apiGet).toHaveBeenCalledWith('/api/status', {
      skipAuthRedirect: true,
    })
  })
})

describe('sign-in flow: terminal refusals', () => {
  it('parks a disabled account on the account-disabled page instead of retrying login forever', async () => {
    apiPost.mockRejectedValue(new ApiError('disabled', { status: 403 }))
    mount()
    await flush()

    expect(replace).toHaveBeenCalledWith(`${ORIGIN}/account-disabled`)
    // Crucially it must NOT also start the identity-login redirect, which is
    // what would produce the bounce loop this branch exists to prevent.
    expect(assign).not.toHaveBeenCalled()
    expect(window.localStorage.getItem('user')).toBeNull()
  })

  // A 403 the person can act on must not be dressed up as "your account is
  // disabled": the remedy is an administrator freeing a seat or the right
  // organisation's sign-in link, and the account-disabled page offers neither.
  it.each([
    [
      'TENANT_SEAT_LIMIT',
      'This tenant has reached its user limit. Ask an administrator to free a seat or raise the limit, then sign in again.',
    ],
    [
      'TENANT_ORG_MISMATCH',
      "This account belongs to a different organisation than the tenant you are signing in to. Use your own organisation's sign-in link, or ask an administrator.",
    ],
  ])('parks a %s refusal on its own message', async (code, message) => {
    apiPost.mockRejectedValue(
      new ApiError('refused', { status: 403, code })
    )
    mount()
    await flush()

    expect(screen.getByText(message)).toBeInTheDocument()
    // Neither the account-disabled page nor the identity-login loop.
    expect(replace).not.toHaveBeenCalled()
    expect(assign).not.toHaveBeenCalled()
  })

  it('treats an unrecognised 403 code as a disabled account', async () => {
    apiPost.mockRejectedValue(
      new ApiError('refused', { status: 403, code: 'SOMETHING_ELSE' })
    )
    mount()
    await flush()
    expect(replace).toHaveBeenCalledWith(`${ORIGIN}/account-disabled`)
  })
})

describe('sign-in flow: timing and lifecycle', () => {
  it('reveals the "still redirecting" copy and a manual link only after the 3s grace period', async () => {
    vi.useFakeTimers()
    apiPost.mockRejectedValue(new ApiError('nope', { status: 401 }))
    mount()
    await act(async () => {
      for (let i = 0; i < 8; i++) await Promise.resolve()
    })
    expect(assign).toHaveBeenCalledTimes(1)
    expect(
      screen.queryByText(/Redirecting to single sign-on/)
    ).not.toBeInTheDocument()

    await act(async () => {
      vi.advanceTimersByTime(2999)
    })
    expect(
      screen.queryByText(/Redirecting to single sign-on/)
    ).not.toBeInTheDocument()

    await act(async () => {
      vi.advanceTimersByTime(1)
    })
    expect(
      screen.getByText('Redirecting to single sign-on, please wait...')
    ).toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: 'Continue to sign-in' })
    ).toHaveAttribute('href', loginUrl(`${ORIGIN}/dashboard`))
  })

  it('does not navigate after the component has been unmounted mid-flight', async () => {
    let resolvePost: (v: unknown) => void = () => {}
    apiPost.mockReturnValue(
      new Promise((r) => {
        resolvePost = r
      })
    )
    const { unmount } = mount()
    unmount()
    await act(async () => {
      resolvePost({ id: 9, tenant_slug: 't' })
      for (let i = 0; i < 4; i++) await Promise.resolve()
    })

    expect(replace).not.toHaveBeenCalled()
    expect(assign).not.toHaveBeenCalled()
    // No account may be left behind by a response that arrived after leaving.
    expect(window.localStorage.getItem('user')).toBeNull()
    expect(window.localStorage.getItem('tenant_slug')).toBeNull()
  })

  it('does not start the identity redirect when unmounted while the capability probe runs', async () => {
    let resolveStatus: (v: unknown) => void = () => {}
    apiPost.mockRejectedValue(new ApiError('nope', { status: 401 }))
    apiGet.mockReturnValue(
      new Promise((r) => {
        resolveStatus = r
      })
    )
    const { unmount } = mount()
    await flush()
    unmount()
    await act(async () => {
      resolveStatus(capability(true, false))
      for (let i = 0; i < 4; i++) await Promise.resolve()
    })
    expect(assign).not.toHaveBeenCalled()
  })
})

describe('sign-in routes', () => {
  it('skips the exchange for a visitor that already holds a session hint', async () => {
    window.localStorage.setItem('user', '{"id":5}')
    window.history.replaceState(null, '', '/login?redirect=%2Fmodels')
    mount()
    await flush()
    expect(apiPost).not.toHaveBeenCalled()
    expect(replace).toHaveBeenCalledWith(`${ORIGIN}/models`)
  })

  it('serves /login/:tenantSlug with the same flow, ignoring the slug', async () => {
    apiPost.mockRejectedValue(new ApiError('nope', { status: 401 }))
    mount(<LoginPage tenantSlug='acme' />)
    await flush()
    expect(apiPost).toHaveBeenCalledWith(
      '/api/v2/auth/zita-bootstrap',
      {},
      { skipAuthRedirect: true }
    )
    expect(assign).toHaveBeenCalledWith(loginUrl(`${ORIGIN}/dashboard`))
    // The slug is never sent anywhere.
    expect(JSON.stringify([apiPost.mock.calls, assign.mock.calls])).not.toContain(
      'acme'
    )
  })

  it('serves /register with the same flow: registration is the identity platform', async () => {
    apiPost.mockRejectedValue(new ApiError('nope', { status: 401 }))
    mount(<RegisterPage />)
    await flush()
    expect(assign).toHaveBeenCalledWith(loginUrl(`${ORIGIN}/dashboard`))
  })
})
