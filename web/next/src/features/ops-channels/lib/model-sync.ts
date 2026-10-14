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
import { formatModelsArray, parseModelsString } from './model-mapping-validation'

/**
 * Keep a channel's published model list consistent with a batch of mapping
 * pairs: the request names (`from`) become published, and the raw upstream
 * names (`to`) are hidden so /v1/models only lists what users should call.
 */
export function syncModelsWithMappingPairs(
  models: string,
  pairs: ReadonlyArray<{ from: string; to: string }>
): string {
  const targets = new Set(pairs.map((p) => p.to.trim()).filter(Boolean))
  const next = parseModelsString(models).filter((m) => !targets.has(m))
  for (const pair of pairs) {
    const alias = pair.from.trim()
    if (alias && !next.includes(alias)) next.push(alias)
  }
  return formatModelsArray(next)
}
