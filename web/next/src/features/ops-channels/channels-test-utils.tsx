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

import type { Channel } from './types'

/** A v1 channel row as GET /api/v2/admin/channels/ returns it (key column omitted). */
export function channelRow(
  id: number,
  over: Partial<Channel> & Record<string, unknown> = {}
): Channel {
  return {
    id,
    type: 1,
    key: '',
    status: 1,
    name: `channel-${id}`,
    weight: 0,
    created_time: 1_700_000_000,
    test_time: 0,
    response_time: 0,
    base_url: '',
    other: '',
    balance: 0,
    balance_updated_time: 0,
    models: 'model-a,model-b',
    group: 'default',
    used_quota: 0,
    model_mapping: null,
    status_code_mapping: null,
    priority: 0,
    auto_ban: 1,
    other_info: '',
    tag: null,
    setting: null,
    param_override: null,
    header_override: null,
    remark: null,
    channel_info: {
      is_multi_key: false,
      multi_key_size: 0,
      multi_key_polling_index: 0,
      multi_key_mode: 'random',
    },
    settings: '',
    ...over,
  }
}

/** The v1 list envelope. */
export function listBody(items: Channel[], counts?: Record<string, number>) {
  const typeCounts: Record<string, number> = counts ?? {}
  if (counts === undefined) {
    for (const c of items) {
      typeCounts[String(c.type)] = (typeCounts[String(c.type)] ?? 0) + 1
    }
  }
  return {
    success: true,
    message: '',
    data: {
      items,
      total: items.length,
      page: 1,
      page_size: 20,
      type_counts: typeCounts,
    },
  }
}

export function renderWithClient(ui: ReactElement) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>
  )
}
