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

// ============================================================================
// v1 channel management (GET/POST/PUT /api/channel...). The row is the raw
// channel record with the key column omitted by the server.
// ============================================================================

export interface ChannelInfo {
  is_multi_key: boolean
  multi_key_size: number
  multi_key_status_list?: Record<string, number>
  multi_key_disabled_reason?: Record<string, string>
  multi_key_disabled_time?: Record<string, number>
  multi_key_polling_index: number
  multi_key_mode: 'random' | 'polling'
}

export interface Channel {
  id: number
  type: number
  /** Always empty in list/detail payloads; only a create/edit body carries it. */
  key: string
  openai_organization?: string | null
  test_model?: string | null
  /** 1 enabled, 2 manually disabled, 3 auto-disabled. */
  status: number
  name: string
  weight?: number | null
  created_time: number
  test_time: number
  /** Milliseconds. */
  response_time: number
  base_url?: string | null
  other: string
  /** Upstream account balance in USD (not internal quota). */
  balance: number
  balance_updated_time: number
  models: string
  group: string
  /** Internal quota units; convert with useQuota. */
  used_quota: number
  model_mapping?: string | null
  status_code_mapping?: string | null
  priority?: number | null
  auto_ban?: number | null
  other_info: string
  tag?: string | null
  setting?: string | null
  param_override?: string | null
  header_override?: string | null
  remark?: string | null
  channel_info: ChannelInfo
  /** Other-settings JSON document (azure/vertex/aws members). */
  settings: string
}

/** The members of the `setting` document the console manages. */
export interface ChannelSettings {
  force_format?: boolean
  thinking_to_content?: boolean
  proxy?: string
  pass_through_body_enabled?: boolean
  system_prompt?: string
  system_prompt_override?: boolean
  region?: string
  data_collection?: string
}

/** The members of the `settings` (other settings) document the console manages. */
export interface ChannelOtherSettings {
  azure_responses_version?: string
  vertex_key_type?: 'json' | 'api_key'
  openrouter_enterprise?: boolean
  aws_key_type?: 'ak_sk' | 'api_key'
  allow_service_tier?: boolean
  disable_store?: boolean
  allow_safety_identifier?: boolean
}

/** Server envelope of the v1 handlers (HTTP 200 even on business failure). */
export interface ServerResponse<T = undefined> {
  success: boolean
  message?: string
  error_code?: string
  data?: T
}

export interface ChannelListData {
  items: Channel[]
  total: number
  page?: number
  page_size?: number
  type_counts?: Record<string, number>
}

export type GetChannelsResponse = ServerResponse<ChannelListData>
export type SearchChannelsResponse = ServerResponse<ChannelListData>
export type GetChannelResponse = ServerResponse<Channel>

export interface ChannelTestResponse {
  success: boolean
  message?: string
  error_code?: string
  /** Seconds. */
  time?: number
}

export interface ChannelBalanceResponse {
  success: boolean
  message?: string
  /** USD. */
  balance?: number
}

export type FetchModelsResponse = ServerResponse<string[]>

export type CopyChannelResponse = ServerResponse<{ id: number }>

export interface KeyStatus {
  index: number
  /** 1 enabled, 2 manually disabled, 3 auto-disabled. */
  status: number
  disabled_time?: number
  reason?: string
  key_preview?: string
}

export type MultiKeyConfirmAction = {
  type:
    | 'enable'
    | 'disable'
    | 'delete'
    | 'enable-all'
    | 'disable-all'
    | 'delete-disabled'
  keyIndex?: number
}

export interface MultiKeyStatusData {
  keys: KeyStatus[]
  total: number
  page: number
  page_size: number
  total_pages: number
  enabled_count: number
  manual_disabled_count: number
  auto_disabled_count: number
}

export type MultiKeyStatusResponse = ServerResponse<MultiKeyStatusData>

export interface GetChannelsParams {
  p?: number
  page_size?: number
  /** 'enabled' | 'disabled' | empty for all. */
  status?: string
  type?: number
  group?: string
  id_sort?: boolean
  tag_mode?: boolean
}

export interface SearchChannelsParams {
  keyword?: string
  group?: string
  model?: string
  status?: string
  type?: number
  id_sort?: boolean
  tag_mode?: boolean
  p?: number
  page_size?: number
}

export interface CopyChannelParams {
  suffix?: string
  reset_balance?: boolean
}

export interface MultiKeyManageParams {
  channel_id: number
  action:
    | 'get_key_status'
    | 'disable_key'
    | 'enable_key'
    | 'enable_all_keys'
    | 'disable_all_keys'
    | 'delete_key'
    | 'delete_disabled_keys'
  key_index?: number
  page?: number
  page_size?: number
  status?: number
}

export interface BatchDeleteParams {
  ids: number[]
}

export interface BatchSetTagParams {
  ids: number[]
  tag: string | null
}

export interface TagOperationParams {
  tag: string
  new_tag?: string
  priority?: number
  weight?: number
  model_mapping?: string
  models?: string
  groups?: string
}

export interface AddChannelRequest {
  mode: 'single' | 'batch' | 'multi_to_single'
  multi_key_mode?: 'random' | 'polling'
  batch_add_set_key_prefix_2_name?: boolean
  channel: Partial<Channel>
}
