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
import { Coins, Gauge, Hash, Zap, type LucideIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { formatCompactNumber, formatNumber } from '@/components/format'
import { Card, CardContent } from '@/components/ui/card'
import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import type { Metrics } from '@/features/dashboard/lib/series'
import { currentLocale } from '@/lib/locale'
import { formatQuota, type MoneyConfig } from '@/lib/money'
import type { LogStat } from '@/features/usage-logs/lib/logs'

interface KpiRowProps {
  loading: boolean
  failed: boolean
  totals: Metrics
  stat: LogStat | undefined
  money: MoneyConfig | null
}

interface Cell {
  id: string
  label: string
  hint: string
  icon: LucideIcon
  tone: IconBadgeTone
  value: string
}

export function KpiRow(props: KpiRowProps) {
  const { t } = useTranslation()
  const locale = currentLocale()
  const { totals, stat } = props

  const cells: Cell[] = [
    {
      id: 'requests',
      label: t('Requests'),
      hint: t('In selected period'),
      icon: Hash,
      tone: 'info',
      value: formatCompactNumber(totals.count, locale),
    },
    {
      id: 'spend',
      label: t('Spend'),
      hint: t('Quota in period'),
      icon: Coins,
      tone: 'warning',
      value: formatQuota(totals.quota, props.money),
    },
    {
      id: 'tokens',
      label: t('Tokens'),
      hint: t('Prompt + completion'),
      icon: Zap,
      tone: 'primary',
      value: formatCompactNumber(totals.tokens, locale),
    },
    {
      id: 'rpm',
      label: t('RPM'),
      hint: t('Live window'),
      icon: Gauge,
      tone: 'success',
      value:
        stat == null
          ? '—'
          : formatNumber(Math.round(stat.rpm * 100) / 100, locale),
    },
  ]

  return (
    <div
      className='grid grid-cols-2 gap-3 xl:grid-cols-4'
      data-testid='activity-kpi-row'
    >
      {cells.map((c) => (
        <Card key={c.id} size='sm' data-testid={`activity-kpi-${c.id}`}>
          <CardContent className='flex items-start gap-3'>
            <IconBadge tone={c.tone} size='stat'>
              <c.icon />
            </IconBadge>
            <div className='min-w-0 flex-1'>
              <div className='text-muted-foreground text-xs'>
                {c.label}
                <span className='ms-1 opacity-70'>· {c.hint}</span>
              </div>
              {props.loading ? (
                <Skeleton className='mt-1 h-7 w-20' />
              ) : (
                <div className='mt-1 text-xl font-semibold tabular-nums'>
                  {props.failed && c.id === 'rpm' ? '—' : c.value}
                </div>
              )}
            </div>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}
