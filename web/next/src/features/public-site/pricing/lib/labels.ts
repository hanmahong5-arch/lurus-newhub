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
import { useTranslation } from 'react-i18next'

import {
  capabilityLabelKey,
  modalityLabelKey,
} from '@/features/models/lib/catalog'
import { useMoneyConfig } from '@/lib/status'

import type { ModelHealth } from '../../api'
import type { TokenUnit } from '../constants'
import { formatAmount, type PricingType } from './model'

/** Formats a USD figure in the operator's display currency. */
export function useAmountFormatter() {
  const { t } = useTranslation()
  const money = useMoneyConfig()
  const free = t('Free')
  return (usd: number | null) => formatAmount(usd, money, free)
}

export function unitLabel(unit: TokenUnit): string {
  return unit === 'K' ? '1K' : '1M'
}

/** English source text of the pricing type, for t(). */
export function typeLabelKey(type: PricingType): string {
  switch (type) {
    case 'call':
      return 'Per request'
    case 'search':
      return 'Per search unit'
    default:
      return 'Token-based'
  }
}

export function endpointLabel(t: (k: string) => string, endpoint: string) {
  return t(capabilityLabelKey(endpoint))
}

export function modalityLabel(t: (k: string) => string, modality: string) {
  return t(modalityLabelKey(modality))
}

/** English source text of the health state, for t(). */
export function healthLabelKey(health: ModelHealth): string {
  switch (health) {
    case 'operational':
      return 'Operational'
    case 'degraded':
      return 'Degraded'
    default:
      return 'Down'
  }
}
