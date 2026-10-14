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
import {
  CHANNEL_TYPES,
  CHANNEL_TYPE_ICONS,
  RESPONSE_TIME_CONFIG,
  RESPONSE_TIME_THRESHOLDS,
  TYPE_TO_KEY_PROMPT,
} from '../constants'
import type {
  Channel,
  ChannelOtherSettings,
  ChannelSettings,
} from '../types'

// ============================================================================
// Channel type
// ============================================================================

/** Label for a type; unknown ids keep their number so the row stays usable. */
export function getChannelTypeLabel(type: number): string {
  return CHANNEL_TYPES[type] ?? `Type #${type}`
}

/** Vendor icon name for getLobeIcon (language-independent, keyed by type). */
export function getChannelTypeIcon(type: number): string {
  return CHANNEL_TYPE_ICONS[type] ?? 'OpenAI'
}

// ============================================================================
// Status
// ============================================================================

export function isChannelEnabled(channel: Channel): boolean {
  return channel.status === 1
}

export function isMultiKeyChannel(channel: Channel): boolean {
  return channel.channel_info?.is_multi_key || false
}

// ============================================================================
// Key formatting
// ============================================================================

/** Deduplicate keys of a multi-line string, keeping first-seen order. */
export function deduplicateKeys(keysText: string): {
  deduplicatedText: string
  beforeCount: number
  afterCount: number
  removedCount: number
} {
  if (!keysText || keysText.trim() === '') {
    return {
      deduplicatedText: '',
      beforeCount: 0,
      afterCount: 0,
      removedCount: 0,
    }
  }
  const lines = keysText.split('\n')
  const seen = new Set<string>()
  const kept: string[] = []
  for (const line of lines) {
    const trimmed = line.trim()
    if (trimmed && !seen.has(trimmed)) {
      seen.add(trimmed)
      kept.push(trimmed)
    }
  }
  return {
    deduplicatedText: kept.join('\n'),
    beforeCount: lines.length,
    afterCount: kept.length,
    removedCount: lines.length - kept.length,
  }
}

export function getKeyPromptForType(type: number): string {
  return TYPE_TO_KEY_PROMPT[type] || 'Enter API key for this channel'
}

// ============================================================================
// Model and group lists
// ============================================================================

export function parseModelsList(models: string | null | undefined): string[] {
  if (!models) return []
  return models
    .split(',')
    .map((m) => m.trim())
    .filter((m) => m.length > 0)
}

/** Comma-separated groups; `default` first, the rest alphabetical. */
export function parseGroupsList(groups: string | null | undefined): string[] {
  if (!groups) return []
  const list = groups
    .split(',')
    .map((g) => g.trim())
    .filter((g) => g.length > 0)
  return list.sort((a, b) => {
    if (a === 'default') return -1
    if (b === 'default') return 1
    return a.localeCompare(b)
  })
}

// ============================================================================
// Settings documents
// ============================================================================

function parseJsonObject(raw: string | null | undefined): Record<string, unknown> {
  if (!raw || raw === '{}') return {}
  try {
    const parsed: unknown = JSON.parse(raw)
    return typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : {}
  } catch {
    return {}
  }
}

export function parseChannelSettings(
  raw: string | null | undefined
): ChannelSettings {
  return parseJsonObject(raw) as ChannelSettings
}

export function parseChannelOtherSettings(
  raw: string | null | undefined
): ChannelOtherSettings {
  return parseJsonObject(raw) as ChannelOtherSettings
}

// ============================================================================
// Balance (upstream account balance, USD) and response time
// ============================================================================

/** `$1.23`; small balances keep four digits. "-" when unknown. */
export function formatUsdBalance(balance: number | null | undefined): string {
  if (balance == null || Number.isNaN(balance)) return '-'
  const digits = Math.abs(balance) >= 1 ? 2 : 4
  return `$${balance.toFixed(digits)}`
}

export function getBalanceVariant(
  balance: number
): 'success' | 'warning' | 'danger' | 'neutral' {
  if (balance === 0) return 'neutral'
  if (balance < 1) return 'danger'
  if (balance < 10) return 'warning'
  return 'success'
}

type TFunction = (key: string, options?: { value?: number | string }) => string

/** Response time (ms) as text; pass `t` for the localized units. */
export function formatResponseTime(timeMs: number, t?: TFunction): string {
  if (timeMs === 0) return t ? t('Not tested') : 'Not tested'
  if (timeMs < 1000) {
    return t ? t('{{value}}ms', { value: timeMs }) : `${timeMs}ms`
  }
  const seconds = (timeMs / 1000).toFixed(2)
  return t ? t('{{value}}s', { value: seconds }) : `${seconds}s`
}

