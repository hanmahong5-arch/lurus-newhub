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

import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { useQuota } from '@/hooks/use-quota'
import { cn } from '@/lib/utils'

import { chargedCny, formatCny, getLogTypeConfig } from '../lib/log-view'
import type { UsageLog } from '../lib/logs'

/** Cost of a row, with the wallet debit underneath when one was recorded. */
export function CostCell(props: { row: UsageLog; align?: 'left' | 'right' }) {
  const { t } = useTranslation()
  const { formatLogQuota } = useQuota()
  const charged = chargedCny(props.row)
  return (
    <div
      className={cn(
        'flex flex-col gap-0.5',
        props.align === 'right' && 'items-end text-right'
      )}
    >
      <span className='font-mono text-xs font-medium tabular-nums'>
        {formatLogQuota(props.row.quota)}
      </span>
      {charged !== null && (
        <span
          className='text-muted-foreground/70 font-mono text-[11px] tabular-nums'
          title={t('Debited from the wallet')}
        >
          {t('Charged')} {formatCny(charged)}
        </span>
      )}
    </div>
  )
}

export function TypeBadge(props: { type: number; className?: string }) {
  const { t } = useTranslation()
  const cfg = getLogTypeConfig(props.type)
  return (
    <StatusBadge
      label={t(cfg.label)}
      variant={cfg.color as StatusVariant}
      size='sm'
      copyable={false}
      className={props.className}
    />
  )
}

export function Dash() {
  return <span className='text-muted-foreground/60 text-xs'>-</span>
}

