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
import type {
  ChannelDetail,
  ChannelHealth,
  ChannelListItem,
  ChannelListResult,
  ChannelUsage,
  ImportOutcome,
  ImportRow,
  KeyHealth,
  KeyProbeResult,
  ModalityMismatch,
  PlanSettings,
  PlanWindow,
  UsageKeyRow,
  UsageTotals,
} from '../types'

function num(value: unknown, fallback = 0): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function rec(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
}

function arr(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

function strList(value: unknown): string[] {
  return arr(value).filter((v): v is string => typeof v === 'string')
}

function intMap(value: unknown): Record<number, number> {
  const out: Record<number, number> = {}
  for (const [k, v] of Object.entries(rec(value))) {
    const idx = Number.parseInt(k, 10)
    if (Number.isInteger(idx) && typeof v === 'number') out[idx] = v
  }
  return out
}

function strMap(value: unknown): Record<number, string> {
  const out: Record<number, string> = {}
  for (const [k, v] of Object.entries(rec(value))) {
    const idx = Number.parseInt(k, 10)
    if (Number.isInteger(idx) && typeof v === 'string') out[idx] = v
  }
  return out
}

/** Parse a JSON document stored in a string column; null when invalid. */
function parseDoc(raw: unknown): Record<string, unknown> | null {
  if (typeof raw !== 'string' || raw.trim() === '') return {}
  try {
    const v: unknown = JSON.parse(raw)
    return typeof v === 'object' && v !== null && !Array.isArray(v)
      ? (v as Record<string, unknown>)
      : null
  } catch {
    return null
  }
}

// ---- channel type names ----------------------------------------------------

const CHANNEL_TYPE_NAMES: Record<number, string> = {
  1: 'OpenAI',
  3: 'Azure',
  4: 'Ollama',
  8: 'Custom',
  14: 'Anthropic',
  15: 'Baidu',
  16: 'Zhipu',
  17: 'Ali',
  18: 'Xunfei',
  20: 'OpenRouter',
  23: 'Tencent',
  24: 'Google AI Studio',
  25: 'Moonshot',
  27: 'Perplexity',
  31: '01.AI',
  33: 'AWS',
  34: 'Cohere',
  35: 'MiniMax',
  36: 'Suno',
  37: 'Dify',
  38: 'Jina',
  40: 'SiliconFlow',
  41: 'Vertex AI',
  42: 'Mistral',
  43: 'DeepSeek',
  45: 'VolcEngine',
  46: 'Baidu V2',
  47: 'Xinference',
  48: 'xAI',
  49: 'Coze',
  50: 'Kling',
  51: 'Jimeng',
  52: 'Vidu',
  56: 'Replicate',
  57: 'TypeSafe',
  58: 'System One',
}

/** Types offered when the import wizard creates new channels. */
export const IMPORT_CHANNEL_TYPES: Array<{ id: number; name: string }> = [
  1, 8, 14, 16, 20, 24, 25, 35, 40, 43, 45, 48,
].map((id) => ({ id, name: CHANNEL_TYPE_NAMES[id] ?? String(id) }))

export function channelTypeName(type: number): string {
  return CHANNEL_TYPE_NAMES[type] ?? `#${type}`
}

// ---- list ------------------------------------------------------------------

function strMapByName(value: unknown): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(rec(value))) {
    if (typeof v === 'string') out[k] = v
  }
  return out
}

function mapMismatches(value: unknown): ModalityMismatch[] {
  return arr(value)
    .map((raw) => {
      const r = rec(raw)
      return { model: str(r.model), reason: str(r.reason) }
    })
    .filter((m) => m.model !== '')
}

/**
 * Distinct modalities on a channel in first-seen model order; unknown ("")
 * modalities are dropped so a channel with no records shows "--".
 */
export const MODALITY_LABELS: Record<string, string> = {
  chat: 'Chat model',
  embedding: 'Embedding',
  rerank: 'Rerank',
  decision: 'Decision',
  image: 'Image model',
  audio: 'Audio',
}

export function channelModalities(c: {
  modelModalities: Record<string, string>
}): string[] {
  const seen: string[] = []
  for (const m of Object.values(c.modelModalities)) {
    if (m !== '' && !seen.includes(m)) seen.push(m)
  }
  return seen
}

