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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { formatDateTimeObject } from '@/lib/time'

import {
  CHANNEL_STATUS_AUTO_DISABLED,
  CHANNEL_STATUS_ENABLED,
  CHANNEL_STATUS_MANUAL_DISABLED,
} from '../types'

export interface StatusView {
  label: string
  variant: 'default' | 'secondary' | 'destructive'
}

export function useStatusLabel(): (status: number) => StatusView {
  const { t } = useTranslation()
  return (status) => {
    switch (status) {
      case CHANNEL_STATUS_ENABLED:
        return { label: t('Enabled'), variant: 'default' }
      case CHANNEL_STATUS_MANUAL_DISABLED:
        return { label: t('Disabled manually'), variant: 'secondary' }
      case CHANNEL_STATUS_AUTO_DISABLED:
        return { label: t('Auto-disabled'), variant: 'destructive' }
      default:
        return { label: t('Unknown'), variant: 'secondary' }
    }
  }
}

export function usePlanKindLabel(): (kind: string) => string {
  const { t } = useTranslation()
  const labels: Record<string, string> = {
    zhipu_coding: t('Zhipu coding plan'),
    kimi_coding: t('Moonshot coding plan'),
    minimax: t('MiniMax plan'),
  }
  return (kind) =>
    kind === '' ? t('Pay as you go') : (labels[kind] ?? kind)
}

/** Wall clock in whole seconds, re-read every `everyMs`. */
export function useNowSec(everyMs = 1000): number {
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000))
  useEffect(() => {
    const id = setInterval(
      () => setNow(Math.floor(Date.now() / 1000)),
      everyMs
    )
    return () => clearInterval(id)
  }, [everyMs])
  return now
}

export function formatTs(sec: number): string {
  return formatDateTimeObject(new Date(sec * 1000))
}
