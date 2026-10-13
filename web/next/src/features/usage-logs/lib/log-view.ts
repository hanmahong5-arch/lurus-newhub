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
import { formatNumber } from '@/components/format'
import type { StatusVariant } from '@/components/status-badge'
import { currentLocale } from '@/lib/locale'

import { parseOther, type UsageLog } from './logs'

/**
 * How one log row is read for display: its type, what the `other` payload
 * says about tokens, routing and billing. Pure; nothing here renders. Every
 * reader treats an absent or corrupt payload as "nothing to show".
 */

/** entity.LogType* (internal/domain/entity/log.go). */
export const LOG_TYPE = {
  UNKNOWN: 0,
  TOPUP: 1,
  CONSUME: 2,
  MANAGE: 3,
  SYSTEM: 4,
  ERROR: 5,
  REFUND: 6,
} as const

export interface LogTypeConfig {
  value: number
  label: string
  color: StatusVariant
}

export const LOG_TYPES: readonly LogTypeConfig[] = [
  { value: LOG_TYPE.UNKNOWN, label: 'Unknown', color: 'neutral' },
  { value: LOG_TYPE.TOPUP, label: 'Top-up', color: 'cyan' },
  { value: LOG_TYPE.CONSUME, label: 'Consume', color: 'green' },
  { value: LOG_TYPE.MANAGE, label: 'Manage', color: 'orange' },
  { value: LOG_TYPE.SYSTEM, label: 'System event', color: 'purple' },
  { value: LOG_TYPE.ERROR, label: 'Error', color: 'red' },
  { value: LOG_TYPE.REFUND, label: 'Refund', color: 'blue' },
]

/** Types the filter offers (everything except the catch-all "unknown"). */
export const LOG_TYPE_FILTERS: readonly LogTypeConfig[] = LOG_TYPES.filter(
  (t) => t.value !== LOG_TYPE.UNKNOWN
)

export function getLogTypeConfig(type: number): LogTypeConfig {
  return (
    LOG_TYPES.find((t) => t.value === Number(type)) ??
    (LOG_TYPES[0] as LogTypeConfig)
  )
}

/** Rows that carry a model, tokens and a price. */
export function isDisplayableLogType(type: number): boolean {
  return [0, 2, 5, 6].includes(Number(type))
}

/** Rows that carry latency numbers. */
export function isTimingLogType(type: number): boolean {
  return [2, 5].includes(Number(type))
}

function num(v: unknown): number | null {
  const n = Number(v)
  return Number.isFinite(n) ? n : null
}

function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

export interface TokenBreakdown {
  prompt: number
  completion: number
  cacheRead: number
  cacheWrite: number
}

export function tokenBreakdown(row: UsageLog): TokenBreakdown {
  const o = parseOther(row) ?? {}
  const pos = (v: unknown) => {
    const n = num(v)
    return n !== null && n > 0 ? n : 0
  }
  return {
    prompt: row.prompt_tokens || 0,
    completion: row.completion_tokens || 0,
    cacheRead: pos(o.cache_tokens),
    cacheWrite: pos(o.cache_creation_tokens),
  }
}

/** The wallet debit of the row in CNY, or null when none was recorded. */
export function chargedCny(row: Pick<UsageLog, 'charged_cny4'>): number | null {
  const units = Number(row.charged_cny4)
  return Number.isFinite(units) && units > 0 ? units / 10000 : null
}

export function formatCny(cny: number): string {
  return `¥${cny.toFixed(4)}`
}

/** Group ratio that actually applied (user-specific wins), or null if 1x. */
export function groupRatio(row: Pick<UsageLog, 'other'>): number | null {
  const o = parseOther(row)
  if (!o) return null
  const user = num(o.user_group_ratio)
  if (user !== null && user !== -1) return user
  const group = num(o.group_ratio)
  return group !== null && group !== 1 ? group : null
}

export function formatRatio(ratio: number): string {
  return ratio % 1 === 0 ? String(ratio) : ratio.toFixed(4).replace(/\.?0+$/, '')
}

export interface RouteAttempt {
  channel_id: number
  channel_name: string
  provider: string
  outcome: string
  status_code: number | null
  error_code: string
  duration_ms: number | null
}

/**
 * Per-attempt routing trace; written only when the request bounced between
 * channels and only shipped to platform staff. Empty means "not applicable
 * or not visible to you", never an error.
 */
export function routeAttempts(row: Pick<UsageLog, 'other'>): RouteAttempt[] {
  const o = parseOther(row)
  const admin = o?.admin_info
  const raw =
    admin && typeof admin === 'object'
      ? (admin as Record<string, unknown>).route_attempts
      : undefined
  if (!Array.isArray(raw)) return []
  return raw
    .filter((a): a is Record<string, unknown> => !!a && typeof a === 'object')
    .map((a) => ({
      channel_id: num(a.channel_id) ?? 0,
      channel_name: str(a.channel_name),
      provider: str(a.provider),
      outcome: str(a.outcome),
      status_code: num(a.status_code) || null,
      error_code: str(a.error_code),
      duration_ms: num(a.duration_ms),
    }))
}

export function attemptVariant(outcome: string): StatusVariant {
  if (outcome === 'success') return 'success'
  if (outcome === 'breaker_open') return 'neutral'
  return 'danger'
}

/** Channels visited in order (staff only), e.g. "3 -> 7". */
export function channelChain(row: Pick<UsageLog, 'other'>): string | null {
  const attempts = routeAttempts(row)
  if (attempts.length < 2) return null
  return attempts.map((a) => `#${a.channel_id}`).join(' → ')
}

export interface ErrorFacts {
  errorCode: string
  errorType: string
  statusCode: number | null
}

