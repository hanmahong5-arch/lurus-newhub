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

export interface LogBody {
  requestBody: string
  responseText: string
  /** False when the reply text was not recorded (e.g. streamed replies). */
  responseCaptured: boolean
  /** A field was cut at the archive size limit. */
  truncated: boolean
}

/**
 * Archived text of one call. Rejects with ApiError status 404 when nothing
 * was archived (never consented, expired, or not stored for this call);
 * the server answers all those cases identically on purpose.
 */
export async function fetchLogBody(requestId: string): Promise<LogBody> {
  const raw = await tenantApi.get<Record<string, unknown>>(
    `/logs/${encodeURIComponent(requestId)}/body`
  )
  const str = (v: unknown) => (typeof v === 'string' ? v : '')
  return {
    requestBody: str(raw?.request_body),
    responseText: str(raw?.response_text),
    responseCaptured: raw?.response_captured === true,
    truncated: raw?.truncated === true,
  }
}