export function mapChannelList(data: unknown): ChannelListResult {
  const d = rec(data)
  const items: ChannelListItem[] = arr(d.channels).map((raw) => {
    const r = rec(raw)
    return {
      id: num(r.id),
      name: str(r.name),
      type: num(r.type),
      keyMasked: str(r.key),
      status: num(r.status),
      group: str(r.group),
      models: str(r.models),
      baseUrl: str(r.base_url),
      tag: str(r.tag),
      responseTimeMs: num(r.response_time),
      testTime: num(r.test_time),
      planKind: str(r.plan_kind),
      expiresAt: num(r.expires_at),
      keyCount: num(r.key_count),
      enabledKeyCount: num(r.enabled_key_count),
      routable: r.routable === true,
      unroutableReasons: strList(r.unroutable_reasons),
      modelModalities: strMapByName(r.model_modalities),
      modalityMismatches: mapMismatches(r.modality_mismatches),
    }
  })
  return { items, total: num(d.total, items.length) }
}

// ---- detail / plan ---------------------------------------------------------

export const PLAN_KINDS = ['zhipu_coding', 'kimi_coding', 'minimax'] as const
export const TEST_MODES = ['all', 'auto_ban_only', 'none'] as const

export function parsePlanSettings(
  setting: Record<string, unknown>
): PlanSettings {
  return {
    planKind: str(setting.plan_kind),
    thresholdPct: num(setting.plan_threshold_pct),
    expiresAt: num(setting.expires_at),
    monthlyFeeCny4: num(setting.plan_monthly_fee_cny4),
    testMode: str(setting.test_mode),
  }
}

export function mapWindows(value: unknown): PlanWindow[] {
  return arr(value)
    .map((raw) => rec(raw))
    .filter((w) => typeof w.used_pct === 'number')
    .map((w) => ({
      name: str(w.name),
      usedPct: num(w.used_pct),
      resetAt: num(w.reset_at),
    }))
}

/**
 * The health endpoint's `window` is a free-form map filled by an optional
 * plan-quota snapshot (null when the channel was never probed). Accept `{windows:[...]}` or a single window object.
 */
export function mapHealthWindow(value: unknown): PlanWindow[] {
  const w = rec(value)
  if (Array.isArray(w.windows)) return mapWindows(w.windows)
  if (typeof w.used_pct === 'number') return mapWindows([w])
  return []
}

export function mapChannelDetail(data: unknown): ChannelDetail {
  const d = rec(data)
  const info = rec(d.channel_info)
  const settingRaw = typeof d.setting === 'string' ? d.setting : ''
  const setting = parseDoc(settingRaw) ?? {}
  const other = parseDoc(d.other_info) ?? {}
  const quota = rec(other.plan_quota)
  const multi = info.is_multi_key === true
  return {
    id: num(d.id),
    isMultiKey: multi,
    keyCount: multi ? num(info.multi_key_size) : 1,
    keyProxy: strMap(info.multi_key_proxy),
    keyWeight: intMap(info.multi_key_weight),
    plan: parsePlanSettings(setting),
    settingRaw,
    planWindows: mapWindows(quota.windows),
    planProbeError: str(quota.error),
  }
}

export interface PlanForm {
  planKind: string
  thresholdPct: string
  expiresOn: string
  /** Monthly fee in yuan, as typed. */
  monthlyFeeCny: string
  testMode: string
}

function pad(n: number): string {
  return String(n).padStart(2, '0')
}

function ymd(sec: number): string {
  if (sec <= 0) return ''
  const d = new Date(sec * 1000)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

export function cny4ToYuanText(cny4: number): string {
  if (cny4 <= 0) return ''
  return String(Math.round(cny4) / 10000)
}

export function planToForm(plan: PlanSettings): PlanForm {
  return {
    planKind: plan.planKind,
    thresholdPct: plan.thresholdPct > 0 ? String(plan.thresholdPct) : '',
    expiresOn: ymd(plan.expiresAt),
    monthlyFeeCny: cny4ToYuanText(plan.monthlyFeeCny4),
    testMode: plan.testMode,
  }
}

export type PlanFormError =
  | 'kind'
  | 'threshold'
  | 'expiry'
  | 'fee'
  | 'test-mode'
  | 'setting-corrupt'

export type PlanBuild =
  | { ok: true; setting: string }
  | { ok: false; error: PlanFormError }

/** End of the chosen local day, Unix seconds; NaN when the text is not a date. */
export function expiryToUnix(text: string): number {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(text.trim())
  if (!m) return Number.NaN
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]), 23, 59, 59)
  if (d.getMonth() !== Number(m[2]) - 1) return Number.NaN
  return Math.floor(d.getTime() / 1000)
}

