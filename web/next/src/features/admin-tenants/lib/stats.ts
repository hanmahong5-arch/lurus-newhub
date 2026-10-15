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
import type { TFunction } from 'i18next'

import { formatTimestampToDate } from '@/components/format'

import type { TenantStats } from '../types'

const count = (n: number | undefined) => Number(n ?? 0).toLocaleString('en-US')

/**
 * The rows the stats drawer shows, in reading order, each with a human label.
 * The endpoint also returns active_subscriptions and total_topup_amount, but
 * the server never fills them in: they are always 0, and a 0 printed here
 * would read as a measured fact. tenant_id is already the drawer's subject.
 */
export function statRows(
  stats: TenantStats,
  formatMoney: (quota: number) => string,
  t: TFunction
): [label: string, value: string][] {
  return [
    [
      t('Seats used'),
      stats.max_users > 0
        ? `${count(stats.user_count)} / ${count(stats.max_users)}`
        : count(stats.user_count),
    ],
    [t('API keys'), count(stats.token_count)],
    [t('Channels'), count(stats.channel_count)],
    [t('Quota used'), formatMoney(stats.total_quota_used || 0)],
    [t('Quota remaining'), formatMoney(stats.total_quota_remaining || 0)],
    [
      t('Quota cap'),
      stats.max_quota > 0 ? formatMoney(stats.max_quota) : t('Unlimited'),
    ],
    [t('Redemption codes'), count(stats.total_redemptions)],
    [t('Requests logged'), count(stats.log_count)],
    [
      t('Last activity'),
      stats.last_activity_at > 0
        ? formatTimestampToDate(stats.last_activity_at)
        : t('Never'),
    ],
  ]
}
