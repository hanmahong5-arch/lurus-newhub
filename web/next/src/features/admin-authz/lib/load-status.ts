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
import { ApiError } from '@/lib/api-error'
import { getServerErrorStatus } from '@/lib/server-error-message'

/** Why a read did not give us data. */
export type ReadFailure = 'forbidden' | 'unauthenticated' | 'error'

// The v1 session branch refuses with HTTP 200 {success:false,message}. The
// v2 admin routes answer a real 403 today, so this is a defensive match: a
// recognisable refusal body must still read as a refusal, while any other
// {success:false} (a store outage, a maintenance page) stays a plain error —
// claiming "root access required" for it would be a fresh false statement.
const REFUSAL_MESSAGES = new Set(['无权进行此操作，权限不足'])
const REFUSAL_CODE = 'PERMISSION_DENIED'

export function classifyReadError(error: unknown): ReadFailure {
  const status = getServerErrorStatus(error)
  if (status === 401) return 'unauthenticated'
  if (status === 403) return 'forbidden'
  if (error instanceof ApiError) {
    if (error.code === REFUSAL_CODE) return 'forbidden'
    if (REFUSAL_MESSAGES.has(error.message.trim())) return 'forbidden'
  }
  return 'error'
}

/** The page-level outcome of several reads: a refusal outranks a plain error. */
export function combineFailures(
  failures: Array<ReadFailure | null>
): ReadFailure | null {
  const real = failures.filter((f): f is ReadFailure => f !== null)
  if (real.length === 0) return null
  if (real.includes('forbidden')) return 'forbidden'
  return real[0]
}