export function getResponseTimeConfig(timeMs: number) {
  if (timeMs === 0) return RESPONSE_TIME_CONFIG.UNKNOWN
  if (timeMs <= RESPONSE_TIME_THRESHOLDS.EXCELLENT) {
    return RESPONSE_TIME_CONFIG.EXCELLENT
  }
  if (timeMs <= RESPONSE_TIME_THRESHOLDS.GOOD) return RESPONSE_TIME_CONFIG.GOOD
  if (timeMs <= RESPONSE_TIME_THRESHOLDS.FAIR) return RESPONSE_TIME_CONFIG.FAIR
  return RESPONSE_TIME_CONFIG.POOR
}

// ============================================================================
// Time
// ============================================================================

/**
 * Compact relative time ("4h ago") for a Unix timestamp in seconds. The caller
 * supplies the locale, so the interface language is honoured.
 */
export function formatRelativeTime(
  timestamp: number,
  locale: Intl.LocalesArgument,
  neverLabel = 'Never'
): string {
  if (!timestamp) return neverLabel
  try {
    const diffSec = timestamp - Date.now() / 1000
    const absSec = Math.abs(diffSec)
    const rtf = new Intl.RelativeTimeFormat(locale, {
      numeric: 'always',
      style: 'narrow',
    })
    const MINUTE = 60
    const HOUR = 60 * MINUTE
    const DAY = 24 * HOUR
    const MONTH = 30 * DAY
    const YEAR = 365 * DAY

    let value: number
    let unit: Intl.RelativeTimeFormatUnit
    if (absSec < MINUTE) {
      value = Math.round(diffSec)
      unit = 'second'
    } else if (absSec < HOUR) {
      value = Math.round(diffSec / MINUTE)
      unit = 'minute'
    } else if (absSec < DAY) {
      value = Math.round(diffSec / HOUR)
      unit = 'hour'
    } else if (absSec < MONTH) {
      value = Math.round(diffSec / DAY)
      unit = 'day'
    } else if (absSec < YEAR) {
      value = Math.round(diffSec / MONTH)
      unit = 'month'
    } else {
      value = Math.round(diffSec / YEAR)
      unit = 'year'
    }
    const formatted = rtf.format(value, unit)
    const primary = Array.isArray(locale) ? locale[0] : locale
    if (primary?.toString().startsWith('zh')) {
      return formatted.replaceAll(/(\d)([\u4e00-\u9fff])/g, '$1 $2')
    }
    return formatted
  } catch {
    return '-'
  }
}

// ============================================================================
// Tag aggregation (tag mode)
// ============================================================================

/** A tag row: the aggregate of its child channels. */
export type TagRow = Channel & { children: Channel[] }

export function isTagAggregateRow(row: Channel | TagRow): row is TagRow {
  return Array.isArray((row as TagRow).children)
}

export function getChannelTableRowId(row: Channel | TagRow): string {
  if (isTagAggregateRow(row)) return `tag:${row.tag || ''}`
  return `channel:${row.id}`
}

/** Group a flat page of channels by tag into aggregate rows with children. */
export function aggregateChannelsByTag(
  channels: Channel[]
): Array<Channel | TagRow> {
  const tagMap = new Map<string, TagRow>()
  const result: Array<Channel | TagRow> = []

  for (const channel of channels) {
    const tag = channel.tag || ''
    let tagRow = tagMap.get(tag)
    if (!tagRow) {
      tagRow = {
        ...channel,
        key: tag,
        tag,
        name: tag,
        type: 0,
        status: 0,
        group: '',
        used_quota: 0,
        response_time: 0,
        priority: -1,
        weight: -1,
        balance: 0,
        test_time: 0,
        created_time: 0,
        balance_updated_time: 0,
        models: '',
        children: [],
      }
      tagMap.set(tag, tagRow)
      result.push(tagRow)
    }

    tagRow.children.push(channel)
    const count = tagRow.children.length
    tagRow.used_quota += channel.used_quota
    tagRow.response_time =
      (tagRow.response_time * (count - 1) + channel.response_time) / count

    // priority/weight: the shared value, or null when children differ
    if (tagRow.priority === -1) tagRow.priority = channel.priority ?? 0
    else if (tagRow.priority !== (channel.priority ?? 0)) tagRow.priority = null
    if (tagRow.weight === -1) tagRow.weight = channel.weight ?? 0
    else if (tagRow.weight !== (channel.weight ?? 0)) tagRow.weight = null

    const groups = new Set(tagRow.group.split(',').filter(Boolean))
    for (const g of channel.group.split(',').filter(Boolean)) groups.add(g)
    tagRow.group = [...groups].join(',')

    // enabled when any child is enabled
    if (channel.status === 1) tagRow.status = 1
    else if (tagRow.status === 0) tagRow.status = channel.status
  }

  return result
}
