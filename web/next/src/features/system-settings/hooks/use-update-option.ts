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
import { useMutation, useQueryClient } from '@tanstack/react-query'

import { updateSystemOption, type OptionValue, type SystemOption } from '../api'
import { SYSTEM_OPTIONS_QUERY_KEY } from './use-system-options'

export type UpdateOptionRequest = { key: string; value: OptionValue }

/** The string the server stores for a value (handler/option.go stringifies). */
export function storedString(value: OptionValue): string {
  return typeof value === 'string' ? value : String(value)
}

/**
 * Write one option. A refusal (HTTP 200 `success:false`, or a 4xx/5xx) rejects
 * with an ApiError carrying the server's own message - the caller decides how
 * to present it, so one failed key never reads as a saved form.
 *
 * On success the cached option list is patched in place, so the form that just
 * saved reads the new value as its baseline immediately. It is deliberately
 * NOT refetched: replicas pick a write up on their next option-sync tick, so
 * an immediate read can land on a replica that still serves the old value and
 * would flip a just-saved form back to dirty. `/api/status` is refreshed
 * because the site name, notice and money display all travel through it.
 */
export function useUpdateOption() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async ({ key, value }: UpdateOptionRequest) => {
      await updateSystemOption(key, value)
    },
    onSuccess: (_data, { key, value }) => {
      const stored = storedString(value)
      queryClient.setQueryData<SystemOption[]>(
        SYSTEM_OPTIONS_QUERY_KEY,
        (current) => {
          if (!current) return current
          return current.some((option) => option.key === key)
            ? current.map((option) =>
                option.key === key ? { key, value: stored } : option
              )
            : [...current, { key, value: stored }]
        }
      )
      void queryClient.invalidateQueries({ queryKey: ['status'] })
    },
  })
}
