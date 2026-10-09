import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { currentUserQueryKey, type CurrentUser } from '@/lib/user'

import { RequireAccess } from './require-access'

vi.mock('@/features/errors/forbidden', () => ({
  ForbiddenError: (p: { embedded?: boolean }) => (
    <div data-embedded={String(!!p.embedded)}>403 Forbidden</div>
  ),
}))

function renderGuard(
  requires: Parameters<typeof RequireAccess>[0]['requires'],
  user: Partial<CurrentUser> | undefined
) {
  const client = new QueryClient()
  if (user) client.setQueryData(currentUserQueryKey, { role: 1, ...user })
  return render(
    <QueryClientProvider client={client}>
      <RequireAccess requires={requires}>
        <div>secret page</div>
      </RequireAccess>
    </QueryClientProvider>
  )
}

describe('RequireAccess', () => {
  it('shows 403 in place for a plain member on a tenant-admin route', () => {
    renderGuard('tenantAdmin', { tenant_role: '' })
    expect(screen.getByText('403 Forbidden')).toBeTruthy()
    expect(screen.queryByText('secret page')).toBeNull()
  })

  it('renders the embedded 403 variant (no full-viewport height)', () => {
    renderGuard('tenantAdmin', { tenant_role: '' })
    expect(
      screen.getByText('403 Forbidden').getAttribute('data-embedded')
    ).toBe('true')
  })

  it('shows 403 to a tenant admin on a platform route', () => {
    renderGuard('platformStaff', { tenant_role: 'admin' })
    expect(screen.getByText('403 Forbidden')).toBeTruthy()
  })

  it('lets a department lead into lead routes', () => {
    renderGuard('orgLead', { tenant_role: 'dept_lead' })
    expect(screen.getByText('secret page')).toBeTruthy()
  })

  it('shows 403 to a department lead on an admin-only route', () => {
    renderGuard('tenantAdmin', { tenant_role: 'dept_lead' })
    expect(screen.getByText('403 Forbidden')).toBeTruthy()
  })

  it('lets platform staff through everywhere', () => {
    renderGuard('platformStaff', { role: 10 })
    expect(screen.getByText('secret page')).toBeTruthy()
  })

  it('renders nothing (not a 403) while the user is unresolved', () => {
    const { container } = renderGuard('tenantAdmin', undefined)
    expect(container.textContent).toBe('')
  })
})
