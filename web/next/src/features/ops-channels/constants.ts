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
// ============================================================================
// Channel types. Labels are i18n keys or proper names; use t(value) to show.
// Only types the relay can serve are listed; anything else renders as
// "Type #N" in the table so an unknown row is never hidden.
// ============================================================================

export const CHANNEL_TYPE_OLLAMA = 4
export const CHANNEL_TYPE_VERTEX = 41
export const CHANNEL_TYPE_AWS = 33
export const CHANNEL_TYPE_OPENROUTER = 20

export const CHANNEL_TYPES: Record<number, string> = {
  1: 'OpenAI',
  3: 'Azure',
  4: 'Ollama',
  8: 'Custom',
  14: 'Anthropic',
  15: 'Baidu',
  16: 'Zhipu',
  17: 'Ali',
  18: 'Xunfei',
  19: '360',
  20: 'OpenRouter',
  23: 'Tencent',
  24: 'Google AI Studio',
  25: 'Moonshot',
  26: 'Zhipu V4',
  27: 'Perplexity',
  31: '01.AI',
  33: 'AWS',
  34: 'Cohere',
  35: 'MiniMax',
  36: 'Suno',
  37: 'Dify',
  38: 'Jina',
  39: 'Cloudflare',
  40: 'SiliconFlow',
  41: 'Vertex AI',
  42: 'Mistral',
  43: 'DeepSeek',
  44: 'MokaAI',
  45: 'VolcEngine',
  46: 'Baidu V2',
  47: 'Xinference',
  48: 'xAI',
  49: 'Coze',
  50: 'Kling',
  51: 'Jimeng',
  52: 'Vidu',
  53: 'Submodel',
  54: 'Volcengine Video',
  55: 'OpenAI Video',
  56: 'Replicate',
  57: 'TypeSafe',
  58: 'System One',
  59: 'Voyage',
}

export type ChannelProviderPresentation = {
  descriptionKey: string
  badge?: { labelKey: string; tone: 'warning' | 'primary' }
}

/** Display copy only; routing and availability do not depend on it. */
export const CHANNEL_PROVIDER_PRESENTATION: Partial<
  Record<number, ChannelProviderPresentation>
> = {
  1: { descriptionKey: 'Connect to the OpenAI API or compatible services' },
  3: { descriptionKey: 'Connect to OpenAI models deployed on Azure' },
  4: { descriptionKey: 'Connect to local or self-hosted Ollama models' },
  8: {
    descriptionKey: 'Full-URL integration for any OpenAI-compatible service',
    badge: { labelKey: 'Advanced', tone: 'warning' },
  },
  14: { descriptionKey: 'Connect to the Anthropic API or compatible services' },
  15: { descriptionKey: 'Access Baidu Qianfan models through the legacy API' },
  16: { descriptionKey: 'Access Zhipu models through the legacy API' },
  17: { descriptionKey: 'Connect to Alibaba Cloud Bailian model services' },
  18: { descriptionKey: 'Connect to iFlytek model services' },
  19: { descriptionKey: 'Connect to 360 model services' },
  20: {
    descriptionKey: 'Access models from multiple providers through OpenRouter',
  },
  23: { descriptionKey: 'Connect to Tencent model services' },
  24: { descriptionKey: 'Connect to models through the Google AI Studio API' },
  25: { descriptionKey: 'Connect to Moonshot AI model services' },
  26: { descriptionKey: 'Access Zhipu models through the V4 API' },
  27: { descriptionKey: 'Connect to Perplexity model services' },
  31: { descriptionKey: 'Connect to 01.AI model services' },
  33: { descriptionKey: 'Access models through Amazon Bedrock' },
  34: { descriptionKey: 'Connect to Cohere model services' },
  35: { descriptionKey: 'Connect to MiniMax model services' },
  36: { descriptionKey: 'Generate music and lyrics through Suno' },
  37: { descriptionKey: 'Connect to Dify applications and workflows' },
  38: { descriptionKey: 'Connect to Jina embedding and reranking services' },
  39: { descriptionKey: 'Access models through Cloudflare Workers AI' },
  40: { descriptionKey: 'Connect to SiliconFlow model inference services' },
  41: { descriptionKey: 'Access models through Google Cloud Vertex AI' },
  42: { descriptionKey: 'Connect to Mistral AI model services' },
  43: { descriptionKey: 'Connect to DeepSeek model services' },
  44: { descriptionKey: 'Access model services through MokaAI' },
  45: { descriptionKey: 'Connect to Volcengine Ark model services' },
  46: { descriptionKey: 'Access Baidu Qianfan models through the V2 API' },
  47: { descriptionKey: 'Connect to self-hosted models served by Xinference' },
  48: { descriptionKey: 'Connect to xAI model services' },
  49: { descriptionKey: 'Connect to Coze bots' },
  50: { descriptionKey: 'Connect to Kling video generation services' },
  51: {
    descriptionKey: 'Connect to Jimeng image and video generation services',
  },
  52: { descriptionKey: 'Connect to Vidu video generation services' },
  53: { descriptionKey: 'Connect to Submodel model services' },
  54: { descriptionKey: 'Connect to video generation through Volcengine' },
  55: { descriptionKey: 'Connect to OpenAI video generation services' },
  56: { descriptionKey: 'Access hosted model predictions through Replicate' },
  57: { descriptionKey: 'Connect to the hosted TypeSafe decision service' },
  58: {
    descriptionKey: 'Connect to a self-hosted System One compatible server',
  },
  59: { descriptionKey: 'Connect to Voyage embedding and reranking services' },
}

