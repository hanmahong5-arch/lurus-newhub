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
const INT32_MAX = 2147483647

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

/**
 * ModelRequestRateLimitGroup: {group: [maxRequests, maxSuccess]}. Mirrors
 * setting.CheckModelRequestRateLimitGroup so the person is told before the
 * round trip. Returns an English message or null.
 */
export function validateRateLimitGroups(parsed: unknown): string | null {
  if (!isRecord(parsed)) return 'Expected a JSON object'
  for (const value of Object.values(parsed)) {
    if (
      !Array.isArray(value) ||
      value.length !== 2 ||
      !Number.isInteger(value[0]) ||
      !Number.isInteger(value[1])
    ) {
      return 'Each group needs [maxRequests, maxSuccess] as whole numbers'
    }
    const [requests, success] = value as [number, number]
    if (requests < 0 || success < 1 || requests > INT32_MAX || success > INT32_MAX) {
      return 'maxRequests must be 0 or more and maxSuccess 1 or more, both within 2,147,483,647'
    }
  }
  return null
}

/** GroupRatio: {group: non-negative number}. */
export function validateGroupRatios(parsed: unknown): string | null {
  if (!isRecord(parsed)) return 'Expected a JSON object'
  for (const value of Object.values(parsed)) {
    if (typeof value !== 'number' || !Number.isFinite(value)) {
      return 'Every ratio must be a number'
    }
    if (value < 0) return 'Ratios cannot be negative'
  }
  return null
}

/** GroupGroupRatio: {userGroup: {usingGroup: non-negative number}}. */
export function validateGroupGroupRatios(parsed: unknown): string | null {
  if (!isRecord(parsed)) return 'Expected a JSON object'
  for (const inner of Object.values(parsed)) {
    if (!isRecord(inner)) return 'Each user group needs an object of group ratios'
    const problem = validateGroupRatios(inner)
    if (problem) return problem
  }
  return null
}

/** UserUsableGroups: {group: description text}. */
export function validateUsableGroups(parsed: unknown): string | null {
  if (!isRecord(parsed)) return 'Expected a JSON object'
  for (const value of Object.values(parsed)) {
    if (typeof value !== 'string') return 'Every description must be text'
  }
  return null
}
