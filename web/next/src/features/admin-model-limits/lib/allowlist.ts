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
/** Mirrors the server's maxAllowlistEntryBytes (tenant_model_allowlist.go). */
export const MAX_ENTRY_LENGTH = 128

export type EntryProblem = 'duplicate' | 'wildcard' | 'too-long' | null

/**
 * Why `entry` (already trimmed, non-empty) cannot join `list`. The server is
 * the authority; this only catches what it would answer 400 to, so the
 * operator hears about it at the input instead of after a save.
 */
export function entryProblem(
  entry: string,
  list: readonly string[]
): EntryProblem {
  if (list.includes(entry)) return 'duplicate'
  const star = entry.indexOf('*')
  // "*" is honoured only as a trailing wildcard (tenantpolicy.ModelAllowed).
  if (star >= 0 && star !== entry.length - 1) return 'wildcard'
  if (entry.length > MAX_ENTRY_LENGTH) return 'too-long'
  return null
}
