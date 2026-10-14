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
import { queryOptions } from '@tanstack/react-query'

import { api } from '@/lib/api'

import {
  mapMjLog,
  mapPage,
  mapTaskLog,
  mjParams,
  taskParams,
  type MjFilters,
  type MjLog,
  type TaskFilters,
  type TaskLog,
  type TaskPage,
} from './lib/tasks'

/** The caller's own async jobs: GET /api/task/self/ (v1, session auth). */
export function taskLogsQueryOptions(
  filters: TaskFilters,
  page: number,
  pageSize: number
) {
  return queryOptions<TaskPage<TaskLog>>({
    queryKey: ['tasks', 'jobs', filters, page, pageSize],
    queryFn: async () =>
      mapPage(
        await api.get('/api/task/self/', {
          params: taskParams(filters, page, pageSize),
        }),
        mapTaskLog
      ),
    retry: false,
  })
}

/** The caller's own Midjourney rows: GET /api/mj/self/. */
export function mjLogsQueryOptions(
  filters: MjFilters,
  page: number,
  pageSize: number
) {
  return queryOptions<TaskPage<MjLog>>({
    queryKey: ['tasks', 'drawing', filters, page, pageSize],
    queryFn: async () =>
      mapPage(
        await api.get('/api/mj/self/', {
          params: mjParams(filters, page, pageSize),
        }),
        mapMjLog
      ),
    retry: false,
  })
}
