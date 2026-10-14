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
import { toApiError } from '@/lib/api'

import { BULK_DELETE_CONCURRENCY } from '../constants'

export interface BulkDeleteResult {
  deleted: number[]
  failed: { id: number; message: string }[]
}

/**
 * Delete many codes one by one. The tenant routes have no batch endpoint, so
 * this runs a few requests at a time and keeps going past a failure; the
 * caller reports exactly which ones were removed and which were not.
 */
export async function deleteMany(
  ids: number[],
  remove: (id: number) => Promise<unknown>,
  concurrency: number = BULK_DELETE_CONCURRENCY
): Promise<BulkDeleteResult> {
  const result: BulkDeleteResult = { deleted: [], failed: [] }
  let next = 0

  async function worker() {
    while (next < ids.length) {
      const id = ids[next++]
      try {
        await remove(id)
        result.deleted.push(id)
      } catch (error) {
        result.failed.push({ id, message: toApiError(error).message })
      }
    }
  }

  const workers = Array.from(
    { length: Math.min(Math.max(concurrency, 1), ids.length) },
    () => worker()
  )
  await Promise.all(workers)
  return result
}
