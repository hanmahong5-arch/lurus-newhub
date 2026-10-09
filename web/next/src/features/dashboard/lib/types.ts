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

/** One row of GET /api/v2/~/logs (handler.logView); only the fields read here. */
export interface LogRow {
  id?: number
  created_at?: number
  type?: number
  model_name?: string
  quota?: number
  total_latency_ms?: number
}

/** One row of GET /api/data/self/ (entity.QuotaData): per user/model/hour. */
export interface QuotaRow {
  model_name?: string
  created_at?: number
  quota?: number
  count?: number
  token_used?: number
}

export interface RealtimeLogs {
  logs: LogRow[]
  /** Server-reported row count for the window; `logs` is only one page. */
  total: number | null
}

export interface TrendData {
  rows: QuotaRow[]
  start: number
  end: number
}
