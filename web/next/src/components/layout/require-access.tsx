import { useQuery } from '@tanstack/react-query'
import type { ReactNode } from 'react'

import { ForbiddenError } from '@/features/errors/forbidden'
import { canAccess, type Requires } from '@/lib/access'
import { currentUserQueryOptions, userAccess } from '@/lib/user'

/**
 * Route guard: renders the 403 page in place (no redirect) when the caller
 * lacks the permission. The user is already resolved by the _authenticated
 * loader; while it is not, nothing is rendered rather than flashing a 403.
 */
export function RequireAccess(props: {
  requires: Requires
  children: ReactNode
}) {
  const { data: user } = useQuery(currentUserQueryOptions)
  if (!user) return null
  if (!canAccess(props.requires, userAccess(user))) return <ForbiddenError embedded />
  return props.children
}
