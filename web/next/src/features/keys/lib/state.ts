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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import type { StatusVariant } from '@/components/status-badge'

import type { TokenState } from './map'

export const STATE_VARIANT: Record<TokenState, StatusVariant> = {
  enabled: 'success',
  'near-cap': 'warning',
  disabled: 'neutral',
  expired: 'danger',
  exhausted: 'danger',
}

export const TOKEN_STATES: TokenState[] = [
  'enabled',
  'near-cap',
  'disabled',
  'expired',
  'exhausted',
]

/** Localised label of a derived key state. */
export function useStateLabels(): Record<TokenState, string> {
  const { t } = useTranslation()
  return useMemo(
    () => ({
      enabled: t('Enabled'),
      'near-cap': t('Near quota limit'),
      disabled: t('Disabled'),
      expired: t('Expired'),
      exhausted: t('Quota exhausted'),
    }),
    [t]
  )
}
