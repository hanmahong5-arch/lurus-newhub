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
import { Coins, Gauge, Hash, Layers, Zap, type LucideIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { formatCompactNumber, formatNumber } from '@/components/format'
import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { currentLocale } from '@/lib/locale'
import { formatQuota, type MoneyConfig } from '@/lib/money'
import { cn } from '@/lib/utils'

import { perMinute, type Metrics } from '../../lib/series'
import type { LoadState } from '../../lib/state'

const MAX_INLINE_CHARS = 9

function plain(value: number): { shown: string; full: string } {
  const locale = currentLocale()
  const full = formatNumber(value, locale)
  return {
    shown:
      full.length > MAX_INLINE_CHARS
        ? formatCompactNumber(value, locale)
        : full,
    full,
  }
}

interface Cell {
  id: string
  title: string
  desc: string
  icon: LucideIcon
  tone: IconBadgeTone
  shown: string
  full: string
}

export interface StatStripProps {
  state: LoadState
  totals: Metrics
  /** Span the numbers cover, unix seconds (for the per-minute averages). */
  start: number
  end: number
  money: MoneyConfig | null
}

/** Count / quota / tokens / avg RPM / avg TPM over the selected range. */
export function StatStrip(props: StatStripProps) {
  const { t } = useTranslation()
  const { totals } = props
  const locale = currentLocale()
  const rate = (v: number) => {
    const full = formatNumber(Math.round(v * 100) / 100, locale)
    return { shown: full, full }
  }
  const quota = formatQuota(totals.quota, props.money)
  const cells: Cell[] = [
    {
      id: 'count',
      title: t('Total Count'),
      desc: t('Statistical count'),
      icon: Hash,
      tone: 'info',
      ...plain(totals.count),
    },
    {
      id: 'quota',
      title: t('Total Quota'),
      desc: t('Statistical quota'),
      icon: Coins,
      tone: 'success',
      shown: quota,
      full: quota,
    },
    {
      id: 'tokens',
      title: t('Total Tokens'),
      desc: t('Statistical tokens'),
      icon: Layers,
      tone: 'chart-4',
      ...plain(totals.tokens),
    },
    {
      id: 'avg-rpm',
      title: t('Average RPM'),
      desc: t('Requests per minute'),
      icon: Gauge,
      tone: 'chart-2',
      ...rate(perMinute(totals.count, props.start, props.end)),
    },
    {
      id: 'avg-tpm',
      title: t('Average TPM'),
      desc: t('Tokens per minute'),
      icon: Zap,
      tone: 'warning',
      ...rate(perMinute(totals.tokens, props.start, props.end)),
    },
  ]
  return (
    <div
      data-testid='dashboard-stat-strip'
      className='overflow-hidden rounded-lg border'
    >
      <div className='bg-border grid min-w-0 grid-cols-2 gap-px sm:grid-cols-3 lg:grid-cols-5'>
        {cells.map((c, idx) => {
          const Icon = c.icon
          return (
            <div
              key={c.id}
              data-testid={`stat-${c.id}`}
              className={cn(
                'bg-card min-w-0 px-3 py-2.5 sm:px-5 sm:py-4',
                idx === cells.length - 1 &&
                  cells.length % 2 !== 0 &&
                  'col-span-2 sm:col-span-1'
              )}
            >
              <div className='flex min-w-0 items-center gap-2'>
                <IconBadge tone={c.tone} size='stat'>
                  <Icon />
                </IconBadge>
                <div className='text-muted-foreground truncate text-[11px] leading-4 font-medium tracking-wide uppercase sm:text-xs'>
                  {c.title}
                </div>
              </div>
              {props.state === 'loading' && (
                <Skeleton className='mt-2 h-6 w-20 sm:h-7' />
              )}
              {props.state === 'error' && (
                <div className='text-muted-foreground mt-2 font-mono text-base font-bold sm:text-2xl'>
                  --
                </div>
              )}
              {props.state === 'ok' && (
                <>
                  <div
                    className='mt-1 max-w-full truncate font-mono text-base leading-tight font-bold tracking-tight tabular-nums sm:mt-2 sm:text-2xl'
                    title={c.full}
                  >
                    {c.shown}
                  </div>
                  <div className='text-muted-foreground/70 mt-1 hidden text-xs md:block'>
                    {c.desc}
                  </div>
                </>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}
