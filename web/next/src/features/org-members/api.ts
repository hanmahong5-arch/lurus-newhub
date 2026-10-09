import { queryOptions } from '@tanstack/react-query'

import { tenantApi } from '@/lib/api'

import {
  buildMembers,
  mapInviteList,
  mapIssuedInvite,
  mapProjects,
  num,
  role,
  str,
  type SelfRef,
} from './lib/map'
import type { InviteWriteBody, MemberRole } from './types'

export const PAGE_SIZE = 20
export const membersQueryKey = ['org-members'] as const

export const projectsQueryOptions = queryOptions({
  queryKey: [...membersQueryKey, 'projects'],
  queryFn: async () => mapProjects(await tenantApi.get('/projects')),
  staleTime: 60_000,
})

/**
 * No tenant-wide member endpoint exists: read each department's members and
 * fold in the signed-in user. Any failed read fails the whole list.
 */
export function membersQueryOptions(self: SelfRef | null) {
  return queryOptions({
    queryKey: [...membersQueryKey, 'list', self?.id ?? 0, self?.role ?? ''],
    queryFn: async () => {
      const projects = mapProjects(await tenantApi.get('/projects'))
      const perProject = await Promise.all(
        projects.map(async (project) => ({
          project,
          raw: await tenantApi.get(`/projects/${project.id}/members`),
        }))
      )
      return buildMembers(perProject, self)
    },
  })
}

export function invitesQueryOptions(page: number) {
  return queryOptions({
    queryKey: [...membersQueryKey, 'invites', page],
    queryFn: async () =>
      mapInviteList(
        await tenantApi.get('/invites', {
          params: { page, page_size: PAGE_SIZE },
        })
      ),
  })
}

export async function setMemberRole(userId: number, tenantRole: MemberRole) {
  await tenantApi.put(`/members/${userId}/role`, { tenant_role: tenantRole })
}

export async function createInvite(body: InviteWriteBody) {
  return mapIssuedInvite(await tenantApi.post('/invites', body))
}

export async function revokeInvite(id: number) {
  await tenantApi.delete(`/invites/${id}`)
}

export async function redeemInvite(code: string) {
  const raw = await tenantApi.post<Record<string, unknown> | null>(
    '/invites/redeem',
    { code }
  )
  return {
    memberRole: role(raw?.member_role),
    projectId: num(raw?.project_id),
    tenantId: str(raw?.tenant_id),
  }
}
