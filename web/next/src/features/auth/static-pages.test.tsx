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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { AccountDisabledPage, ForbiddenPage } from './index'

const { assign, statusGet } = vi.hoisted(() => ({
  assign: vi.fn(),
  statusGet: vi.fn(),
}))

vi.mock('./lib/navigate', () => ({
  navigation: { replace: vi.fn(), assign, reload: vi.fn() },
}))

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>()
  return { ...actual, api: { get: statusGet, post: vi.fn() } }
})

function wrap(node: React.ReactElement) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>)
}

beforeEach(() => {
  statusGet.mockResolvedValue({ system_name: 'Acme Gateway', logo: '/acme.png' })
})

describe('AccountDisabledPage', () => {
  it('renders both ways out and no navigation shell', () => {
    wrap(<AccountDisabledPage />)
    expect(screen.getByTestId('account-disabled')).toBeInTheDocument()
    expect(screen.getByText('Account disabled')).toBeInTheDocument()
    expect(
      screen.getByText(/Your account is currently disabled/)
    ).toBeInTheDocument()
    // A suspended account must not render navigation or a tenant switcher.
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
    expect(screen.queryByTestId('hf-shell')).not.toBeInTheDocument()
  })

  it('offers a mail link to the administrator', () => {
    wrap(<AccountDisabledPage />)
    expect(
      screen.getByRole('link', { name: 'Contact administrator' })
    ).toHaveAttribute('href', 'mailto:support@lurus.cn')
  })

  it('switches account through a full navigation to the logout endpoint', async () => {
    const user = userEvent.setup()
    wrap(<AccountDisabledPage />)
    await user.click(screen.getByRole('button', { name: 'Switch account' }))
    expect(assign).toHaveBeenCalledWith('/api/v2/auth/zita-logout')
  })
})

describe('ForbiddenPage', () => {
  it('explains the refusal and offers a way home', () => {
    wrap(<ForbiddenPage />)
    expect(screen.getByTestId('forbidden')).toBeInTheDocument()
    expect(
      screen.getByText(
        'You do not have permission to access this page. Please contact the administrator.'
      )
    ).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to Home' })).toHaveAttribute(
      'href',
      '/'
    )
  })

  it('goes back in history', async () => {
    const back = vi.spyOn(window.history, 'back').mockImplementation(() => {})
    const user = userEvent.setup()
    wrap(<ForbiddenPage />)
    await user.click(screen.getByRole('button', { name: 'Go Back' }))
    expect(back).toHaveBeenCalledTimes(1)
  })
})

describe('branded frame', () => {
  it('shows the system name and logo from /api/status', async () => {
    wrap(<ForbiddenPage />)
    await waitFor(() =>
      expect(screen.getByTestId('auth-brand-name')).toHaveTextContent(
        'Acme Gateway'
      )
    )
    expect(document.querySelector('img')).toHaveAttribute('src', '/acme.png')
  })

  it('falls back to the product brand while /api/status has not answered', () => {
    statusGet.mockReturnValue(new Promise(() => {}))
    wrap(<ForbiddenPage />)
    expect(screen.getByTestId('auth-brand-name')).toHaveTextContent('Lurus')
  })

  it('keeps working when /api/status is unreachable', async () => {
    statusGet.mockRejectedValue(new Error('offline'))
    wrap(<AccountDisabledPage />)
    await waitFor(() => expect(statusGet).toHaveBeenCalled())
    expect(screen.getByTestId('auth-brand-name')).toHaveTextContent('Lurus')
    expect(screen.getByTestId('account-disabled')).toBeInTheDocument()
  })

  it('ignores a blank system name', async () => {
    statusGet.mockResolvedValue({ system_name: '   ', logo: '' })
    wrap(<ForbiddenPage />)
    await waitFor(() => expect(statusGet).toHaveBeenCalled())
    expect(screen.getByTestId('auth-brand-name')).toHaveTextContent('Lurus')
    expect(document.querySelector('img')).toHaveAttribute('src', '/next/logo.png')
  })
})