const CHANNEL_TYPE_DISPLAY_ORDER: number[] = [
  1, 14, 24, 33, 43, 3, 41, 17, 45, 25, 26, 23, 48, 42, 34, 20, 4, 40, 27, 15,
  46, 18, 31, 35, 49, 19, 47, 37, 38, 39, 8, 44, 53, 59, 57, 58, 36, 50, 51, 52,
  54, 55, 56,
]

export const CHANNEL_TYPE_OPTIONS: { value: number; label: string }[] = (() => {
  const ordered: { value: number; label: string }[] = []
  const seen = new Set<number>()
  for (const id of CHANNEL_TYPE_DISPLAY_ORDER) {
    const label = CHANNEL_TYPES[id]
    if (label) {
      ordered.push({ value: id, label })
      seen.add(id)
    }
  }
  for (const [key, label] of Object.entries(CHANNEL_TYPES)) {
    const id = Number(key)
    if (!seen.has(id)) ordered.push({ value: id, label })
  }
  return ordered
})()

/** Brand icon per channel type (vendor level; resolved by lobe-icon). */
export const CHANNEL_TYPE_ICONS: Record<number, string> = {
  1: 'OpenAI',
  3: 'Azure',
  4: 'Ollama',
  8: 'OpenAI',
  14: 'Anthropic',
  15: 'Baidu',
  16: 'Zhipu',
  17: 'Alibaba',
  18: 'IFlyTekCloud',
  19: 'Ai360',
  20: 'OpenRouter',
  23: 'Tencent',
  24: 'Google',
  25: 'Moonshot',
  26: 'Zhipu',
  27: 'Perplexity',
  31: 'Yi',
  33: 'Aws',
  34: 'Cohere',
  35: 'Minimax',
  36: 'Suno',
  37: 'Dify',
  38: 'Jina',
  39: 'Cloudflare',
  40: 'SiliconCloud',
  41: 'VertexAI',
  42: 'Mistral',
  43: 'DeepSeek',
  44: 'OpenAI',
  45: 'Volcengine',
  46: 'Baidu',
  47: 'Xinference',
  48: 'XAI',
  49: 'Coze',
  50: 'Kling',
  51: 'Jimeng',
  52: 'Vidu',
  53: 'SubModel',
  54: 'ByteDance',
  55: 'OpenAI',
  56: 'Replicate',
  59: 'Voyage',
}

/**
 * Provider default base URLs (mirror of the server's ChannelBaseURLs). The
 * server has no endpoint for them, and it falls back to the same value when
 * base_url is empty, so this is a hint only.
 */
export const CHANNEL_DEFAULT_BASE_URLS: Record<number, string> = {
  1: 'https://api.openai.com',
  4: 'http://localhost:11434',
  14: 'https://api.anthropic.com',
  15: 'https://aip.baidubce.com',
  16: 'https://open.bigmodel.cn',
  17: 'https://dashscope.aliyuncs.com',
  19: 'https://api.360.cn',
  20: 'https://openrouter.ai/api',
  23: 'https://hunyuan.tencentcloudapi.com',
  24: 'https://generativelanguage.googleapis.com',
  25: 'https://api.moonshot.cn',
  26: 'https://open.bigmodel.cn',
  27: 'https://api.perplexity.ai',
  31: 'https://api.lingyiwanwu.com',
  34: 'https://api.cohere.ai',
  35: 'https://api.minimax.chat',
  37: 'https://api.dify.ai',
  38: 'https://api.jina.ai',
  39: 'https://api.cloudflare.com',
  40: 'https://api.siliconflow.cn',
  42: 'https://api.mistral.ai',
  43: 'https://api.deepseek.com',
  44: 'https://api.moka.ai',
  45: 'https://ark.cn-beijing.volces.com',
  46: 'https://qianfan.baidubce.com',
  48: 'https://api.x.ai',
  49: 'https://api.coze.cn',
  50: 'https://api.klingai.com',
  51: 'https://visual.volcengineapi.com',
  52: 'https://api.vidu.cn',
  53: 'https://llm.submodel.ai',
  54: 'https://ark.cn-beijing.volces.com',
  55: 'https://api.openai.com',
  56: 'https://api.replicate.com',
  57: 'https://api.typesafe.ai',
  59: 'https://api.voyageai.com',
}

