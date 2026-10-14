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
 * Group model ids for the selection lists by their leading name token
 * ("vendor/name", "family-1.2", "family:tag" all group under "family"). Names
 * that share a token with no other model fall into "Other". This is purely
 * lexical; it never maps a name to a vendor.
 */
const LEADING_TOKEN = /^[a-z]+/i

function leadingToken(model: string): string {
  const afterVendor = model.includes('/')
    ? model.slice(model.indexOf('/') + 1)
    : model
  const match = LEADING_TOKEN.exec(afterVendor.trim())
  return match ? match[0].toLowerCase() : ''
}

export function getModelCategory(
  model: string,
  counts: Map<string, number>
): string {
  const token = leadingToken(model)
  if (token === '' || (counts.get(token) ?? 0) < 2) return 'Other'
  return token
}

export function categorizeModels(
  models: readonly string[]
): Record<string, string[]> {
  const counts = new Map<string, number>()
  for (const model of models) {
    const token = leadingToken(model)
    counts.set(token, (counts.get(token) ?? 0) + 1)
  }
  const categories: Record<string, string[]> = {}
  for (const model of models) {
    const category = getModelCategory(model, counts)
    categories[category] ??= []
    categories[category].push(model)
  }
  return categories
}
