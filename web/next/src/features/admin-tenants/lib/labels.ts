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

import type { StatusVariant } from '@/components/status-badge'
import dayjs from '@/lib/dayjs'

import { TENANT_STATUS } from '../constants'
import type { TenantAction } from '../types'

export function statusLabel(status: number, t: TFunction): string {
  if (status === TENANT_STATUS.ENABLED) return t('Active')
  if (status === TENANT_STATUS.DISABLED) return t('Disabled')
  if (status === TENANT_STATUS.SUSPENDED) return t('Suspended')
  return t('Unknown')
}

export function statusVariant(status: number): StatusVariant {
  if (status === TENANT_STATUS.ENABLED) return 'success'
  if (status === TENANT_STATUS.SUSPENDED) return 'warning'
  return 'neutral'
}

export function planLabel(plan: string | undefined, t: TFunction): string {
  switch (plan) {
    case 'free':
      return t('Free')
    case 'pro':
      return t('Pro')
    case 'enterprise':
      return t('Enterprise')
    default:
      return plan || '-'
  }
}

export function planVariant(plan: string | undefined): StatusVariant {
  if (plan === 'enterprise') return 'purple'
  if (plan === 'pro') return 'info'
  return 'neutral'
}

/** An ISO timestamp from the API, or "-" when absent or unreadable. */
export function formatIsoDate(iso: string | null | undefined): string {
  if (!iso) return '-'
  const parsed = dayjs(iso)
  // Go serialises an unset time.Time as 0001-01-01T00:00:00Z, which parses as
  // valid and renders as 0000-12-31 in a western-hemisphere zone. No real
  // record predates the epoch, so anything earlier means "not recorded".
  if (!parsed.isValid() || parsed.year() < 1970) return '-'
  return parsed.format('YYYY-MM-DD HH:mm')
}

/** Title, button and success text of an enable / disable / suspend. */
export function statusActionCopy(
  action: TenantAction,
  name: string,
  t: TFunction
): { title: string; confirm: string; done: string } {
  switch (action) {
    case 'enable':
      return {
        title: t('Enable tenant "{{name}}"?', { name }),
        confirm: t('Enable'),
        done: t('Tenant enabled'),
      }
    case 'disable':
      return {
        title: t('Disable tenant "{{name}}"?', { name }),
        confirm: t('Disable'),
        done: t('Tenant disabled'),
      }
    case 'suspend':
      return {
        title: t('Suspend tenant "{{name}}"?', { name }),
        confirm: t('Suspend'),
        done: t('Tenant suspended'),
      }
  }
}

export function resetPeriodLabel(period: string, t: TFunction): string {
  switch (period) {
    case 'none':
      return t('No reset')
    case 'daily':
      return t('Daily')
    case 'weekly':
      return t('Weekly')
    case 'monthly':
      return t('Monthly')
    default:
      return period
  }
}
