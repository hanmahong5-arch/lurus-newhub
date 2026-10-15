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

const base = {
  rank: 1,
  total_tokens: 0,
  quota: 5,
  quota_share_pct: 0,
}
// The server ranks the nameless bucket first: it has the most traffic.
const nameless = { ...base, name: '', requests: 90, token_share_pct: 90 }
const named = { ...base, name: 'model-a', requests: 10, token_share_pct: 10 }

describe('RankingsPanel rows with no name', () => {
  beforeEach(() => {
    get.mockReset()
    get.mockResolvedValue({ rows: [nameless, named] })
  })

  it('labels a nameless model row "Unknown model" and lists it after named ones', async () => {
    renderPanel()
    const list = await screen.findByTestId('rankings-rows')
    const items = [...list.querySelectorAll('li')]
    expect(items).toHaveLength(2)
    expect(items[0]).toHaveTextContent('model-a')
    expect(items[1]).toHaveTextContent('Unknown model')
    // Its numbers are real and stay attached to it.
    expect(items[1]).toHaveTextContent('90 requests')
    expect(items[1]).toHaveTextContent('90.0%')
  })

  it('says "Unknown" (not "Unknown model") for another dimension', async () => {
    renderPanel()
    await screen.findByTestId('rankings-rows')
    fireEvent.click(screen.getByTestId('rankings-by-group'))
    await waitFor(() =>
      expect(get).toHaveBeenLastCalledWith('/analytics/rankings', {
        params: { by: 'group', hours: 168 },
      })
    )
    await waitFor(() => {
      const items = [...screen.getByTestId('rankings-rows').querySelectorAll('li')]
      expect(items.at(-1)?.textContent).toContain('Unknown')
    })
    expect(screen.getByTestId('rankings-rows')).not.toHaveTextContent(
      'Unknown model'
    )
  })
})