/**
 * Merge the form into the existing `setting` document. Members this page does
 * not own (proxy, system prompt, ...) are preserved; cleared plan members are
 * removed. A corrupt stored document is refused rather than overwritten.
 */
export function buildSettingJson(raw: string, form: PlanForm): PlanBuild {
  const doc = parseDoc(raw)
  if (doc === null) return { ok: false, error: 'setting-corrupt' }
  const next: Record<string, unknown> = { ...doc }

  const kind = form.planKind.trim()
  if (
    kind !== '' &&
    kind !== 'none' &&
    !(PLAN_KINDS as readonly string[]).includes(kind)
  ) {
    return { ok: false, error: 'kind' }
  }
  if (kind === '' || kind === 'none') delete next.plan_kind
  else next.plan_kind = kind

  const th = form.thresholdPct.trim()
  if (th === '') delete next.plan_threshold_pct
  else {
    const n = Number(th)
    if (!Number.isFinite(n) || n <= 0 || n > 100) {
      return { ok: false, error: 'threshold' }
    }
    next.plan_threshold_pct = n
  }

  const exp = form.expiresOn.trim()
  if (exp === '') delete next.expires_at
  else {
    const sec = expiryToUnix(exp)
    if (!Number.isFinite(sec)) return { ok: false, error: 'expiry' }
    next.expires_at = sec
  }

  const fee = form.monthlyFeeCny.trim()
  if (fee === '') delete next.plan_monthly_fee_cny4
  else {
    const n = Number(fee)
    if (!Number.isFinite(n) || n < 0) return { ok: false, error: 'fee' }
    const cny4 = Math.round(n * 10000)
    if (cny4 === 0) delete next.plan_monthly_fee_cny4
    else next.plan_monthly_fee_cny4 = cny4
  }

  const mode = form.testMode.trim()
  if (mode !== '' && !(TEST_MODES as readonly string[]).includes(mode)) {
    return { ok: false, error: 'test-mode' }
  }
  if (mode === '') delete next.test_mode
  else next.test_mode = mode

  return { ok: true, setting: JSON.stringify(next) }
}

// ---- health ----------------------------------------------------------------

function nullableTs(value: unknown): number | null {
  return typeof value === 'number' && value > 0 ? value : null
}

function mapKeyHealth(raw: unknown): KeyHealth {
  const k = rec(raw)
  return {
    index: num(k.index),
    name: str(k.name),
    fingerprint: str(k.fingerprint),
    routable: k.routable === true,
    reasons: strList(k.reasons),
    cooldownUntil: num(k.cooldown_until),
    windows: mapHealthWindow(k.window),
    lastError: str(k.last_error),
    lastSuccessAt: nullableTs(k.last_success_at),
    weight: typeof k.weight === 'number' ? k.weight : null,
    hasProxy: k.has_proxy === true,
  }
}

export function mapChannelHealth(data: unknown): ChannelHealth {
  const h = rec(data)
  return {
    channelId: num(h.channel_id),
    routable: h.routable === true,
    reasons: strList(h.reasons),
    cooldownUntil: num(h.cooldown_until),
    windows: mapHealthWindow(h.window),
    lastError: str(h.last_error),
    lastSuccessAt: nullableTs(h.last_success_at),
    keys: arr(h.keys).map(mapKeyHealth),
  }
}

/** English source strings for the stable reason codes (translated by i18n). */
export const REASON_LABELS: Record<string, string> = {
  disabled_manual: 'Disabled manually',
  auto_disabled: 'Auto-disabled',
  cooling_429: 'Cooling down after rate limit (429)',
  plan_window_exhausted: 'Plan window used up',
  expired: 'Plan expired',
  balance_low: 'Insufficient balance',
  auth_failed: 'Authentication failed',
}

/** Remaining time as `1h 05m` / `4m 07s` / `12s`; `0s` once elapsed. */
export function formatCountdown(untilSec: number, nowSec: number): string {
  const left = Math.max(0, Math.floor(untilSec - nowSec))
  const h = Math.floor(left / 3600)
  const m = Math.floor((left % 3600) / 60)
  const s = left % 60
  if (h > 0) return `${h}h ${pad(m)}m`
  if (m > 0) return `${m}m ${pad(s)}s`
  return `${s}s`
}

export function mapKeyProbe(data: unknown): KeyProbeResult {
  const r = rec(data)
  return {
    ok: r.ok === true,
    latencyMs: num(r.latency_ms),
    error: str(r.error),
    keyIndex: num(r.key_index),
    fingerprint: str(r.fingerprint),
    restoreProof: str(r.restore_proof),
    restoreProofExpiresAt: num(r.restore_proof_expires_at),
  }
}