export function errorFacts(row: Pick<UsageLog, 'other'>): ErrorFacts {
  const o = parseOther(row) ?? {}
  return {
    errorCode: str(o.error_code),
    errorType: str(o.error_type),
    statusCode: num(o.status_code) || null,
  }
}

export interface MappedModel {
  name: string
  /** Upstream model when the request model was remapped, else null. */
  upstream: string | null
}

export function mappedModel(row: UsageLog): MappedModel {
  const o = parseOther(row)
  const upstream =
    o?.is_model_mapped === true && str(o.upstream_model_name)
      ? str(o.upstream_model_name)
      : null
  return { name: row.model_name, upstream }
}

/** Per-call price in USD when the row was billed per call, else null. */
export function perCallPriceUsd(
  row: Pick<UsageLog, 'other'>
): number | null {
  const p = num(parseOther(row)?.model_price)
  return p !== null && p > 0 ? p : null
}

export interface RatioFacts {
  model: number | null
  completion: number | null
  cache: number | null
}

/** Billing multipliers recorded for a token-priced row. */
export function ratioFacts(row: Pick<UsageLog, 'other'>): RatioFacts {
  const o = parseOther(row) ?? {}
  const pos = (v: unknown) => {
    const n = num(v)
    return n !== null && n > 0 ? n : null
  }
  return {
    model: pos(o.model_ratio),
    completion: pos(o.completion_ratio),
    cache: pos(o.cache_ratio),
  }
}

export interface DetailSegment {
  text: string
  muted?: boolean
  danger?: boolean
}

type Translate = (key: string, opts?: Record<string, unknown>) => string

/**
 * The one-line preview of the "Details" column, most important first. The
 * full explanation lives in the detail sheet.
 */
export function detailSegments(
  row: UsageLog,
  t: Translate,
  formatUsd: (usd: number) => string
): DetailSegment[] {
  const o = parseOther(row)
  const out: DetailSegment[] = []
  const type = Number(row.type)

  if (o?.settlement === 'failed') {
    out.push({ text: t('Settlement failed'), danger: true })
  }
  if (type === LOG_TYPE.ERROR) {
    const f = errorFacts(row)
    const head = [f.statusCode ? String(f.statusCode) : '', f.errorCode]
      .filter(Boolean)
      .join(' ')
    if (head) out.push({ text: head, danger: true })
    if (row.content) out.push({ text: row.content, danger: !head })
    return out
  }
  if (type === LOG_TYPE.REFUND) {
    out.push({ text: t('Async task refund') })
    if (row.content) out.push({ text: row.content, muted: true })
    return out
  }
  if (type !== LOG_TYPE.CONSUME) {
    if (row.content) out.push({ text: row.content })
    return out
  }

  const perCall = perCallPriceUsd(row)
  if (perCall !== null) {
    out.push({ text: `${t('Per-call')} · ${formatUsd(perCall)}` })
  } else {
    const r = ratioFacts(row)
    if (r.model !== null) {
      const parts = [`${t('Model ratio')} ${formatRatio(r.model)}x`]
      if (r.completion !== null) {
        parts.push(`${t('Completion ratio')} ${formatRatio(r.completion)}x`)
      }
      out.push({ text: parts.join(' · ') })
    }
  }
  const gr = groupRatio(row)
  if (gr !== null) {
    out.push({
      text: `${t('Group ratio')} ${formatRatio(gr)}x`,
      muted: true,
    })
  }
  if (o?.is_system_prompt_overwritten === true) {
    out.push({ text: t('System prompt override'), danger: true })
  }
  if (out.length === 0 && row.content) {
    out.push({ text: row.content, muted: true })
  }
  return out
}

/** Latency colour tiers (seconds). */
export function firstResponseVariant(seconds: number): StatusVariant {
  if (seconds < 5) return 'success'
  if (seconds < 10) return 'warning'
  return 'danger'
}

export function durationVariant(
  seconds: number,
  completionTokens: number
): StatusVariant {
  if (completionTokens >= 100 && seconds > 0) {
    const tps = completionTokens / seconds
    if (tps >= 30) return 'success'
    if (tps >= 15) return 'warning'
    return 'danger'
  }
  if (seconds < 10) return 'success'
  if (seconds < 30) return 'warning'
  return 'danger'
}

/** Seconds -> "850ms" / "1.2s" / "2m 05s". */
export function formatSeconds(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '--'
  if (seconds < 1) return `${Math.round(seconds * 1000)}ms`
  if (seconds < 60) return `${seconds.toFixed(1)}s`
  const m = Math.floor(seconds / 60)
  const s = Math.round(seconds - m * 60)
  return `${m}m ${String(s).padStart(2, '0')}s`
}

/** Total request time in seconds, preferring the relay's ms measurement. */
export function durationSec(
  row: Pick<UsageLog, 'total_latency_ms' | 'use_time'>
): number | null {
  if (row.total_latency_ms > 0) return row.total_latency_ms / 1000
  return row.use_time > 0 ? row.use_time : null
}

/** Time to first token in ms (only streamed calls record one). */
export function firstTokenMs(row: Pick<UsageLog, 'other'>): number | null {
  const frt = num(parseOther(row)?.frt)
  return frt !== null && frt > 0 ? Math.round(frt) : null
}

export function tokensPerSecond(row: UsageLog): number | null {
  const d = durationSec(row)
  return d !== null && row.completion_tokens > 0 ? row.completion_tokens / d : null
}

/** Grouped integer in the console language (never the browser locale). */
export function fmtNum(n: number | null | undefined): string {
  return formatNumber(n, currentLocale())
}
