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
export const TENANT_STATUS = {
  ENABLED: 1,
  DISABLED: 2,
  SUSPENDED: 3,
} as const

/** Plans the platform names (entity.TenantPlan*). */
export const PLAN_OPTIONS = ['free', 'pro', 'enterprise'] as const
export const DEFAULT_PLAN = 'free'

export const DEFAULT_PAGE_SIZE = 20

/** The list endpoint caps page_size at 100; the console reads every page. */
export const LIST_FETCH_PAGE_SIZE = 100
export const LIST_FETCH_MAX_PAGES = 20

export const SLUG_MAX_LENGTH = 64
export const NAME_MAX_LENGTH = 128

/** Credit pool: max_balance -1 means "no ceiling". */
export const POOL_UNLIMITED = -1
export const POOL_RESET_PERIODS = ['none', 'daily', 'weekly', 'monthly'] as const
export const POOL_DEFAULT_RESET_PERIOD = 'monthly'
export const POOL_DEFAULT_ALERT_PCT = 80
/** A pool under this share of its ceiling is "low". */
export const POOL_LOW_RATIO = 0.2
export const POOL_USAGE_LIMIT = 20

export const INVITE_STATUS = {
  PENDING: 1,
  CONSUMED: 2,
  REVOKED: 3,
} as const

export const INVITE_DEFAULT_TTL_HOURS = 72
export const INVITE_MAX_TTL_HOURS = 30 * 24
export const INVITE_LIST_PAGE_SIZE = 100
