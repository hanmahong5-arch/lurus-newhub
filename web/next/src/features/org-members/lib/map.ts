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

export interface SelfRef {
  id: number
  username: string
  displayName: string
  email: string
  role: MemberRole
  isPayer: boolean
}

/**
 * The server has no tenant-wide member list; the people it can name are the
 * members of the tenant's departments, plus the signed-in user. Merge them by
 * user id, collecting department names and the earliest join time.
 */
export function buildMembers(
  perProject: { project: ProjectRef; raw: unknown }[],
  self: SelfRef | null
): Member[] {
  const byId = new Map<number, Member>()
  for (const { project, raw } of perProject) {
    for (const m of mapProjectMembers(raw)) {
      const cur = byId.get(m.userId)
      if (cur) {
        cur.departments.push(project.name)
        if (m.addedAt > 0 && (cur.joinedAt === 0 || m.addedAt < cur.joinedAt)) {
          cur.joinedAt = m.addedAt
        }
        continue
      }
      byId.set(m.userId, {
        userId: m.userId,
        name: m.displayName || m.username,
        username: m.username,
        role: m.role,
        departments: [project.name],
        joinedAt: m.addedAt,
        isPayer: null,
        email: '',
      })
    }
  }
  if (self && self.id > 0) {
    const cur = byId.get(self.id)
    if (cur) {
      cur.isPayer = self.isPayer
      cur.email = self.email
      cur.role = self.role
    } else {
      byId.set(self.id, {
        userId: self.id,
        name: self.displayName || self.username,
        username: self.username,
        role: self.role,
        departments: [],
        joinedAt: 0,
        isPayer: self.isPayer,
        email: self.email,
      })
    }
  }
  return [...byId.values()].sort((a, b) => a.userId - b.userId)
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
