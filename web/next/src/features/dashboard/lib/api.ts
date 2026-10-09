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
import { http, tenantApi, unwrap } from '@/lib/api'

import {
  LOG_PAGE_SIZE,
  REALTIME_WINDOW_SECONDS,
  TREND_WINDOW_SECONDS,
} from './kpis'
import type { LogRow, QuotaRow, RealtimeLogs, TrendData } from './types'

/**
 * Last 5 minutes of the tenant's own logs. GET /logs answers
 * `{logs,total,page,page_size}`.
 */
export async function fetchRealtimeLogs(
  nowSec = Math.floor(Date.now() / 1000)
): Promise<RealtimeLogs> {
  const body = await tenantApi.get<{ logs?: unknown; total?: unknown } | null>(
    '/logs',
    {
      params: {
        page: 1,
        page_size: LOG_PAGE_SIZE,
        start_time: nowSec - REALTIME_WINDOW_SECONDS,
      },
    }
  )
  const logs = Array.isArray(body?.logs) ? (body.logs as LogRow[]) : []
  const total = typeof body?.total === 'number' ? body.total : null
  return { logs, total }
}

/**
 * Per (user, model, hour) quota rows for the trailing window. A failed read
 * throws; an empty array means "nothing recorded".
 */
export async function fetchTrend(
  nowSec = Math.floor(Date.now() / 1000)
): Promise<TrendData> {
  const start = nowSec - TREND_WINDOW_SECONDS
  const res = await http.get<unknown>('/api/data/self/', {
    params: { start_timestamp: start, end_timestamp: nowSec },
  })
  const rows = unwrap<QuotaRow[] | null>(res.data, res.status)
  return { rows: Array.isArray(rows) ? rows : [], start, end: nowSec }
}
