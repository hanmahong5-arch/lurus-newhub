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
import { Shield, User, Users } from 'lucide-react'

import type { AdminUser } from './types'

export const USER_STATUS = {
  ENABLED: 1,
  DISABLED: 2,
  DELETED: -1,
} as const

export const USER_STATUSES = {
  [USER_STATUS.ENABLED]: { labelKey: 'Enabled', variant: 'success' },
  [USER_STATUS.DISABLED]: { labelKey: 'Disabled', variant: 'neutral' },
  [USER_STATUS.DELETED]: { labelKey: 'Deleted', variant: 'danger' },
} as const

export const USER_ROLE = {
  USER: 1,
  SUBSCRIBER: 5,
  ADMIN: 10,
  ROOT: 100,
} as const

export const USER_ROLES = {
  [USER_ROLE.USER]: { labelKey: 'User', icon: User },
  [USER_ROLE.SUBSCRIBER]: { labelKey: 'Subscriber', icon: User },
  [USER_ROLE.ADMIN]: { labelKey: 'Admin', icon: Users },
  [USER_ROLE.ROOT]: { labelKey: 'Root', icon: Shield },
} as const

export const DEFAULT_PAGE_SIZE = 20

/** Column limits of the users table (repo.User validate tags). */
export const NAME_MAX_LENGTH = 20
export const REMARK_MAX_LENGTH = 255

/** Reason limit of the 2FA force-disable audit record (runes). */
export const REASON_MAX_LENGTH = 200

/** users.quota is a 32-bit integer column. */
export const QUOTA_COLUMN_MAX = 2_147_483_647

export function isUserDeleted(user: AdminUser): boolean {
  return user.DeletedAt != null && user.DeletedAt !== ''
}

export function getUserStatusOptions(t: (key: string) => string) {
  return [
    { label: t('Enabled'), value: String(USER_STATUS.ENABLED) },
    { label: t('Disabled'), value: String(USER_STATUS.DISABLED) },
    { label: t('Deleted'), value: String(USER_STATUS.DELETED) },
  ]
}

export function getUserRoleOptions(t: (key: string) => string) {
  return [
    { label: t('User'), value: String(USER_ROLE.USER), icon: User },
    { label: t('Subscriber'), value: String(USER_ROLE.SUBSCRIBER), icon: User },
    { label: t('Admin'), value: String(USER_ROLE.ADMIN), icon: Users },
    { label: t('Root'), value: String(USER_ROLE.ROOT), icon: Shield },
  ]
}
