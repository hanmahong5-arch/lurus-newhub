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
/** Active-probe row for one (channel, model); null = never probed. */
export interface ModelHealth {
  ok: boolean
  last_probe_at: number
  latency_ms: number
  last_error: string
  consecutive_failures: number
  auto_disabled: boolean
}

export interface PoolModel {
  model: string
  requests_24h: number
  errors_24h: number
  health: ModelHealth | null
}

export interface KeyCounts {
  total: number
  enabled: number
  cooling: number
  disabled: number
}

export type ChannelStatus =
  'enabled' | 'auto_disabled' | 'manually_disabled' | 'unknown'

export interface PoolChannel {
  id: number
  name: string
  type: number
  /** Server label; kept open so an unknown future value still renders. */
  status: ChannelStatus | (string & {})
  priority: number
  weight: number
  tenant_id: string
  test_time: number
  response_time: number
  balance: number
  keys: KeyCounts
  models: PoolModel[]
}

export interface Pool {
  group: string
  channels: PoolChannel[]
}

export interface PoolsOverview {
  generated_at: number
  pools: Pool[]
}
