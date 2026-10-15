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
import { describe, expect, it } from 'vitest'

import type { TenantStats } from '../types'
import { statRows } from './stats'

const t = ((key: string) => key) as unknown as TFunction
const money = (quota: number) => `$${(quota / 500_000).toFixed(2)}`

const STATS: TenantStats = {
  tenant_id: 't-1',
  user_count: 3,
  max_users: 100,
  max_quota: 0,
  token_count: 12,
  channel_count: 2,
  total_quota_used: 0,
  total_quota_remaining: 0,
  active_subscriptions: 0,
  total_topup_amount: 0,
  total_redemptions: 1,
  log_count: 4200,
  last_activity_at: 0,
}

describe('statRows', () => {
  it('labels every row in words and formats the values', () => {
    const rows = Object.fromEntries(
      statRows(
        { ...STATS, total_quota_used: 5 * 500_000, max_quota: 20 * 500_000 },
        money,
        t
      )
    )
    expect(rows['Seats used']).toBe('3 / 100')
    expect(rows['Quota used']).toBe('$5.00')
    expect(rows['Quota cap']).toBe('$20.00')
    expect(rows['Requests logged']).toBe('4,200')
    expect(rows['Last activity']).toBe('Never')
  })

  it('says Unlimited for a zero cap and omits the fields the backend never fills', () => {
    const rows = statRows(STATS, money, t)
    const labels = rows.map(([label]) => label)
    expect(Object.fromEntries(rows)['Quota cap']).toBe('Unlimited')
    for (const raw of Object.keys(STATS)) {
      expect(labels).not.toContain(raw)
    }
    expect(labels.join(' ')).not.toMatch(/subscription|top-?up/i)
  })

  it('shows the seat count alone when there is no seat cap', () => {
    const rows = Object.fromEntries(
      statRows({ ...STATS, max_users: 0 }, money, t)
    )
    expect(rows['Seats used']).toBe('3')
  })

  it('formats a real last-activity time', () => {
    const rows = Object.fromEntries(
      statRows({ ...STATS, last_activity_at: 1_700_000_000 }, money, t)
    )
    expect(rows['Last activity']).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/)
  })
})
