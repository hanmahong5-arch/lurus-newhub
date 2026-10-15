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
import { blankLast } from './model-name'
import type { PerformanceRow } from './types'

/** Volume fields exist for tenant admins only. */
export function hasVolume(rows: PerformanceRow[]): boolean {
  return rows.some((r) => typeof r.requests === 'number')
}

export function sortPerformance(rows: PerformanceRow[]): PerformanceRow[] {
  const volume = hasVolume(rows)
  // A row with no model name is never ranked against real models: it is last.
  return blankLast(
    [...rows].sort((a, b) =>
      volume
        ? (b.requests ?? 0) - (a.requests ?? 0) ||
          a.model_name.localeCompare(b.model_name)
        : a.model_name.localeCompare(b.model_name)
    ),
    (r) => r.model_name
  )
}
