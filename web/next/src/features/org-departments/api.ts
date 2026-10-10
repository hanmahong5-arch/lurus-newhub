import { queryOptions } from '@tanstack/react-query'

import { tenantApi } from '@/lib/api'

import { mapDepartments, mapMembers, mapSpend } from './lib/map'
import type { DepartmentWriteBody } from './types'

export const departmentsQueryKey = ['org-departments'] as const

export const departmentsQueryOptions = queryOptions({
  queryKey: [...departmentsQueryKey, 'list'],
  queryFn: async () => mapDepartments(await tenantApi.get('/projects')),
})

/** Consume spend per project since `start` (unix seconds). */
export function spendQueryOptions(start: number) {
  return queryOptions({
    queryKey: [...departmentsQueryKey, 'spend', start],
    queryFn: async () =>
      mapSpend(await tenantApi.get('/projects/spend', { params: { start } })),
  })
}

export function membersQueryOptions(projectId: number) {
  return queryOptions({
    queryKey: [...departmentsQueryKey, 'members', projectId],
    queryFn: async () =>
      mapMembers(await tenantApi.get(`/projects/${projectId}/members`)),
  })
}

export async function createDepartment(body: DepartmentWriteBody) {
  await tenantApi.post('/projects', body)
}

export async function updateDepartment(id: number, body: DepartmentWriteBody) {
  await tenantApi.put(`/projects/${id}`, body)
}

export async function deleteDepartment(id: number) {
  await tenantApi.delete(`/projects/${id}`)
}

export async function addMember(projectId: number, userId: number) {
  await tenantApi.post(`/projects/${projectId}/members`, { user_id: userId })
}

export async function removeMember(projectId: number, userId: number) {
  await tenantApi.delete(`/projects/${projectId}/members`, {
    params: { user_id: userId },
  })
}
