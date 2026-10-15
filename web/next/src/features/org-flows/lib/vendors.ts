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
 * Vendor picker of the "New channel" wizard. `type` is the numeric
 * repo.Channel.Type the backend persists (the same catalogue as the Channels
 * page); picking a vendor sets the type and, when VENDOR_BASE_URLS carries
 * one, prefills the base url.
 */
export type VendorHint = 'oai' | 'native' | 'custom'

export interface VendorChoice {
  key: string
  type: number
  /** Proper vendor name; null = the generic "Custom" card (translated). */
  label: string | null
  hint: VendorHint
}

export const VENDOR_CHOICES: readonly VendorChoice[] = [
  { key: 'openai', type: 1, label: 'OpenAI', hint: 'oai' },
  { key: 'anthropic', type: 14, label: 'Anthropic', hint: 'native' },
  { key: 'vertex', type: 41, label: 'Google Vertex', hint: 'native' },
  { key: 'azure', type: 3, label: 'Azure', hint: 'native' },
  { key: 'bedrock', type: 33, label: 'AWS Bedrock', hint: 'native' },
  { key: 'zhipu', type: 26, label: 'Zhipu', hint: 'native' },
  { key: 'siliconflow', type: 40, label: 'SiliconCloud', hint: 'oai' },
  { key: 'custom', type: 8, label: null, hint: 'custom' },
]

/**
 * Default upstream host a vendor card prefills. OpenAI is deliberately blank
 * (the server falls back to its own default); the four vendors in
 * NO_DEFAULT_BASE_URL_TYPES have none anywhere.
 */
export const VENDOR_BASE_URLS: Readonly<Record<number, string>> = {
  1: '',
  14: 'https://api.anthropic.com',
  26: 'https://open.bigmodel.cn',
  40: 'https://api.siliconflow.cn',
}

/**
 * Types with no default upstream host on the server (constant.ChannelBaseURLs
 * holds "" for 3, 8, 33, 41). Leaving base_url blank would create a channel
 * whose discovery and test both answer 400 "Channel has no base URL
 * configured" and that the relay can never route through, so the wizard blocks
 * the create instead.
 */
export const NO_DEFAULT_BASE_URL_TYPES: ReadonlySet<number> = new Set([
  3, 8, 33, 41,
])

/** Google Vertex needs a region document in the channel's `other` field. */
export const VERTEX_TYPE = 41
/** Azure takes its API version in `other` (optional). */
export const AZURE_TYPE = 3

export function presetBaseUrl(type: number): string {
  return VENDOR_BASE_URLS[type] ?? ''
}
