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

import { QuotaDetailsPopover } from '@/components/quota-details-popover'
import { StatusBadge } from '@/components/status-badge'
import { useQuota } from '@/hooks/use-quota'
import { cn } from '@/lib/utils'

interface UserQuotaCellProps {
  remaining: number
  used: number
}

export function UserQuotaCell(props: UserQuotaCellProps) {
  const { t } = useTranslation()
  const { formatQuota, symbol, isTokens } = useQuota()
  const unit = isTokens ? t('Tokens') : symbol
  const hasQuota = props.remaining !== 0 || props.used !== 0
  const remaining = formatQuota(props.remaining)
  const used = formatQuota(props.used)

  return (
    <QuotaDetailsPopover
      title={unit ? `${t('Quota')} (${unit})` : t('Quota')}
      triggerLabel={
        hasQuota
          ? `${t('Available Balance')} ${remaining}; ${t('Used amount')} ${used}`
          : t('No Quota')
      }
      details={[
        { label: t('Available Balance'), value: remaining },
        { label: t('Total Used'), value: used },
      ]}
    >
      {hasQuota ? (
        <span className='grid min-w-0 grid-cols-1 gap-y-1 text-sm tabular-nums'>
          <span
            className={cn(
              props.remaining < 0 && 'text-destructive',
              props.remaining === 0 && 'text-muted-foreground'
            )}
          >
            {remaining}
          </span>
          <span
            data-table-text='secondary'
            className='text-muted-foreground flex items-baseline gap-1 text-xs font-normal'
          >
            <span>{t('Used amount')}</span>
            <span>{used}</span>
          </span>
        </span>
      ) : (
        <StatusBadge
          label={t('No Quota')}
          variant='neutral'
          copyable={false}
          className='-ml-1.5 font-normal'
        />
      )}
    </QuotaDetailsPopover>
  )
}
