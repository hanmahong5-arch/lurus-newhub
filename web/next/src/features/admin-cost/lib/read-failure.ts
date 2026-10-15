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
 * How a failed admin read is presented. Three different answers, never
 * collapsed into one: the role is short (403), the session is gone (401, no
 * permission grant would help), or the read simply did not work (everything
 * else, including an HTTP 200 carrying `success:false`). Only the last one
 * offers a retry, and none of them may be drawn as "nothing to report".
 */
export type ReadFailureKind = 'forbidden' | 'signed-out' | 'error'

export function classifyReadFailure(error: unknown): ReadFailureKind {
  const status =
    typeof error === 'object' && error !== null && 'status' in error
      ? (error as { status?: unknown }).status
      : undefined
  if (status === 403) return 'forbidden'
  if (status === 401) return 'signed-out'
  return 'error'
}

export function readFailureMessage(error: unknown): string | undefined {
  return error instanceof Error && error.message.trim()
    ? error.message
    : undefined
}
