import type {
  Invite,
  InviteList,
  InviteState,
  IssuedInvite,
  Member,
  MemberRole,
  ProjectRef,
} from '../types'

export function num(value: unknown, fallback = 0): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

export function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function record(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null
    ? (value as Record<string, unknown>)
    : {}
}

export function role(value: unknown): MemberRole {
  return value === 'admin' || value === 'dept_lead' ? value : ''
}

/** GET /projects -> live projects only. */
export function mapProjects(raw: unknown): ProjectRef[] {
  const items = record(raw).items
  if (!Array.isArray(items)) return []
  return items
    .map((p) => {
      const r = record(p)
      return { id: num(r.id), name: str(r.name), deleted: r.deleted === true }
    })
    .filter((p) => p.id > 0 && !p.deleted)
    .map(({ id, name }) => ({ id, name }))
}

/** GET /projects/:id/members `items` (repo.ProjectMemberUser). */
export function mapProjectMembers(raw: unknown) {
  const items = record(raw).items
  if (!Array.isArray(items)) return []
  return items
    .map((m) => {
      const r = record(m)
      return {
        userId: num(r.user_id),
        username: str(r.username),
        displayName: str(r.display_name),
        role: role(r.tenant_role),
        addedAt: num(r.added_at),
      }
    })
    .filter((m) => m.userId > 0)
}

export interface MemberList {
  items: Member[]
  total: number
}

/** GET /members -> { items, total } (tenantMemberView). */
export function mapMemberList(raw: unknown): MemberList {
  const r = record(raw)
  const list = Array.isArray(r.items) ? r.items : []
  const items = list
    .map((x): Member => {
      const m = record(x)
      const deps = Array.isArray(m.departments) ? m.departments : []
      const username = str(m.username)
      return {
        userId: num(m.user_id),
        name: str(m.display_name) || username,
        username,
        role: role(m.tenant_role),
        departments: deps.map((d) => str(record(d).name)).filter(Boolean),
        // joined_at is null when the server has no join timestamp.
        joinedAt: num(m.joined_at),
        isPayer: m.is_payer === true,
        email: str(m.email),
      }
    })
    .filter((m) => m.userId > 0)
  return { items, total: num(r.total, items.length) }
}

function inviteState(r: Record<string, unknown>): InviteState {
  if (r.used === true || r.status === 2) return 'used'
  if (r.revoked === true || r.status === 3) return 'revoked'
  if (r.expired === true) return 'expired'
  return 'active'
}

function createdSec(value: unknown): number {
  const ms = typeof value === 'string' ? Date.parse(value) : Number.NaN
  return Number.isFinite(ms) ? Math.floor(ms / 1000) : 0
}

/** GET /invites -> { invites, total } (tenantInviteView). */
export function mapInviteList(raw: unknown): InviteList {
  const r = record(raw)
  const list = Array.isArray(r.invites) ? r.invites : []
  const items = list.map((x): Invite => {
    const v = record(x)
    return {
      id: num(v.id),
      codePrefix: str(v.code_prefix),
      memberRole: role(v.member_role),
      projectId: num(v.project_id),
      expiresAt: num(v.expires_at),
      createdAt: createdSec(v.created_at),
      state: inviteState(v),
    }
  })
  return { items, total: num(r.total, items.length) }
}

/** POST /invites 201 body: the only response that carries the full code. */
export function mapIssuedInvite(raw: unknown): IssuedInvite {
  const r = record(raw)
  return {
    id: num(r.id),
    code: str(r.code),
    memberRole: role(r.member_role),
    projectId: num(r.project_id),
    expiresAt: num(r.expired_time),
  }
}

export function inviteLink(origin: string, code: string): string {
  return `${origin}/login?invite=${encodeURIComponent(code)}`
}

export const DEFAULT_TTL_HOURS = 72
export const MAX_TTL_HOURS = 720
