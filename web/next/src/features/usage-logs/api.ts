import { tenantApi } from '@/lib/api'

import {
  buildFilterParams,
  buildListParams,
  type LogFilters,
  type LogListData,
  type LogStat,
} from './lib/logs'

/** `/logs` for the caller's own rows, `/logs/all` for the tenant-wide view. */
export function fetchLogs(f: LogFilters, page: number): Promise<LogListData> {
  const route = f.tenantWide ? '/logs/all' : '/logs'
  return tenantApi.get<LogListData>(
    `${route}?${buildListParams(f, page).toString()}`
  )
}

export function fetchLogStat(f: LogFilters): Promise<LogStat> {
  const route = f.tenantWide ? '/logs/stat/all' : '/logs/stat'
  return tenantApi.get<LogStat>(`${route}?${buildFilterParams(f).toString()}`)
}
