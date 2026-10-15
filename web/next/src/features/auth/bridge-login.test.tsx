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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'

import { BridgeLoginPage } from './bridge-login'

const { apiPost, apiGet, replace } = vi.hoisted(() => ({
  apiPost: vi.fn(),
  apiGet: vi.fn(),
  replace: vi.fn(),
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return { ...actual, api: { get: apiGet, post: apiPost } }
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

const bridgeStatus = (enabled: boolean) => ({
  login_methods: { bridge: { enabled } },
})

const okUser = { id: 1, username: 'root', role: 100, tenant_slug: 'lurus' }

function openAt(path: string) {
  window.history.replaceState(null, '', path)
}

beforeEach(() => {
  window.localStorage.clear()
})

afterEach(() => {
  window.history.replaceState(null, '', '/')
})

describe('BridgeLoginPage', () => {
  it('a link that carries a token signs in with no further interaction', async () => {
    openAt('/bridge-login#t=probe-token&u=1')
    apiGet.mockResolvedValue(bridgeStatus(true))
    apiPost.mockResolvedValue(okUser)

    render(<BridgeLoginPage />)

    await waitFor(() => expect(apiPost).toHaveBeenCalled())
    expect(apiPost.mock.calls[0][0]).toBe(
      '/api/v2/bridge/exchange?token=probe-token&user_id=1'
    )
    expect(apiPost.mock.calls[0][2]).toEqual({ skipAuthRedirect: true })
    await waitFor(() =>
      expect(replace).toHaveBeenCalledWith(`${ORIGIN}/dashboard`)
    )
    expect(
      JSON.parse(window.localStorage.getItem('user') ?? 'null').username
    ).toBe('root')
    // The slug comes from the response, never from a literal in this file.
    expect(window.localStorage.getItem('tenant_slug')).toBe('lurus')
    // The credential must not survive in the address bar.
    expect(window.location.hash).toBe('')
    expect(window.location.pathname).toBe('/bridge-login')
  })

  it('takes the user id from the fragment when it carries one', async () => {
    openAt('/bridge-login#t=probe-token&u=7')
    apiGet.mockResolvedValue(bridgeStatus(true))
    apiPost.mockResolvedValue({ ...okUser, id: 7 })

    render(<BridgeLoginPage />)

    await waitFor(() => expect(apiPost).toHaveBeenCalled())
    expect(apiPost.mock.calls[0][0]).toBe(
      '/api/v2/bridge/exchange?token=probe-token&user_id=7'
    )
  })

  it('url-encodes the token it sends', async () => {
    openAt('/bridge-login#t=a%26b%3Dc')
    apiGet.mockResolvedValue(bridgeStatus(true))
    apiPost.mockResolvedValue(okUser)

    render(<BridgeLoginPage />)

    await waitFor(() => expect(apiPost).toHaveBeenCalled())
    expect(apiPost.mock.calls[0][0]).toBe(
      '/api/v2/bridge/exchange?token=a%26b%3Dc&user_id=1'
    )
  })

  it('lands on a safe ?redirect= and resolves it before the URL is rewritten', async () => {
    openAt('/bridge-login?redirect=%2Fkeys#t=probe-token')
    apiGet.mockResolvedValue(bridgeStatus(true))
    apiPost.mockResolvedValue(okUser)

    render(<BridgeLoginPage />)

    await waitFor(() => expect(replace).toHaveBeenCalledWith(`${ORIGIN}/keys`))
  })

  it('does not store a tenant slug the server could not name', async () => {
    openAt('/bridge-login#t=probe-token')
    apiGet.mockResolvedValue(bridgeStatus(true))
    apiPost.mockResolvedValue({ id: 1, username: 'root', tenant_slug: '' })

    render(<BridgeLoginPage />)

    await waitFor(() => expect(replace).toHaveBeenCalled())
    expect(window.localStorage.getItem('tenant_slug')).toBeNull()
  })

  it('says so and never calls the exchange where the instance has no bridge route', async () => {
    openAt('/bridge-login#t=probe-token')
    apiGet.mockResolvedValue(bridgeStatus(false))

    render(<BridgeLoginPage />)

    expect(
      await screen.findByText(
        'Acceptance sign-in is not available on this instance'
      )
    ).toBeInTheDocument()
    expect(apiPost).not.toHaveBeenCalled()
    expect(replace).not.toHaveBeenCalled()
  })

  it('treats a status payload with no bridge entry as unavailable', async () => {
    openAt('/bridge-login#t=probe-token')
    apiGet.mockResolvedValue({ login_methods: {} })

    render(<BridgeLoginPage />)

    expect(
      await screen.findByText(
        'Acceptance sign-in is not available on this instance'
      )
    ).toBeInTheDocument()
    expect(apiPost).not.toHaveBeenCalled()
  })

  it('lets the exchange answer when the status endpoint cannot be reached', async () => {
    openAt('/bridge-login#t=probe-token')
    apiGet.mockRejectedValue(new Error('offline'))
    apiPost.mockResolvedValue(okUser)

    render(<BridgeLoginPage />)

    await waitFor(() => expect(replace).toHaveBeenCalled())
    expect(apiPost).toHaveBeenCalledTimes(1)
  })

  it('reports a rejected token instead of navigating', async () => {
    openAt('/bridge-login#t=wrong-token')
    apiGet.mockResolvedValue(bridgeStatus(true))
    apiPost.mockRejectedValue(new ApiError('invalid bridge token', { status: 403 }))

    render(<BridgeLoginPage />)

    expect((await screen.findByTestId('bridge-login-error')).textContent).toBe(
      'That sign-in token was rejected'
    )
    expect(replace).not.toHaveBeenCalled()
    expect(window.localStorage.getItem('user')).toBeNull()
  })

  it('tells a rate-limited visitor to wait rather than blaming the token', async () => {
    openAt('/bridge-login#t=probe-token')
    apiGet.mockResolvedValue(bridgeStatus(true))
    apiPost.mockRejectedValue(new ApiError('Too many requests', { status: 429 }))

    render(<BridgeLoginPage />)

    expect((await screen.findByTestId('bridge-login-error')).textContent).toBe(
      'Too many attempts. Wait a minute before trying again.'
    )
    expect(replace).not.toHaveBeenCalled()
  })

  it('reports any other failure generically and keeps the form usable', async () => {
    openAt('/bridge-login#t=probe-token')
    apiGet.mockResolvedValue(bridgeStatus(true))
    apiPost.mockRejectedValue(new ApiError('boom', { status: 500 }))

    render(<BridgeLoginPage />)

    expect((await screen.findByTestId('bridge-login-error')).textContent).toBe(
      'Login failed, please try again'
    )
    expect(screen.getByTestId('bridge-login-token')).toBeInTheDocument()
  })

  it('treats an exchange that returns no user as a failure', async () => {
    openAt('/bridge-login#t=probe-token')
    apiGet.mockResolvedValue(bridgeStatus(true))
    apiPost.mockResolvedValue({})

    render(<BridgeLoginPage />)

    expect((await screen.findByTestId('bridge-login-error')).textContent).toBe(
      'Login failed, please try again'
    )
    expect(replace).not.toHaveBeenCalled()
    expect(window.localStorage.getItem('user')).toBeNull()
  })

  it('offers the token field when the link carries none, and signs in on submit', async () => {
    openAt('/bridge-login')
    apiGet.mockResolvedValue(bridgeStatus(true))
    apiPost.mockResolvedValue(okUser)
    const user = userEvent.setup()

    render(<BridgeLoginPage />)

    const field = await screen.findByTestId('bridge-login-token')
    expect(apiPost).not.toHaveBeenCalled()
    // The submit button stays off until there is something to send.
    expect(screen.getByRole('button', { name: 'Open the console' })).toBeDisabled()

    await user.type(field, '  typed-token  ')
    await user.click(screen.getByRole('button', { name: 'Open the console' }))

    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1))
    // Surrounding whitespace never reaches the server.
    expect(apiPost.mock.calls[0][0]).toBe(
      '/api/v2/bridge/exchange?token=typed-token&user_id=1'
    )
    await waitFor(() => expect(replace).toHaveBeenCalled())
  })

  it('keeps the pasted token masked', async () => {
    openAt('/bridge-login')
    apiGet.mockResolvedValue(bridgeStatus(true))

    render(<BridgeLoginPage />)

    expect(await screen.findByTestId('bridge-login-token')).toHaveAttribute(
      'type',
      'password'
    )
  })

  it('can retry after a refused token', async () => {
    openAt('/bridge-login#t=wrong-token')
    apiGet.mockResolvedValue(bridgeStatus(true))
    apiPost.mockRejectedValueOnce(new ApiError('nope', { status: 403 }))
    apiPost.mockResolvedValueOnce(okUser)
    const user = userEvent.setup()

    render(<BridgeLoginPage />)
    const field = await screen.findByTestId('bridge-login-token')
    await screen.findByTestId('bridge-login-error')

    await user.clear(field)
    await user.type(field, 'right-token')
    await user.click(screen.getByRole('button', { name: 'Open the console' }))

    await waitFor(() => expect(replace).toHaveBeenCalled())
    expect(apiPost.mock.calls[1][0]).toBe(
      '/api/v2/bridge/exchange?token=right-token&user_id=1'
    )
  })
})
