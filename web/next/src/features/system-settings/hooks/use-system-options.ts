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
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { getSystemOptions, type SystemOption } from '../api'

export const SYSTEM_OPTIONS_QUERY_KEY = ['system-options'] as const

/** Every option the server lets a root operator read. */
export function useSystemOptions() {
  return useQuery({
    queryKey: SYSTEM_OPTIONS_QUERY_KEY,
    queryFn: getSystemOptions,
    staleTime: 60 * 1000,
    refetchOnWindowFocus: false,
  })
}

/** key -> raw stored string. */
export type OptionMap = Record<string, string>

export function optionsToMap(options: SystemOption[] | undefined): OptionMap {
  const out: OptionMap = {}
  for (const option of options ?? []) out[option.key] = option.value
  return out
}

export function useOptionMap(): {
  map: OptionMap
  isLoading: boolean
  isError: boolean
  error: unknown
  refetch: () => void
  /** True once the list has been read at least once. */
  loaded: boolean
} {
  const query = useSystemOptions()
  const map = useMemo(() => optionsToMap(query.data), [query.data])
  return {
    map,
    isLoading: query.isLoading,
    isError: query.isError && !query.data,
    error: query.error,
    refetch: () => void query.refetch(),
    loaded: query.data !== undefined,
  }
}
