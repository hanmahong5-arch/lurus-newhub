/** Role a member holds inside the tenant (users.tenant_role); '' = plain member. */
export type MemberRole = 'admin' | 'dept_lead' | ''

export interface Member {
  userId: number
  /** display_name, falling back to username. */
  name: string
  username: string
  role: MemberRole
  /** Names of the departments (projects) the member belongs to. */
  departments: string[]
  /** Join time, unix seconds; 0 = the server has no join timestamp. */
  joinedAt: number
  /** Whether the member is the tenant payer. */
  isPayer: boolean
  email: string
}

export interface ProjectRef {
  id: number
  name: string
}

export type InviteState = 'active' | 'used' | 'revoked' | 'expired'

export interface Invite {
  id: number
  codePrefix: string
  memberRole: MemberRole
  projectId: number
  /** Unix seconds; 0 = never. */
  expiresAt: number
  createdAt: number
  state: InviteState
}

export interface InviteList {
  items: Invite[]
  total: number
}

export interface IssuedInvite {
  id: number
  code: string
  memberRole: MemberRole
  projectId: number
  expiresAt: number
}

export interface InviteWriteBody {
  ttl_hours: number
  member_role: MemberRole
  project_id: number
}
