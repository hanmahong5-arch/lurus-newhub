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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { tenantApi } from '@/lib/api'

import { RankingsPanel } from './rankings-panel'

vi.mock('@/lib/api', () => ({ tenantApi: { get: vi.fn() } }))

const get = vi.mocked(tenantApi.get)

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <RankingsPanel />
    </QueryClientProvider>
  )
}

const row = {
  name: 'rerank',
  rank: 1,
  requests: 12,
  total_tokens: 0,
  quota: 5,
  token_share_pct: 100,
  quota_share_pct: 100,
}

describe('RankingsPanel', () => {
  beforeEach(() => {
    get.mockReset()
    get.mockResolvedValue({ rows: [row] })
  })

  it('offers relay_mode and usage_unit and requests the chosen dimension', async () => {
    renderPanel()
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith('/analytics/rankings', {
        params: { by: 'model', hours: 168 },
      })
    )
    fireEvent.click(screen.getByTestId('rankings-by-relay_mode'))
    await waitFor(() =>
      expect(get).toHaveBeenLastCalledWith('/analytics/rankings', {
        params: { by: 'relay_mode', hours: 168 },
      })
    )
    fireEvent.click(screen.getByTestId('rankings-by-usage_unit'))
    await waitFor(() =>
      expect(get).toHaveBeenLastCalledWith('/analytics/rankings', {
        params: { by: 'usage_unit', hours: 168 },
      })
    )
    expect(await screen.findByText('rerank')).toBeTruthy()
  })

  it('never offers the platform-only vendor dimension', () => {
    renderPanel()
    expect(screen.queryByTestId('rankings-by-vendor')).toBeNull()
  })

  it('shows a failure, not an empty list, when the read fails', async () => {
    get.mockRejectedValue(new Error('boom'))
    renderPanel()
    expect(await screen.findByTestId('dashboard-load-failed')).toBeTruthy()
  })
})
