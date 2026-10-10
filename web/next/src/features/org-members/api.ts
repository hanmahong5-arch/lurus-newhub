import { queryOptions } from '@tanstack/react-query'

import { tenantApi } from '@/lib/api'

import {
  mapMemberList,
  mapInviteList,
  mapIssuedInvite,
  mapProjects,
  num,
  role,
  str,
} from './lib/map'
import type { InviteWriteBody, MemberRole } from './types'

export const PAGE_SIZE = 20
export const membersQueryKey = ['org-members'] as const

export const projectsQueryOptions = queryOptions({
  queryKey: [...membersQueryKey, 'projects'],
  queryFn: async () => mapProjects(await tenantApi.get('/projects')),
  staleTime: 60_000,
})

export interface MembersQuery {
  page: number
  keyword: string
}

/** GET /members: the tenant roster (admin only), paged and keyword-filtered. */
export function membersQueryOptions(q: MembersQuery) {
  return queryOptions({
    queryKey: [...membersQueryKey, 'list', q.page, q.keyword],
    queryFn: async () =>
      mapMemberList(
        await tenantApi.get('/members', {
          params: {
            page: q.page,
            page_size: PAGE_SIZE,
            ...(q.keyword ? { keyword: q.keyword } : {}),
          },
        })
      ),
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
