import { currentLocale } from '@/lib/locale'

/**
 * Pure data layer of the usage-logs page: wire types (taken from
 * handler.logView / handler.logStatView, not from the upstream project),
 * query building, and row interpretation. Nothing here renders.
 */

/** Log type 5 is LogTypeError (internal/domain/entity/log.go). */
export const LOG_TYPE_ERROR = 5

export const PAGE_SIZE = 20

/** Default lookback of every list/stat query that has no explicit start. */
export const DEFAULT_LOOKBACK_SEC = 7 * 24 * 3600
/** request_id search scans an unindexed JSON extract: always window it. */
export const ID_FILTER_LOOKBACK_SEC = DEFAULT_LOOKBACK_SEC

/** One row of GET /api/v2/~/logs (handler.logView). */
export interface UsageLog {
  id: number
  user_id: number
  created_at: number
  type: number
  content: string
  username: string
  token_name: string
  model_name: string
  quota: number
  prompt_tokens: number
  completion_tokens: number
  use_time: number
  is_stream: boolean
  channel: number
  channel_name: string
  token_id: number
  group: string
  total_latency_ms: number
  /** Wallet debit in 0.0001 CNY; only present when the server ships it. */
  other?: unknown
}

export interface LogListData {
  logs: UsageLog[] | null
  total: number
  page: number
  page_size: number
}

/** GET /api/v2/~/logs/stat (handler.logStatView). */
export interface LogStat {
  total_requests: number
  total_quota: number
  prompt_tokens: number
  completion_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  rpm: number
  tpm: number
}

export interface LogFilters {
  /** `datetime-local` value ("" = unset). */
  start: string
  end: string
  model: string
  token: string
  errorsOnly: boolean
  requestId: string
  /** Admin-only: read every member's rows (/logs/all). */
  tenantWide: boolean
}

export const EMPTY_FILTERS: LogFilters = {
  start: '',
  end: '',
  model: '',
  token: '',
  errorsOnly: false,
  requestId: '',
  tenantWide: false,
}

function toSec(value: string): number | null {
  if (!value) return null
  const ms = new Date(value).getTime()
  return Number.isFinite(ms) ? Math.floor(ms / 1000) : null
}

/**
 * Effective lower bound: explicit start wins; otherwise the lookback anchored
 * on the end bound (so start never lands after end) or on `nowSec`.
 */
export function computeStartTimeSec(
  start: string,
  end: string,
  hasIdFilter: boolean,
  nowSec: number = Math.floor(Date.now() / 1000)
): number {
  const explicit = toSec(start)
  if (explicit !== null) return explicit
  const anchor = toSec(end) ?? nowSec
  return anchor - (hasIdFilter ? ID_FILTER_LOOKBACK_SEC : DEFAULT_LOOKBACK_SEC)
}

/** Filter query shared by the list and the stat header. */
export function buildFilterParams(
  f: LogFilters,
  nowSec?: number
): URLSearchParams {
  const p = new URLSearchParams()
  const model = f.model.trim()
  const token = f.token.trim()
  if (model) p.set('model_name', model)
  if (token) p.set('token_name', token)
  if (f.errorsOnly) p.set('type', String(LOG_TYPE_ERROR))
  p.set(
    'start_time',
    String(computeStartTimeSec(f.start, f.end, !!f.requestId.trim(), nowSec))
  )
  const end = toSec(f.end)
  if (end !== null) p.set('end_time', String(end))
  return p
}

/** List query: filters + request_id + pagination. */
export function buildListParams(
  f: LogFilters,
  page: number,
  nowSec?: number
): URLSearchParams {
  const p = buildFilterParams(f, nowSec)
  const rid = f.requestId.trim()
  if (rid) p.set('request_id', rid)
  p.set('page', String(page))
  p.set('page_size', String(PAGE_SIZE))
  return p
}

/** Parsed `other` payload; absent or corrupt reads as null, never an error. */
export function parseOther(
  row: Pick<UsageLog, 'other'>
): Record<string, unknown> | null {
  const raw = row.other
  if (raw === undefined || raw === null || raw === '') return null
  try {
    const o = typeof raw === 'string' ? JSON.parse(raw) : raw
    return o && typeof o === 'object' && !Array.isArray(o)
      ? (o as Record<string, unknown>)
      : null
  } catch {
    return null
  }
}

export function isErrorLog(row: Pick<UsageLog, 'type'>): boolean {
  return Number(row.type) === LOG_TYPE_ERROR
}

/** other.settlement === 'failed': the debit may not have landed. */
export function isSettlementFailed(row: Pick<UsageLog, 'other'>): boolean {
  return parseOther(row)?.settlement === 'failed'
}

function positiveNumber(v: unknown): number | null {
  const n = Number(v)
  return Number.isFinite(n) && n > 0 ? n : null
}

export interface LogDetailFields {
  cacheReadTokens: number | null
  cacheWriteTokens: number | null
  endpoint: string | null
  firstResponseMs: number | null
  requestId: string | null
  sessionId: string | null
}

/** Billing-explainability fields; each is null unless the row carries it. */
export function detailFields(row: Pick<UsageLog, 'other'>): LogDetailFields {
  const o = parseOther(row) ?? {}
  const str = (v: unknown) => (typeof v === 'string' && v ? v : null)
  const frt = o.frt
  return {
    cacheReadTokens: positiveNumber(o.cache_tokens),
    cacheWriteTokens: positiveNumber(o.cache_creation_tokens),
    endpoint: str(o.request_path),
    firstResponseMs:
      frt !== undefined && frt !== null && Number.isFinite(Number(frt))
        ? Math.round(Number(frt))
        : null,
    requestId: str(o.request_id),
    sessionId: str(o.session_id),
  }
}

/** Compact count: 1234 -> 1.2K. */
export function compactNumber(n: number | null | undefined): string {
  const v = Number(n) || 0
  for (const [d, u] of [
    [1e9, 'B'],
    [1e6, 'M'],
    [1e3, 'K'],
  ] as const) {
    if (v >= d) return `${Number((v / d).toFixed(1))}${u}`
  }
  return v.toLocaleString(currentLocale())
}

/** Latency shown for a row; null when the relay recorded none. */
export function latencyMs(
  row: Pick<UsageLog, 'total_latency_ms'>
): number | null {
  return row.total_latency_ms > 0 ? row.total_latency_ms : null
}

/** Normalise the list payload (null `logs` becomes an empty array). */
export function normalizeList(data: LogListData | undefined | null): {
  logs: UsageLog[]
  total: number
} {
  return { logs: data?.logs ?? [], total: data?.total ?? 0 }
}

export function pageCount(total: number): number {
  return Math.max(1, Math.ceil(total / PAGE_SIZE))
}

/** Local date-time of a unix-seconds timestamp. */
export function formatLogTime(sec: number): string {
  return new Date(sec * 1000).toLocaleString(currentLocale())
}
