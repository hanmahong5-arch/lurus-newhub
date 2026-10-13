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

import { ROLE } from '@/lib/auth'
import { currentUserQueryOptions, userAccess, type UserAccess } from '@/lib/user'

/** Access flags of the signed-in user (UI hygiene only; the server enforces). */
export function useUserAccess(): UserAccess {
  const { data } = useQuery(currentUserQueryOptions)
  return userAccess(data)
}

/** Platform staff (integer role >= admin). Replaces donor `useIsAdmin`. */
export function useIsAdmin(): boolean {
  const { data } = useQuery(currentUserQueryOptions)
  return (data?.role ?? 0) >= ROLE.admin
}
