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
import type { StatusVariant } from '@/components/status-badge'

/**
 * Pure data layer of the task-log page: wire shapes (taken from
 * entity.Task / entity.Midjourney and handler.GetUserTask /
 * GetUserMidjourney), query building, row interpretation. Nothing renders.
 *
 * Time units differ per list: tasks use unix SECONDS, Midjourney rows use
 * unix MILLISECONDS (submit_time is whatever the upstream proxy stamped).
 */

export const DEFAULT_PAGE_SIZE = 20

/** `{ items, total, page, page_size }` (common.PageInfo). */
export interface TaskPage<T> {
  items: T[]
  total: number
}

export interface TaskLog {
  id: number
  created_at: number
  updated_at: number
  task_id: string
  platform: string
  user_id: number
  group: string
  quota: number
  action: string
  status: string
  /** On success of a video task the result URL; otherwise the failure text. */
  fail_reason: string
  submit_time: number
  start_time: number
  finish_time: number
  progress: string
  properties: {
    input: string
    upstream_model_name: string
    origin_model_name: string
  }
  data: unknown
  project_id: number
  request_id: string
}

export interface MjLog {
  id: number
  code: number
  user_id: number
  action: string
  mj_id: string
  prompt: string
  prompt_en: string
  description: string
  state: string
  submit_time: number
  start_time: number
  finish_time: number
  image_url: string
  video_url: string
  status: string
  progress: string
  fail_reason: string
  quota: number
}

function rec(v: unknown): Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : {}
}
function num(v: unknown): number {
  const n = Number(v)
  return Number.isFinite(n) ? n : 0
}
function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

export function mapTaskLog(raw: unknown): TaskLog {
  const r = rec(raw)
  const p = rec(r.properties)
  return {
    id: num(r.id),
    created_at: num(r.created_at),
    updated_at: num(r.updated_at),
    task_id: str(r.task_id),
    platform: str(r.platform),
    user_id: num(r.user_id),
    group: str(r.group),
    quota: num(r.quota),
    action: str(r.action),
    status: str(r.status),
    fail_reason: str(r.fail_reason),
    submit_time: num(r.submit_time),
    start_time: num(r.start_time),
    finish_time: num(r.finish_time),
    progress: str(r.progress),
    properties: {
      input: str(p.input),
      upstream_model_name: str(p.upstream_model_name),
      origin_model_name: str(p.origin_model_name),
    },
    data: r.data,
    project_id: num(r.project_id),
    request_id: str(r.request_id),
  }
}

export function mapMjLog(raw: unknown): MjLog {
  const r = rec(raw)
  return {
    id: num(r.id),
    code: num(r.code),
    user_id: num(r.user_id),
    action: str(r.action),
    mj_id: str(r.mj_id),
    prompt: str(r.prompt),
    prompt_en: str(r.prompt_en),
    description: str(r.description),
    state: str(r.state),
    submit_time: num(r.submit_time),
    start_time: num(r.start_time),
    finish_time: num(r.finish_time),
    image_url: str(r.image_url),
    video_url: str(r.video_url),
    status: str(r.status),
    progress: str(r.progress),
    fail_reason: str(r.fail_reason),
    quota: num(r.quota),
  }
}

/** Normalise a PageInfo payload (`items` may be null on an empty page). */
export function mapPage<T>(
  raw: unknown,
  mapRow: (row: unknown) => T
): TaskPage<T> {
  const r = rec(raw)
  const items = (Array.isArray(r.items) ? r.items : []).map(mapRow)
  return { items, total: num(r.total) || items.length }
}

/* ------------------------------ filters ------------------------------ */

export interface TaskFilters {
  /** `datetime-local` values; '' = unset. */
  start: string
  end: string
  taskId: string
  platform: string
  /** '' = every status. */
  status: string
  action: string
}

export const EMPTY_TASK_FILTERS: TaskFilters = {
  start: '',
  end: '',
  taskId: '',
  platform: '',
  status: '',
  action: '',
}

