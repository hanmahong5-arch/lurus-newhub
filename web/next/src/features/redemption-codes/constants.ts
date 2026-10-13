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

import type { StatusBadgeProps } from '@/components/status-badge'

export const REDEMPTION_STATUS = {
  ENABLED: 1,
  DISABLED: 2,
  USED: 3,
} as const

export const REDEMPTION_STATUSES: Record<
  number,
  Pick<StatusBadgeProps, 'variant'> & { labelKey: string }
> = {
  [REDEMPTION_STATUS.ENABLED]: { labelKey: 'Unused', variant: 'success' },
  [REDEMPTION_STATUS.DISABLED]: { labelKey: 'Disabled', variant: 'neutral' },
  [REDEMPTION_STATUS.USED]: { labelKey: 'Used', variant: 'neutral' },
}

/** "Expired" is not stored: it is an unused code past its expiry time. */
export const REDEMPTION_FILTER_EXPIRED = 'expired'

export function getRedemptionStatusOptions(t: TFunction) {
  return [
    { label: t('Unused'), value: String(REDEMPTION_STATUS.ENABLED) },
    { label: t('Disabled'), value: String(REDEMPTION_STATUS.DISABLED) },
    { label: t('Used'), value: String(REDEMPTION_STATUS.USED) },
    { label: t('Expired'), value: REDEMPTION_FILTER_EXPIRED },
  ]
}

export const DEFAULT_PAGE_SIZE = 20

/** The create handler rejects names over 50 bytes and counts over 100. */
export const NAME_MAX_BYTES = 50
export const COUNT_MIN = 1
export const COUNT_MAX = 100

/** Simultaneous DELETE requests of a bulk delete. */
export const BULK_DELETE_CONCURRENCY = 4
