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
import { isAxiosError } from 'axios'

import { ApiError, http, tenantApi, tenantUrl, toApiError } from '@/lib/api'

import {
  PAGE_SIZE,
  buildFilterParams,
  buildListParams,
  type LogFilters,
  type LogListData,
  type LogStat,
} from './lib/logs'

/** `/logs` for the caller's own rows, `/logs/all` for the tenant-wide view. */
export function fetchLogs(
  f: LogFilters,
  page: number,
  pageSize: number = PAGE_SIZE
): Promise<LogListData> {
  const route = f.tenantWide ? '/logs/all' : '/logs'
  return tenantApi.get<LogListData>(
    `${route}?${buildListParams(f, page, pageSize).toString()}`
  )
}

/** Server cap of GET /logs/export (rows); larger requests are clamped. */
export const EXPORT_MAX_ROWS = 50_000

/**
 * CSV of the rows the filters select (the export honours the same model,
 * token, type and time filters as the list; it has no request-id filter).
 * The body is the file; failures come back as the usual JSON envelope.
 */
export async function exportLogsCsv(f: LogFilters): Promise<string> {
  const p = buildFilterParams(f)
  // Only the filters the export route binds.
  p.delete('username')
  if (f.tenantWide) p.set('scope', 'tenant')
  p.set('max_rows', String(EXPORT_MAX_ROWS))
  try {
    const res = await http.get<string>(tenantUrl('/logs/export'), {
      params: Object.fromEntries(p),
      responseType: 'text',
      transformResponse: (d: unknown) => d,
    })
    return typeof res.data === 'string' ? res.data : ''
  } catch (e) {
    throw exportFailure(e)
  }
}

/**
 * A failed export answers with the JSON envelope, but the request asked for
 * plain text, so the body arrives unparsed: open it here to keep the server's
 * own message.
 */
function exportFailure(e: unknown): ApiError {
  // The shared interceptor already folded the axios error into an ApiError;
  // the original (with the raw body) rides along as `cause`.
  const base = toApiError(e)
  const raw = base.cause
  if (!isAxiosError(raw) || typeof raw.response?.data !== 'string') return base
  try {
    const body = JSON.parse(raw.response.data) as {
      message?: unknown
      error_code?: unknown
    }
    const message = typeof body.message === 'string' ? body.message.trim() : ''
    if (!message) return base
    return new ApiError(message, {
      status: base.status,
      code: typeof body.error_code === 'string' ? body.error_code : undefined,
      cause: raw,
    })
  } catch {
    return base
  }
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