export interface MjFilters {
  start: string
  end: string
  mjId: string
}

export const EMPTY_MJ_FILTERS: MjFilters = { start: '', end: '', mjId: '' }

function toMs(value: string): number | null {
  if (!value) return null
  const ms = new Date(value).getTime()
  return Number.isFinite(ms) ? ms : null
}

/** Window problem: start after end. */
export function rangeError(start: string, end: string): boolean {
  const a = toMs(start)
  const b = toMs(end)
  return a !== null && b !== null && a > b
}

/** GET /api/task/self/ query: `p` is the 1-based page, times are seconds. */
export function taskParams(
  f: TaskFilters,
  page: number,
  pageSize: number
): Record<string, string | number> {
  const p: Record<string, string | number> = { p: page, page_size: pageSize }
  const set = (k: string, v: string) => {
    const t = v.trim()
    if (t) p[k] = t
  }
  set('task_id', f.taskId)
  set('platform', f.platform)
  set('status', f.status)
  set('action', f.action)
  const a = toMs(f.start)
  const b = toMs(f.end)
  if (a !== null) p.start_timestamp = Math.floor(a / 1000)
  if (b !== null) p.end_timestamp = Math.floor(b / 1000)
  return p
}

/** GET /api/mj/self/ query: times are milliseconds. */
export function mjParams(
  f: MjFilters,
  page: number,
  pageSize: number
): Record<string, string | number> {
  const p: Record<string, string | number> = { p: page, page_size: pageSize }
  if (f.mjId.trim()) p.mj_id = f.mjId.trim()
  const a = toMs(f.start)
  const b = toMs(f.end)
  if (a !== null) p.start_timestamp = a
  if (b !== null) p.end_timestamp = b
  return p
}

/* ------------------------------ mappings ------------------------------ */

export const TASK_STATUS = {
  NOT_START: 'NOT_START',
  SUBMITTED: 'SUBMITTED',
  QUEUED: 'QUEUED',
  IN_PROGRESS: 'IN_PROGRESS',
  SUCCESS: 'SUCCESS',
  FAILURE: 'FAILURE',
  UNKNOWN: 'UNKNOWN',
} as const

interface StatusMapping {
  label: string
  variant: StatusVariant
}

const TASK_STATUS_MAP: Record<string, StatusMapping> = {
  SUCCESS: { label: 'Succeeded', variant: 'green' },
  NOT_START: { label: 'Not started', variant: 'neutral' },
  SUBMITTED: { label: 'Queued', variant: 'yellow' },
  QUEUED: { label: 'Queued', variant: 'orange' },
  IN_PROGRESS: { label: 'In progress', variant: 'blue' },
  FAILURE: { label: 'Failed', variant: 'red' },
  UNKNOWN: { label: 'Unknown', variant: 'neutral' },
}

const MJ_STATUS_MAP: Record<string, StatusMapping> = {
  ...TASK_STATUS_MAP,
  MODAL: { label: 'Waiting', variant: 'amber' },
}

export function taskStatus(status: string): StatusMapping {
  return (
    TASK_STATUS_MAP[status] ?? {
      label: status || 'Submitting',
      variant: 'neutral',
    }
  )
}

export function mjStatus(status: string): StatusMapping {
  return (
    MJ_STATUS_MAP[status] ?? { label: status || 'Submitting', variant: 'neutral' }
  )
}

/** Status values the filter offers (those a task row can really carry). */
export const TASK_STATUS_FILTERS: readonly string[] = [
  TASK_STATUS.NOT_START,
  TASK_STATUS.SUBMITTED,
  TASK_STATUS.QUEUED,
  TASK_STATUS.IN_PROGRESS,
  TASK_STATUS.SUCCESS,
  TASK_STATUS.FAILURE,
]

const TASK_ACTION_LABEL: Record<string, string> = {
  MUSIC: 'Generate music',
  LYRICS: 'Generate lyrics',
  GENERATE: 'Music generate',
  generate: 'Image to video',
  textGenerate: 'Text to video',
  firstTailGenerate: 'First/last frame to video',
  referenceGenerate: 'Reference video',
  remixGenerate: 'Video remix',
}

