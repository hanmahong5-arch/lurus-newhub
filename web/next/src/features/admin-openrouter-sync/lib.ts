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
import type { TFunction } from 'i18next'

import type { StatusVariant } from '@/components/status-badge'

import {
  TOP_N_MAX,
  type JobBody,
  type OpenRouterChannel,
  type SyncCategory,
  type SyncJob,
} from './types'

/** The categories column is JSON text; tolerate an array and garbage. */
export function parseCategories(raw: SyncJob['categories']): string[] {
  if (!raw) return []
  if (Array.isArray(raw)) return raw
  try {
    const v: unknown = JSON.parse(raw)
    return Array.isArray(v) ? (v as string[]) : []
  } catch {
    return []
  }
}

/**
 * Localised label of a category. The server labels are fixed Chinese strings,
 * so the known keys are mapped here (literal t() calls so the key extractor
 * sees them) and the server label is only the fallback for a new key.
 */
export function categoryLabel(
  key: string,
  serverLabel: string | undefined,
  t: TFunction
): string {
  switch (key) {
    case 'llm_reasoning':
      return t('Reasoning language models')
    case 'vision':
      return t('Multimodal vision')
    case 'image_gen':
      return t('Text to image')
    case 'asr':
      return t('Speech to text')
    case 'tts':
      return t('Text to speech')
    default:
      return serverLabel || key
  }
}

export function scheduleLabel(schedule: string, t: TFunction): string {
  switch (schedule) {
    case 'daily':
      return t('Daily (24h)')
    case 'weekly':
      return t('Weekly (7d)')
    case 'manual':
      return t('Manual only')
    default:
      return schedule || '—'
  }
}

/** "target channel" cell: `name (#id)`, or `#id` when the channel is unknown. */
export function channelLabel(
  id: number,
  channels: OpenRouterChannel[] | undefined
): string {
  const c = channels?.find((x) => x.id === id)
  return c ? `${c.name} (#${id})` : `#${id}`
}

export function categoryOptions(
  categories: SyncCategory[] | undefined,
  selected: string[]
): SyncCategory[] {
  const known = categories ?? []
  const extra = selected
    .filter((k) => !known.some((c) => c.key === k))
    .map((k) => ({ key: k, label: k }))
  return [...known, ...extra]
}

export interface JobFormValues {
  name: string
  channelId: string
  categories: string[]
  topN: string
  schedule: string
  enabled: boolean
}

export const EMPTY_JOB_FORM: JobFormValues = {
  name: '',
  channelId: '',
  categories: [],
  topN: '0',
  schedule: 'manual',
  enabled: true,
}

export function formFromJob(job: SyncJob): JobFormValues {
  return {
    name: job.name,
    channelId: job.target_channel_id > 0 ? String(job.target_channel_id) : '',
    categories: parseCategories(job.categories),
    topN: String(job.top_n ?? 0),
    schedule: job.schedule || 'manual',
    enabled: job.enabled,
  }
}

export type JobFormError = 'required' | 'top-n'

export type BuiltJob =
  | { ok: true; body: JobBody }
  | { ok: false; error: JobFormError }

/** Validate the form and turn it into the request body. */
export function buildJobBody(form: JobFormValues): BuiltJob {
  const name = form.name.trim()
  const channelId = Number(form.channelId)
  if (!name || !Number.isInteger(channelId) || channelId <= 0) {
    return { ok: false, error: 'required' }
  }
  if (form.categories.length === 0) return { ok: false, error: 'required' }
  const raw = form.topN.trim()
  const topN = raw === '' ? 0 : Number(raw)
  if (!Number.isInteger(topN) || topN < 0 || topN > TOP_N_MAX) {
    return { ok: false, error: 'top-n' }
  }
  return {
    ok: true,
    body: {
      name,
      target_channel_id: channelId,
      categories: form.categories,
      top_n: topN,
      schedule: form.schedule,
      enabled: form.enabled,
    },
  }
}

/** Cool-down countdown: "45s", "3m 20s", "2h 5m", or "recovering shortly". */
export function formatRemaining(
  seconds: number | undefined,
  t: TFunction
): string {
  const s = seconds ?? 0
  if (!s || s <= 0) return t('Recovering shortly')
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  return `${h}h ${m}m`
}

export const KEY_STATUS_VARIANT: Record<string, StatusVariant> = {
  enabled: 'success',
  cooling: 'warning',
  permanent_disabled: 'danger',
}

export const CHANNEL_STATUS_VARIANT: Record<string, StatusVariant> = {
  enabled: 'success',
  auto_disabled: 'warning',
  manually_disabled: 'danger',
}

export function keyStatusLabel(status: string, t: TFunction): string {
  switch (status) {
    case 'enabled':
      return t('Enabled')
    case 'cooling':
      return t('Cooling down')
    case 'permanent_disabled':
      return t('Permanently disabled')
    default:
      return status
  }
}

export function channelStatusLabel(status: string, t: TFunction): string {
  switch (status) {
    case 'enabled':
      return t('Enabled')
    case 'auto_disabled':
      return t('Auto-disabled (pending recovery)')
    case 'manually_disabled':
      return t('Manually disabled')
    default:
      return status
  }
}