/** Types that cannot run without an explicit base URL. */
export const BASE_URL_REQUIRED_TYPES = new Set([3, 8, 36, 45, 58])

/** Types whose `other` field must be filled (region, deployment, ...). */
export const OTHER_REQUIRED_TYPES = new Set([3, 18, 39, 41, 49])

// ============================================================================
// Channel status
// ============================================================================

export const CHANNEL_STATUS = {
  UNKNOWN: 0,
  ENABLED: 1,
  MANUAL_DISABLED: 2,
  AUTO_DISABLED: 3,
} as const

export const CHANNEL_STATUS_OPTIONS = [
  { value: 'all', label: 'All Status' },
  { value: 'enabled', label: 'Enabled' },
  { value: 'disabled', label: 'Disabled' },
] as const

export const CHANNEL_STATUS_CONFIG = {
  [CHANNEL_STATUS.UNKNOWN]: { variant: 'neutral' as const, label: 'Unknown' },
  [CHANNEL_STATUS.ENABLED]: { variant: 'success' as const, label: 'Enabled' },
  [CHANNEL_STATUS.MANUAL_DISABLED]: {
    variant: 'danger' as const,
    label: 'Disabled',
  },
  [CHANNEL_STATUS.AUTO_DISABLED]: {
    variant: 'warning' as const,
    label: 'Auto Disabled',
  },
}

// ============================================================================
// Multi-key
// ============================================================================

export const MULTI_KEY_STATUS = {
  ENABLED: 1,
  MANUAL_DISABLED: 2,
  AUTO_DISABLED: 3,
} as const

export const MULTI_KEY_STATUS_CONFIG = {
  [MULTI_KEY_STATUS.ENABLED]: { variant: 'success' as const, label: 'Enabled' },
  [MULTI_KEY_STATUS.MANUAL_DISABLED]: {
    variant: 'neutral' as const,
    label: 'Manual Disabled',
  },
  [MULTI_KEY_STATUS.AUTO_DISABLED]: {
    variant: 'danger' as const,
    label: 'Auto Disabled',
  },
}

export const MULTI_KEY_MODES = [
  { value: 'random', label: 'Random' },
  { value: 'polling', label: 'Polling' },
] as const

export const ADD_MODE_OPTIONS = [
  { value: 'single', label: 'Single Key' },
  { value: 'batch', label: 'Batch Add (one key per line)' },
  {
    value: 'multi_to_single',
    label: 'Multi-Key Mode (multiple keys, one channel)',
  },
] as const

export const MULTI_KEY_FILTER_OPTIONS = [
  { value: 'all', label: 'All Status' },
  { value: '1', label: 'Enabled' },
  { value: '2', label: 'Manual Disabled' },
  { value: '3', label: 'Auto Disabled' },
] as const

export const MULTI_KEY_CONFIRM_MESSAGES = {
  DELETE:
    'Are you sure you want to delete this key? This action cannot be undone.',
  ENABLE: 'Enable this key?',
  DISABLE: 'Disable this key?',
  ENABLE_ALL: 'Are you sure you want to enable all keys?',
  DISABLE_ALL: 'Are you sure you want to disable all enabled keys?',
  DELETE_DISABLED:
    'Are you sure you want to delete all auto-disabled keys? This action cannot be undone.',
} as const

// ============================================================================
// Messages (i18n keys)
// ============================================================================

export const ERROR_MESSAGES = {
  REQUIRED_NAME: 'Channel name is required',
  REQUIRED_TYPE: 'Channel type is required',
  REQUIRED_KEY: 'API key is required',
  REQUIRED_MODELS: 'Models are required',
  REQUIRED_GROUP: 'Group is required',
  INVALID_JSON: 'Invalid JSON format',
  INVALID_MODEL_MAPPING: 'Invalid model mapping format',
  INVALID_PROXY:
    'Proxy address must use HTTP, HTTPS, SOCKS5, or SOCKS5H and include a valid host',
  CREATE_FAILED: 'Failed to create channel',
  UPDATE_FAILED: 'Failed to update channel',
  DELETE_FAILED: 'Failed to delete channel',
  TEST_FAILED: 'Failed to test channel',
  BALANCE_QUERY_FAILED: 'Failed to query balance',
  FETCH_MODELS_FAILED: 'Failed to fetch models',
} as const