/** A probe result may be used to restore only while its proof is fresh. */
export function canRestore(p: KeyProbeResult, nowSec: number): boolean {
  return p.ok && p.restoreProof !== '' && p.restoreProofExpiresAt > nowSec
}

// ---- usage -----------------------------------------------------------------

function mapTotals(raw: unknown): UsageTotals {
  const t = rec(raw)
  return {
    requests: num(t.requests),
    errors: num(t.errors),
    promptTokens: num(t.prompt_tokens),
    completionTokens: num(t.completion_tokens),
    quota: num(t.quota),
    costCny4: num(t.cost_cny4),
  }
}

export function mapChannelUsage(data: unknown): ChannelUsage {
  const d = rec(data)
  const keys: UsageKeyRow[] = arr(d.keys).map((raw) => ({
    ...mapTotals(raw),
    keyIdx: num(rec(raw).key_idx, -1),
  }))
  return {
    window: str(d.window),
    keys,
    total: mapTotals(d.total),
    planMonthlyFeeCny4: num(d.plan_monthly_fee_cny4),
    utilization: typeof d.utilization === 'number' ? d.utilization : null,
    proratedFeeCny4: num(d.prorated_fee_cny4),
  }
}

/** Amounts the server keeps in 0.0001 CNY (not quota units, so not money.ts). */
export function formatCny4(cny4: number | null | undefined): string {
  if (typeof cny4 !== 'number' || !Number.isFinite(cny4)) return '--'
  return `¥${(cny4 / 10000).toFixed(2)}`
}

export function formatPercent(ratio: number): string {
  return `${(ratio * 100).toFixed(ratio >= 10 ? 0 : 1)}%`
}

// ---- import ----------------------------------------------------------------

export const IMPORT_CODE_LABELS: Record<string, string> = {
  empty_key: 'Empty key',
  invalid_weight: 'Weight must be between 0 and 100',
  invalid_expires_at: 'Invalid expiry time',
  proxy_rejected: 'Proxy address rejected by the safety check',
  models_required: 'Models are required',
  base_url_rejected: 'Base URL rejected by the safety check',
  duplicate_existing_key: 'Already exists in this workspace',
  duplicate_in_batch: 'Repeated in this batch',
  probe_failed: 'Key test failed',
  probe_cancelled: 'Test cancelled',
}

function mapImportRow(raw: unknown): ImportRow {
  const r = rec(raw)
  const probe = r.probe == null ? null : rec(r.probe)
  return {
    index: num(r.index),
    name: str(r.name),
    status: str(r.status),
    errorCode: str(r.error_code),
    fingerprint: str(r.fingerprint),
    existingChannelId: num(r.existing_channel_id),
    probe: probe
      ? {
          ok: probe.ok === true,
          latencyMs: num(probe.latency_ms),
          error: str(probe.error),
        }
      : null,
    imported: r.imported === true,
    channelId: num(r.channel_id),
    keyIndex: typeof r.key_index === 'number' ? r.key_index : null,
  }
}

export function mapImportOutcome(data: unknown): ImportOutcome {
  const d = rec(data)
  const summary: Record<string, number> = {}
  for (const [k, v] of Object.entries(rec(d.summary))) summary[k] = num(v)
  return {
    dryRun: d.dry_run !== false,
    summary,
    rows: arr(d.results).map(mapImportRow),
  }
}

/** Client-side check of the pasted text; mirrors the server's two shapes. */
export function checkImportInput(text: string): 'empty' | 'bad-json' | 'ok' {
  const raw = text.trim()
  if (raw === '') return 'empty'
  if (raw.startsWith('[')) {
    try {
      const v: unknown = JSON.parse(raw)
      return Array.isArray(v) && v.length > 0 ? 'ok' : 'empty'
    } catch {
      return 'bad-json'
    }
  }
  return 'ok'
}

/** Number of keys the text will become (lines, or JSON array length). */
export function countImportItems(text: string): number {
  const raw = text.trim()
  if (raw === '') return 0
  if (raw.startsWith('[')) {
    try {
      const v: unknown = JSON.parse(raw)
      return Array.isArray(v) ? v.length : 0
    } catch {
      return 0
    }
  }
  return raw.split('\n').filter((l) => l.trim() !== '').length
}

export const MAX_IMPORT_ITEMS = 500
