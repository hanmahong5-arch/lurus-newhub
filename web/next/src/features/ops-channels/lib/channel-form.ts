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
import { z } from 'zod'

import {
  BASE_URL_REQUIRED_TYPES,
  CHANNEL_STATUS,
  CHANNEL_TYPE_AWS,
  CHANNEL_TYPE_OPENROUTER,
  CHANNEL_TYPE_VERTEX,
  ERROR_MESSAGES,
  OTHER_REQUIRED_TYPES,
} from '../constants'
import type { AddChannelRequest, Channel } from '../types'
import { parseChannelOtherSettings, parseChannelSettings } from './channel-utils'
import { formatModelsArray, parseModelsString } from './model-mapping-validation'

// ============================================================================
// Validation
// ============================================================================

const SUPPORTED_PROXY_PROTOCOLS = new Set([
  'http:',
  'https:',
  'socks5:',
  'socks5h:',
])

function isOptionalProxyURL(value: string | undefined): boolean {
  const trimmed = value?.trim() || ''
  if (!trimmed) return true
  const schemeIdx = trimmed.indexOf('://')
  if (schemeIdx <= 0) return false
  const rest = trimmed.slice(schemeIdx + 3)
  const suffixIdx = rest.search(/[/?#]/)
  if (suffixIdx >= 0 && rest.slice(suffixIdx) !== '/') return false
  try {
    const url = new URL(trimmed)
    return (
      SUPPORTED_PROXY_PROTOCOLS.has(url.protocol) &&
      Boolean(url.hostname) &&
      url.port !== '0'
    )
  } catch {
    return false
  }
}

function parseOptionalJson(value: string | undefined): unknown {
  if (!value?.trim()) return undefined
  return JSON.parse(value)
}

function isJsonObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isOptionalJsonObject(value: string | undefined): boolean {
  try {
    const parsed = parseOptionalJson(value)
    return parsed === undefined || isJsonObject(parsed)
  } catch {
    return false
  }
}

function isOptionalModelMapping(value: string | undefined): boolean {
  try {
    const parsed = parseOptionalJson(value)
    if (parsed === undefined) return true
    if (!isJsonObject(parsed)) return false
    return Object.values(parsed).every((item) => typeof item === 'string')
  } catch {
    return false
  }
}

function isOptionalStatusCodeMapping(value: string | undefined): boolean {
  try {
    const parsed = parseOptionalJson(value)
    if (parsed === undefined) return true
    if (!isJsonObject(parsed)) return false
    return Object.entries(parsed).every(([from, to]) => {
      const a = Number(from)
      const b = Number(to)
      return (
        Number.isInteger(a) &&
        Number.isInteger(b) &&
        a >= 100 &&
        a <= 599 &&
        b >= 100 &&
        b <= 599
      )
    })
  } catch {
    return false
  }
}

function isVertexJsonKey(value: string | undefined): boolean {
  try {
    const parsed = parseOptionalJson(value)
    if (parsed === undefined) return true
    if (Array.isArray(parsed)) return parsed.every((item) => isJsonObject(item))
    return isJsonObject(parsed)
  } catch {
    return false
  }
}

function addIssue(ctx: z.RefinementCtx, path: string, message: string): void {
  ctx.addIssue({ code: 'custom', path: [path], message })
}

export const channelFormSchema = z
  .object({
    name: z.string().min(1, ERROR_MESSAGES.REQUIRED_NAME),
    type: z.number().min(1, ERROR_MESSAGES.REQUIRED_TYPE),
    base_url: z.string().optional(),
    key: z.string(),
    openai_organization: z.string().optional(),
    models: z.string().min(1, ERROR_MESSAGES.REQUIRED_MODELS),
    group: z.array(z.string()).min(1, ERROR_MESSAGES.REQUIRED_GROUP),
    model_mapping: z
      .string()
      .optional()
      .refine(
        isOptionalModelMapping,
        'Model mapping must be a JSON object with string values'
      ),
    priority: z.number().optional(),
    weight: z.number().optional(),
    test_model: z.string().optional(),
    auto_ban: z.number().optional(),
    status: z.number(),
    status_code_mapping: z
      .string()
      .optional()
      .refine(
        isOptionalStatusCodeMapping,
        'Status code mapping must use valid HTTP status codes'
      ),
    tag: z.string().optional(),
    remark: z
      .string()
      .max(255, 'Remark must be less than 255 characters')
      .optional(),
    param_override: z
      .string()
      .optional()
      .refine(isOptionalJsonObject, ERROR_MESSAGES.INVALID_JSON),
    header_override: z
      .string()
      .optional()
      .refine(isOptionalJsonObject, ERROR_MESSAGES.INVALID_JSON),
    other: z.string().optional(),
    // Add-mode options (not stored on the channel)
    multi_key_mode: z.enum(['single', 'batch', 'multi_to_single']).optional(),
    multi_key_type: z.enum(['random', 'polling']).optional(),
    batch_add_set_key_prefix_2_name: z.boolean().optional(),
    // Edit-mode option for multi-key channels
    key_mode: z.enum(['append', 'replace']).optional(),
    // `setting` document members
    proxy: z
      .string()
      .optional()
      .refine(isOptionalProxyURL, ERROR_MESSAGES.INVALID_PROXY),
    force_format: z.boolean().optional(),
    thinking_to_content: z.boolean().optional(),
    pass_through_body_enabled: z.boolean().optional(),
    system_prompt: z.string().optional(),
    system_prompt_override: z.boolean().optional(),
    region: z.string().optional(),
    data_collection_deny: z.boolean().optional(),
    // `settings` (other settings) document members
    is_enterprise_account: z.boolean().optional(),
    vertex_key_type: z.enum(['json', 'api_key']).optional(),
    aws_key_type: z.enum(['ak_sk', 'api_key']).optional(),
    azure_responses_version: z.string().optional(),
    allow_service_tier: z.boolean().optional(),
    disable_store: z.boolean().optional(),
    allow_safety_identifier: z.boolean().optional(),
  })
  .superRefine((data, ctx) => {
    if (BASE_URL_REQUIRED_TYPES.has(data.type) && !data.base_url?.trim()) {
      addIssue(ctx, 'base_url', 'Base URL is required for this channel type')
    }
    if (OTHER_REQUIRED_TYPES.has(data.type) && !data.other?.trim()) {
      addIssue(
        ctx,
        'other',
        'This channel type requires additional configuration'
      )
    }
    if (
      data.type === CHANNEL_TYPE_VERTEX &&
      data.vertex_key_type === 'json' &&
      data.key?.trim() &&
      !isVertexJsonKey(data.key)
    ) {
      addIssue(ctx, 'key', 'Vertex AI service account key must be valid JSON')
    }
    if (
      data.type === CHANNEL_TYPE_VERTEX &&
      data.vertex_key_type === 'api_key' &&
      data.multi_key_mode &&
      data.multi_key_mode !== 'single'
    ) {
      addIssue(
        ctx,
        'multi_key_mode',
        'Vertex AI API Key mode does not support batch creation'
      )
    }
  })

export type ChannelFormValues = z.infer<typeof channelFormSchema>

export const CHANNEL_FORM_DEFAULT_VALUES: ChannelFormValues = {
  name: '',
  type: 1,
  base_url: '',
  key: '',
  openai_organization: '',
  models: '',
  group: ['default'],
  model_mapping: '',
  priority: 0,
  weight: 0,
  test_model: '',
  auto_ban: 1,
  status: CHANNEL_STATUS.ENABLED,
  status_code_mapping: '',
  tag: '',
  remark: '',
  param_override: '',
  header_override: '',
  other: '',
  multi_key_mode: 'single',
  multi_key_type: 'random',
  batch_add_set_key_prefix_2_name: false,
  key_mode: 'replace',
  proxy: '',
  force_format: false,
  thinking_to_content: false,
  pass_through_body_enabled: false,
  system_prompt: '',
  system_prompt_override: false,
  region: '',
  data_collection_deny: false,
  is_enterprise_account: false,
  vertex_key_type: 'json',
  aws_key_type: 'ak_sk',
  azure_responses_version: '',
  allow_service_tier: false,
  disable_store: false,
  allow_safety_identifier: false,
}

// ============================================================================
// Channel <-> form
// ============================================================================

/** Form values for an existing channel. The key is never loaded (blank = keep). */
export function transformChannelToFormDefaults(
  channel: Channel
): ChannelFormValues {
  const setting = parseChannelSettings(channel.setting)
  const other = parseChannelOtherSettings(channel.settings)
  return {
    ...CHANNEL_FORM_DEFAULT_VALUES,
    name: channel.name,
    type: channel.type,
    base_url: channel.base_url ?? '',
    key: '',
    openai_organization: channel.openai_organization ?? '',
    models: channel.models ?? '',
    group: (channel.group || 'default')
      .split(',')
      .map((g) => g.trim())
      .filter(Boolean),
    model_mapping: channel.model_mapping ?? '',
    priority: channel.priority ?? 0,
    weight: channel.weight ?? 0,
    test_model: channel.test_model ?? '',
    auto_ban: channel.auto_ban ?? 1,
    status: channel.status,
    status_code_mapping: channel.status_code_mapping ?? '',
    tag: channel.tag ?? '',
    remark: channel.remark ?? '',
    param_override: channel.param_override ?? '',
    header_override: channel.header_override ?? '',
    other: channel.other ?? '',
    multi_key_mode: 'single',
    multi_key_type: channel.channel_info?.multi_key_mode ?? 'random',
    key_mode: 'replace',
    proxy: setting.proxy ?? '',
    force_format: setting.force_format ?? false,
    thinking_to_content: setting.thinking_to_content ?? false,
    pass_through_body_enabled: setting.pass_through_body_enabled ?? false,
    system_prompt: setting.system_prompt ?? '',
    system_prompt_override: setting.system_prompt_override ?? false,
    region: setting.region ?? '',
    data_collection_deny: setting.data_collection === 'deny',
    is_enterprise_account: other.openrouter_enterprise ?? false,
    vertex_key_type: other.vertex_key_type ?? 'json',
    aws_key_type: other.aws_key_type ?? 'ak_sk',
    azure_responses_version: other.azure_responses_version ?? '',
    allow_service_tier: other.allow_service_tier ?? false,
    disable_store: other.disable_store ?? false,
    allow_safety_identifier: other.allow_safety_identifier ?? false,
  }
}

function parseDocument(raw: string | null | undefined): Record<string, unknown> {
  if (!raw) return {}
  try {
    const parsed: unknown = JSON.parse(raw)
    return isJsonObject(parsed) ? parsed : {}
  } catch {
    return {}
  }
}

/**
 * The `setting` document: the members the form manages are written (and
 * dropped when empty); every other member (plan fields, test mode, ...) is
 * carried over untouched so an edit never erases it.
 */
export function buildSettingDocument(
  values: ChannelFormValues,
  existing: string | null | undefined
): string {
  const doc = parseDocument(existing)
  const set = (key: string, value: unknown, keep: boolean) => {
    if (keep) doc[key] = value
    else delete doc[key]
  }
  set('proxy', values.proxy?.trim(), Boolean(values.proxy?.trim()))
  set('force_format', true, values.force_format === true)
  set('thinking_to_content', true, values.thinking_to_content === true)
  set('pass_through_body_enabled', true, values.pass_through_body_enabled === true)
  set(
    'system_prompt',
    values.system_prompt,
    Boolean(values.system_prompt?.trim())
  )
  set(
    'system_prompt_override',
    true,
    values.system_prompt_override === true
  )
  set('region', values.region?.trim(), Boolean(values.region?.trim()))
  set('data_collection', 'deny', values.data_collection_deny === true)
  return JSON.stringify(doc)
}

/** The `settings` document (type-specific members), merged over the existing. */
export function buildOtherSettingsDocument(
  values: ChannelFormValues,
  existing: string | null | undefined
): string {
  const doc = parseDocument(existing)
  const set = (key: string, value: unknown, keep: boolean) => {
    if (keep) doc[key] = value
    else delete doc[key]
  }
  const isVertex = values.type === CHANNEL_TYPE_VERTEX
  const isAws = values.type === CHANNEL_TYPE_AWS
  const isOpenRouter = values.type === CHANNEL_TYPE_OPENROUTER
  set('vertex_key_type', values.vertex_key_type, isVertex)
  set('aws_key_type', values.aws_key_type, isAws)
  set('openrouter_enterprise', true, isOpenRouter && values.is_enterprise_account === true)
  set(
    'azure_responses_version',
    values.azure_responses_version?.trim(),
    values.type === 3 && Boolean(values.azure_responses_version?.trim())
  )
  set('allow_service_tier', true, values.allow_service_tier === true)
  set('disable_store', true, values.disable_store === true)
  set('allow_safety_identifier', true, values.allow_safety_identifier === true)
  return JSON.stringify(doc)
}

function pointerString(value: string | undefined): string {
  return value?.trim() ?? ''
}

type ChannelPayload = Partial<Channel>

function sharedPayload(
  values: ChannelFormValues,
  existing: Channel | null
): ChannelPayload {
  return {
    name: values.name.trim(),
    type: values.type,
    base_url: pointerString(values.base_url),
    openai_organization: pointerString(values.openai_organization),
    models: formatModelsArray(parseModelsString(values.models)),
    group: values.group.join(','),
    model_mapping: pointerString(values.model_mapping),
    priority: values.priority ?? 0,
    weight: values.weight ?? 0,
    test_model: pointerString(values.test_model),
    auto_ban: values.auto_ban ?? 1,
    status: values.status,
    status_code_mapping: pointerString(values.status_code_mapping),
    tag: pointerString(values.tag),
    remark: pointerString(values.remark),
    param_override: pointerString(values.param_override),
    header_override: pointerString(values.header_override),
    other: pointerString(values.other),
    setting: buildSettingDocument(values, existing?.setting),
    settings: buildOtherSettingsDocument(values, existing?.settings),
  }
}

/** Body of POST /api/channel/ for a new channel (or channels, in batch mode). */
export function transformFormDataToCreatePayload(
  values: ChannelFormValues
): AddChannelRequest {
  const mode = values.multi_key_mode ?? 'single'
  const channel: ChannelPayload = {
    ...sharedPayload(values, null),
    key: values.key.trim(),
  }
  return {
    mode,
    ...(mode === 'multi_to_single'
      ? { multi_key_mode: values.multi_key_type ?? 'random' }
      : {}),
    ...(mode === 'batch'
      ? {
          batch_add_set_key_prefix_2_name:
            values.batch_add_set_key_prefix_2_name === true,
        }
      : {}),
    channel,
  }
}

/**
 * Body of PUT /api/channel/ for an edit. A blank key is omitted (the server
 * keeps the stored one); multi-key channels also send the key mode and the
 * polling mode.
 */
export function transformFormDataToUpdatePayload(
  values: ChannelFormValues,
  existing: Channel
): ChannelPayload & { key_mode?: string; multi_key_mode?: string } {
  const payload: ChannelPayload & {
    key_mode?: string
    multi_key_mode?: string
  } = sharedPayload(values, existing)
  const key = values.key.trim()
  if (key) payload.key = key
  if (existing.channel_info?.is_multi_key) {
    payload.multi_key_mode = values.multi_key_type ?? 'random'
    if (key) payload.key_mode = values.key_mode ?? 'replace'
  }
  return payload
}