export function taskActionLabel(action: string): string {
  return TASK_ACTION_LABEL[action] ?? action
}

const MJ_ACTION_LABEL: Record<string, string> = {
  IMAGINE: 'Draw',
  UPSCALE: 'Upscale',
  VIDEO: 'Video',
  EDITS: 'Edit',
  VARIATION: 'Vary',
  HIGH_VARIATION: 'Vary (strong)',
  LOW_VARIATION: 'Vary (subtle)',
  PAN: 'Pan',
  DESCRIBE: 'Describe',
  BLEND: 'Blend',
  UPLOAD: 'Upload',
  SHORTEN: 'Shorten',
  REROLL: 'Reroll',
  INPAINT: 'Inpaint',
  SWAP_FACE: 'Swap face',
  ZOOM: 'Zoom',
  CUSTOM_ZOOM: 'Custom zoom',
}

export function mjActionLabel(action: string): string {
  return MJ_ACTION_LABEL[action] ?? action
}

const MJ_ACTION_VARIANT: Record<string, StatusVariant> = {
  IMAGINE: 'blue',
  UPSCALE: 'orange',
  VIDEO: 'orange',
  EDITS: 'orange',
  VARIATION: 'violet',
  HIGH_VARIATION: 'violet',
  LOW_VARIATION: 'violet',
  PAN: 'cyan',
  DESCRIBE: 'yellow',
  BLEND: 'lime',
  UPLOAD: 'blue',
  SHORTEN: 'pink',
  REROLL: 'indigo',
  INPAINT: 'teal',
  SWAP_FACE: 'purple',
  ZOOM: 'green',
  CUSTOM_ZOOM: 'green',
}

export function mjActionVariant(action: string): StatusVariant {
  return MJ_ACTION_VARIANT[action] ?? 'neutral'
}

/* ------------------------------ row reading ------------------------------ */

/** Seconds a job took (finish - submit), or null while it is unfinished. */
export function durationSeconds(
  submit: number,
  finish: number,
  unit: 'seconds' | 'milliseconds'
): number | null {
  if (!(submit > 0) || !(finish > 0) || finish < submit) return null
  const d = finish - submit
  return unit === 'seconds' ? d : d / 1000
}

/** Only plain http(s) links are ever rendered as links. */
export function safeHttpUrl(value: string): string | null {
  const v = value.trim()
  if (!v) return null
  try {
    const u = new URL(v)
    return u.protocol === 'https:' || u.protocol === 'http:' ? u.toString() : null
  } catch {
    return null
  }
}

/**
 * Where a finished video task put its result. The relay stores the URL in
 * `fail_reason` on success; on any other status that column is a failure
 * message, never a link.
 */
export function taskResultUrl(log: TaskLog): string | null {
  if (log.status !== TASK_STATUS.SUCCESS) return null
  return safeHttpUrl(log.fail_reason)
}

/** The failure text of a task, or '' when it did not fail. */
export function taskFailReason(log: TaskLog): string {
  if (log.status === TASK_STATUS.SUCCESS) return ''
  return log.fail_reason
}

export interface AudioClip {
  title: string
  audioUrl: string
}

/** Suno-style result: a list (or JSON string of a list) of clips. */
export function audioClips(data: unknown): AudioClip[] {
  let values: unknown[] = []
  if (Array.isArray(data)) {
    values = data
  } else if (typeof data === 'string') {
    try {
      const parsed: unknown = JSON.parse(data)
      values = Array.isArray(parsed) ? parsed : []
    } catch {
      return []
    }
  }
  const out: AudioClip[] = []
  for (const v of values.slice(0, 64)) {
    const c = rec(v)
    const url = safeHttpUrl(str(c.audio_url))
    if (url) out.push({ title: str(c.title), audioUrl: url })
  }
  return out
}

export function pageCountOf(total: number, size: number): number {
  return Math.max(1, Math.ceil(total / size))
}
