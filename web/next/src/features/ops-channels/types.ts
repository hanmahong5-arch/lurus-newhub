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
/** Channel status codes (common.ChannelStatus*). */
export const CHANNEL_STATUS_ENABLED = 1
export const CHANNEL_STATUS_MANUAL_DISABLED = 2
export const CHANNEL_STATUS_AUTO_DISABLED = 3

/** One row of GET /channels (handler channelView, key already masked). */
export interface ChannelListItem {
  id: number
  name: string
  type: number
  keyMasked: string
  status: number
  group: string
  models: string
  baseUrl: string
  tag: string
  responseTimeMs: number
  testTime: number
  /** Declared plan kind ("" = pay-as-you-go), from the list payload. */
  planKind: string
  /** Plan end, Unix seconds (0 = never). */
  expiresAt: number
  keyCount: number
  enabledKeyCount: number
  /** Same verdict as GET /channels/:id/health. */
  routable: boolean
  unroutableReasons: string[]
  /** model -> modality as stored on the channel's abilities ("" = unknown). */
  modelModalities: Record<string, string>
  /** Models the backend flags: modality_conflict | adapter_unsupported. */
  modalityMismatches: ModalityMismatch[]
}

export interface ModalityMismatch {
  model: string
  reason: string
}

export interface ChannelListResult {
  items: ChannelListItem[]
  total: number
}

/** Plan fields stored in the channel's `setting` JSON document. */
export interface PlanSettings {
  planKind: string
  thresholdPct: number
  expiresAt: number
  /** Monthly fee in 0.0001 CNY (server unit). */
  monthlyFeeCny4: number
  testMode: string
}

export interface PlanWindow {
  name: string
  usedPct: number
  resetAt: number
}

/** GET /channels/:id, reduced to what this page shows. */
export interface ChannelDetail {
  id: number
  isMultiKey: boolean
  keyCount: number
  /** key index -> proxy URL override. */
  keyProxy: Record<number, string>
  /** key index -> explicit weight. */
  keyWeight: Record<number, number>
  plan: PlanSettings
  /** Raw `setting` document (kept so a plan save does not drop other members). */
  settingRaw: string
  /** Last plan-quota probe windows from other_info.plan_quota. */
  planWindows: PlanWindow[]
  planProbeError: string
}

export interface KeyHealth {
  index: number
  name: string
  fingerprint: string
  routable: boolean
  reasons: string[]
  cooldownUntil: number
  windows: PlanWindow[]
  lastError: string
  lastSuccessAt: number | null
  weight: number | null
  hasProxy: boolean
}

export interface ChannelHealth {
  channelId: number
  routable: boolean
  reasons: string[]
  cooldownUntil: number
  windows: PlanWindow[]
  lastError: string
  lastSuccessAt: number | null
  keys: KeyHealth[]
}

export interface KeyProbeResult {
  ok: boolean
  latencyMs: number
  error: string
  keyIndex: number
  fingerprint: string
  restoreProof: string
  restoreProofExpiresAt: number
}

export const USAGE_WINDOWS = ['5h', '24h', '7d', '30d'] as const
export type UsageWindow = (typeof USAGE_WINDOWS)[number]

export interface UsageTotals {
  requests: number
  errors: number
  promptTokens: number
  completionTokens: number
  quota: number
  costCny4: number
}

export interface UsageKeyRow extends UsageTotals {
  /** -1 = requests with no key index (single-key channel / old rows). */
  keyIdx: number
}

export interface ChannelUsage {
  window: string
  keys: UsageKeyRow[]
  total: UsageTotals
  planMonthlyFeeCny4: number
  /** null when the channel has no plan fee. */
  utilization: number | null
  proratedFeeCny4: number
}

export type ImportStatus = 'ready' | 'duplicate' | 'invalid' | 'probe_failed'

export interface ImportRow {
  index: number
  name: string
  status: ImportStatus | string
  errorCode: string
  fingerprint: string
  existingChannelId: number
  probe: { ok: boolean; latencyMs: number; error: string } | null
  imported: boolean
  channelId: number
  keyIndex: number | null
}

export interface ImportOutcome {
  dryRun: boolean
  summary: Record<string, number>
  rows: ImportRow[]
}

export interface ImportRequest {
  dry_run: boolean
  probe: boolean
  keys: string
  channel_id?: number
  type?: number
  group?: string
  base_url?: string
  models?: string
  proxy?: string
}
