import type { InviteWriteBody, MemberRole } from '../types'

import { DEFAULT_TTL_HOURS, MAX_TTL_HOURS } from './map'

/** Validate the raw form into a request body; null = ttl out of range. */
export function buildInviteBody(
  ttl: string,
  role: MemberRole,
  projectId: string
): InviteWriteBody | null {
  const hours = ttl.trim() === '' ? DEFAULT_TTL_HOURS : Number(ttl)
  if (!Number.isInteger(hours) || hours < 1 || hours > MAX_TTL_HOURS) {
    return null
  }
  return {
    ttl_hours: hours,
    member_role: role,
    project_id: projectId === '' ? 0 : Number(projectId),
  }
}
