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
/**
 * Generic number / time / token formatters shared by the console features.
 *
 * Ported from the donor `lib/format.ts` WITHOUT its quota and currency half:
 * money goes through `@/lib/money` (`formatQuota(quota, config)` with the
 * config from `useMoneyConfig()` / `useQuota()`), never through a module-level
 * currency guess.
 */
import dayjs from '@/lib/dayjs'

export function formatNumber(
  value: number | null | undefined,
  locales?: Intl.LocalesArgument
): string {
  if (value == null || Number.isNaN(value as number)) return '-'
  return Intl.NumberFormat(locales, { maximumFractionDigits: 2 }).format(
    value as number
  )
}

export function formatCompactNumber(
  value: number | null | undefined,
  locales?: Intl.LocalesArgument
): string {
  if (value == null || Number.isNaN(value as number)) return '-'
  return Intl.NumberFormat(locales, {
    notation: 'compact',
    maximumFractionDigits: 1,
  }).format(value as number)
}

/** `value` is already a percentage (12.5 -> "12.5%"). */
export function formatPercent(value: number | null | undefined): string {
  if (value == null || Number.isNaN(value as number)) return '-'
  return Intl.NumberFormat(undefined, {
    style: 'percent',
    maximumFractionDigits: 2,
  }).format((value as number) / 100)
}

/**
 * Format a Unix timestamp (seconds, or ms with `unit`) as
 * YYYY-MM-DD HH:mm:ss. Missing / 0 / -1 (the "never" sentinel) -> "-".
 */
export function formatTimestampToDate(
  timestamp?: number,
  unit: 'seconds' | 'milliseconds' = 'seconds'
): string {
  if (!timestamp || timestamp === -1) return '-'
  const ms = unit === 'seconds' ? timestamp * 1000 : timestamp
  return dayjs(ms).format('YYYY-MM-DD HH:mm:ss')
}

/** Like formatTimestampToDate but -1 is rendered by the caller, not here. */
export function formatTimestamp(timestamp: number): string {
  return formatTimestampToDate(timestamp)
}

/** Relative time ("30 seconds ago") through Intl.RelativeTimeFormat. */
export function formatTimestampRelative(
  timestamp?: number,
  unit: 'seconds' | 'milliseconds' = 'seconds',
  locales?: Intl.LocalesArgument
): string {
  if (!timestamp || timestamp === -1) return '-'

  const ms = unit === 'seconds' ? timestamp * 1000 : timestamp
  const diffSeconds = Math.round((ms - Date.now()) / 1000)
  const absSeconds = Math.abs(diffSeconds)
  const formatter = new Intl.RelativeTimeFormat(locales, { numeric: 'always' })

  if (absSeconds < 60) return formatter.format(diffSeconds, 'second')
  if (absSeconds < 3600) {
    return formatter.format(Math.round(diffSeconds / 60), 'minute')
  }
  if (absSeconds < 86400) {
    return formatter.format(Math.round(diffSeconds / 3600), 'hour')
  }
  if (absSeconds < 2592000) {
    return formatter.format(Math.round(diffSeconds / 86400), 'day')
  }
  if (absSeconds < 31536000) {
    return formatter.format(Math.round(diffSeconds / 2592000), 'month')
  }
  return formatter.format(Math.round(diffSeconds / 31536000), 'year')
}

/** Date -> YYYY-MM-DD HH:mm:ss */
export function formatDateTimeStr(date: Date): string {
  return dayjs(date).format('YYYY-MM-DD HH:mm:ss')
}

/** Date -> YYYY-MM-DD */
export function formatDateStr(date: Date): string {
  return dayjs(date).format('YYYY-MM-DD')
}

/** Date -> HH:mm:ss */
export function formatTimeStr(date: Date): string {
  return dayjs(date).format('HH:mm:ss')
}

/** Token count with K / M suffix; 0 -> "-". */
export function formatTokens(tokens: number): string {
  if (tokens === 0) return '-'
  if (tokens < 1000) return tokens.toString()
  if (tokens < 1000000) return `${(tokens / 1000).toFixed(1)}K`
  return `${(tokens / 1000000).toFixed(2)}M`
}

/** Duration in seconds -> "1.2s" / "3m 5s". */
export function formatUseTime(seconds: number): string {
  if (seconds < 60) return `${seconds.toFixed(1)}s`
  const minutes = Math.floor(seconds / 60)
  const remainingSeconds = seconds % 60
  return `${minutes}m ${remainingSeconds.toFixed(0)}s`
}

/** Unix timestamp (seconds) -> datetime-local value (YYYY-MM-DDTHH:mm). */
export function formatTimestampForInput(timestamp: number): string {
  if (timestamp === -1) return ''
  return dayjs(timestamp * 1000).format('YYYY-MM-DDTHH:mm')
}

/** datetime-local value -> Unix timestamp (seconds); empty -> -1 ("never"). */
export function parseTimestampFromInput(value: string): number {
  if (!value) return -1
  return Math.floor(new Date(value).getTime() / 1000)
}