export const SUCCESS_MESSAGES = {
  CREATED: 'Channel created successfully',
  UPDATED: 'Channel updated successfully',
  DELETED: 'Channel deleted successfully',
  ENABLED: 'Channel enabled successfully',
  DISABLED: 'Channel disabled successfully',
  TESTED: 'Channel test completed',
  BALANCE_QUERIED: 'Balance queried successfully',
  MODELS_FETCHED: 'Models fetched successfully',
  COPIED: 'Channel copied successfully',
  TAG_SET: 'Tag set successfully',
  BATCH_DELETED: 'Channels deleted successfully',
} as const

// ============================================================================
// Defaults and table configuration
// ============================================================================

export const DEFAULT_PAGE_SIZE = 20

// ============================================================================
// Response time thresholds (ms)
// ============================================================================

export const RESPONSE_TIME_THRESHOLDS = {
  EXCELLENT: 500,
  GOOD: 1000,
  FAIR: 2000,
  POOR: 5000,
} as const

export const RESPONSE_TIME_CONFIG = {
  EXCELLENT: { variant: 'success' as const, label: 'Excellent' },
  GOOD: { variant: 'success' as const, label: 'Good' },
  FAIR: { variant: 'warning' as const, label: 'Fair' },
  POOR: { variant: 'danger' as const, label: 'Poor' },
  UNKNOWN: { variant: 'neutral' as const, label: 'Not tested' },
} as const

// ============================================================================
// Field hints and placeholders (i18n keys)
// ============================================================================

export const FIELD_PLACEHOLDERS = {
  NAME: 'e.g., Primary account',
  BASE_URL: 'Leave empty to use default',
  KEY: 'API Key (one per line for batch mode)',
  MODELS: 'Comma-separated model names, e.g., model-a,model-b',
  GROUP: 'Please Select user groups that can access this channel.',
  MODEL_MAPPING: '{"request_model": "actual_model"}',
  TEST_MODEL: 'Model to use for testing',
  TAG: 'Optional tag for grouping channels',
  REMARK: 'Optional notes about this channel',
  PARAM_OVERRIDE: '{"temperature": 0.7}',
  HEADER_OVERRIDE: '{"X-Custom-Header": "value"}',
  STATUS_CODE_MAPPING: '{"400": "500"}',
} as const

export const FIELD_DESCRIPTIONS = {
  NAME: 'Friendly name to identify this channel',
  TYPE: 'Provider type (OpenAI, Anthropic, etc.)',
  BASE_URL: 'Custom API base URL. Leave empty to use provider default.',
  KEY: 'API key from the provider',
  MODELS:
    'List of models supported by this channel. Use comma to separate multiple models.',
  GROUP: 'User groups that can access this channel. ',
  MODEL_MAPPING:
    'For this channel, map the model name in client requests to the model name sent upstream.',
  PRIORITY: 'Higher priority channels are selected first',
  WEIGHT: 'Used for load balancing. Higher weight = more requests',
  TEST_MODEL: 'Model to use when testing channel connectivity',
  AUTO_BAN: 'Automatically disable channel on repeated failures',
  STATUS_CODE_MAPPING: 'Map response status codes (JSON format)',
  TAG: 'Group channels by tag for batch operations',
  REMARK: 'Internal notes (not shown to users)',
  PARAM_OVERRIDE: 'Override request parameters (JSON format)',
  HEADER_OVERRIDE: 'Override request headers (JSON format)',
  MULTI_KEY_MODE: 'How to select keys: random or sequential polling',
  BATCH_ADD: 'Create multiple channels from multiple keys',
  OPENAI_ORG: 'OpenAI Organization ID (optional)',
} as const

// ============================================================================
// Type-specific behaviour
// ============================================================================

/** Types whose upstream exposes a model list the server can fetch. */
export const MODEL_FETCHABLE_TYPES = new Set([
  1, 4, 14, 17, 20, 23, 24, 25, 26, 27, 31, 34, 35, 40, 42, 43, 45, 47, 48,
])

/** Providers that accept the extra OpenAI-style passthrough toggles. */
export const OPENAI_FIELD_PASSTHROUGH_TYPES = new Set([1])

export const TYPE_TO_KEY_PROMPT: Record<number, string> = {
  15: 'Format: APIKey|SecretKey',
  18: 'Format: APPID|APISecret|APIKey',
  22: 'Format: APIKey-AppId',
  23: 'Format: TokenHub API Key, or legacy AppId|SecretId|SecretKey',
  33: 'Format: Ak|Sk|Region',
  50: 'Format: AccessKey|SecretKey',
  51: 'Format: Access Key ID|Secret Access Key',
}

export const CHANNEL_TYPE_WARNINGS: Record<number, string> = {
  3: 'For channels added after May 10, 2025, no need to remove "." from model names during deployment',
  8: 'If connecting to an upstream relay project, use the OpenAI type instead unless you know what you are doing',
  37: 'Dify channels only support chatflow and agent, and agent does not support images',
}
