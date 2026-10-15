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
import { render } from '@testing-library/react'
import type { ReactElement } from 'react'
import type { Mock } from 'vitest'

import { currentUserQueryKey } from '@/lib/user'

import type { Tenant } from './types'

/** 1 USD = 500,000 quota units, shown as dollars. */
export const STATUS = { quota_per_unit: 500000, quota_display_type: 'USD' }

export const USER = {
  id: 7,
  role: 100,
  username: 'ops',
  quota: 0,
  used_quota: 0,
  remaining_quota: 0,
}

export function tenantFixture(over: Partial<Tenant> = {}): Tenant {
  return {
    id: 'tenant-uuid-1',
    zitadel_org_id: 'org-acme',
    slug: 'acme',
    name: 'Acme Corp',
    status: 1,
    plan_type: 'free',
    max_users: 100,
    max_quota: 1_000_000,
    rate_limit_rpm: 0,
    rate_limit_tpm: 0,
    wallet_authoritative: false,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...over,
  }
}

export function listPayload(tenants: Tenant[], total = tenants.length) {
  return { tenants, total, page: 1, page_size: 100 }
}

export function makeClient(): QueryClient {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  // The shell has already loaded the signed-in user by the time a page opens.
  client.setQueryData(currentUserQueryKey, USER)
  return client
}

export function renderWithClient(
  ui: ReactElement,
  client: QueryClient = makeClient()
) {
  return {
    client,
    ...render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>),
  }
}

export type RouteConfig = { params?: Record<string, unknown> } | undefined
export type RouteHandler = unknown | ((config?: RouteConfig) => unknown)
export type Routes = Record<string, RouteHandler>

/**
 * Answer GET requests by URL. A value resolves, an Error rejects, and a
 * function is called per request. The returned object stays live: assign a
 * route later to change what the next request sees.
 */
export function serveGet(
  apiGet: Mock,
  tenantGet: Mock,
  routes: Routes
): Routes {
  apiGet.mockImplementation((url: string, config?: RouteConfig) => {
    if (url === '/api/status') return Promise.resolve(STATUS)
    if (!(url in routes)) {
      return Promise.reject(new Error(`unexpected GET ${url}`))
    }
    const handler = routes[url]
    const value =
      typeof handler === 'function'
        ? (handler as (c?: RouteConfig) => unknown)(config)
        : handler
    return value instanceof Error
      ? Promise.reject(value)
      : Promise.resolve(value)
  })
  tenantGet.mockImplementation((path: string) =>
    path === '/user/me'
      ? Promise.resolve(USER)
      : Promise.reject(new Error(`unexpected tenant GET ${path}`))
  )
  return routes
}
