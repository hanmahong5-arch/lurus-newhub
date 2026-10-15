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
/** Wire shapes the flows wizards read (handler/v2_channel*.go, v2_token.go). */

/** POST /api/v2/~/channels: `data` of the 201 answer. */
export interface CreatedChannel {
  id: number
  name: string
}

/** GET /api/v2/~/channels/:id/upstream-models: `data`. */
export interface UpstreamModels {
  /** Every model id the upstream serves. */
  upstream: string[]
  /** The subset not yet configured on the channel. */
  new: string[]
  /** Configured on the channel but no longer served upstream. */
  missing: string[]
}

/**
 * POST /api/v2/~/channels/:id/test. Unlike every other endpoint this answers
 * HTTP 200 with the result at the TOP LEVEL of the body (no `data` wrapper),
 * and an unreachable upstream is `success:false`, not an HTTP error.
 */
export interface ChannelTestResult {
  success: boolean
  latencyMs: number | null
  error: string
}

/** Result of token create: the only moment the plaintext key exists. */
export interface IssuedKey {
  id: number
  key: string
  name?: string
}

export interface ChannelCreateBody {
  name: string
  type: number
  base_url: string
  key: string
  models: string
  openai_organization?: string
  other?: string
}

export interface TokenCreateBody {
  name: string
  group: string
  unlimited_quota: boolean
  remain_quota: number
  /** Unix seconds; -1 = never expires. */
  expired_time: number
}

/** ApiError codes raised by this feature's own response checks. */
export const MISSING_CHANNEL_ID = 'MISSING_CHANNEL_ID'
export const MISSING_KEY = 'MISSING_KEY'
