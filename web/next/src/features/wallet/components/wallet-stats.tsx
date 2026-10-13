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
import { Activity, BarChart3, Lock, WalletCards } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { formatCompactNumber } from '@/components/format'
import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { useQuota } from '@/hooks/use-quota'
import type { CurrentUser } from '@/lib/user'

import { formatCny } from '../lib/format'
import type { WalletInfo } from '../types'

interface Stat {
  key: string
  label: string
  value: string
  description: string
  icon: typeof WalletCards
  tone: IconBadgeTone
}

export function WalletStats(props: {
  wallet: WalletInfo | undefined
  walletLoading: boolean
  walletError: string | null
  user: CurrentUser | undefined
  userLoading: boolean
}) {
  const { t } = useTranslation()
  const { formatQuota } = useQuota()
  const { wallet, user } = props

  // Without a linked platform wallet the server converts the console quota
  // to CNY; showing that next to quota-priced figures (and the dashboard's
  // balance) put two currencies on one screen. Such a sign-in shows its
  // console quota in the console's display currency instead, and has no
  // frozen amount to show. A platform wallet is genuinely CNY.
  const internal = wallet?.source === 'internal'
  const money = (value: number | null | undefined) => {
    if (props.walletError) return '--'
    if (props.walletLoading) return '...'
    return formatCny(value)
  }
  // Same field and rule as the dashboard's balance (remaining_quota;
  // negative = unlimited), so the two pages always agree.
  const consoleBalance = () => {
    const remaining = user?.remaining_quota
    if (typeof remaining !== 'number') return '--'
    if (remaining < 0) return '∞'
    return formatQuota(remaining)
  }
  let walletCaption = t('Available {{amount}}', {
    amount: formatCny(wallet?.available),
  })
  if (props.walletError) walletCaption = t('Wallet unavailable')
  else if (internal) walletCaption = t('Remaining console quota')

  const stats: Stat[] = [
    {
      key: 'wallet',
      label: internal ? t('Quota balance') : t('Wallet balance'),
      value: internal ? consoleBalance() : money(wallet?.balance),
      description: walletCaption,
      icon: WalletCards,
      tone: 'success',
    },
    ...(internal
      ? []
      : [
          {
            key: 'frozen',
            label: t('Frozen'),
            value: money(wallet?.frozen),
            description: t('Held for in-flight requests'),
            icon: Lock,
            tone: 'warning' as IconBadgeTone,
          },
        ]),
    {
      key: 'usage',
      label: t('Total Usage'),
      value: user ? formatQuota(user.used_quota) : '--',
      description: t('Total consumed quota'),
      icon: BarChart3,
      tone: 'info',
    },
    {
      key: 'requests',
      label: t('API Requests'),
      value: user ? formatCompactNumber(user.request_count ?? 0) : '--',
      description: t('Total requests made'),
      icon: Activity,
      tone: 'chart-4',
    },
  ]

  if (props.userLoading && props.walletLoading) {
    return (
      <div
        role='status'
        aria-label={t('Loading...')}
        className='grid grid-cols-2 divide-x rounded-lg border lg:grid-cols-4'
      >
        {stats.map((s) => (
          <div key={s.key} className='min-w-0 px-2.5 py-2.5 sm:px-5 sm:py-4'>
            <Skeleton className='h-3.5 w-full' />
            <Skeleton className='mt-2 h-6 w-full sm:h-7' />
          </div>
        ))}
      </div>
    )
  }

  return (
    <div
      className={`grid grid-cols-2 divide-x rounded-lg border max-lg:[&>*:nth-child(n+3)]:border-t ${stats.length === 3 ? 'lg:grid-cols-3' : 'lg:grid-cols-4'}`}
      data-testid='wallet-stats'
    >
      {stats.map((item) => (
        <div
          key={item.key}
          data-testid={`stat-${item.key}`}
          className='min-w-0 px-2.5 py-2.5 sm:px-5 sm:py-4'
        >
          <div className='flex items-center gap-1.5 sm:gap-2.5'>
            <IconBadge tone={item.tone} size='stat'>
              <item.icon />
            </IconBadge>
            <div className='text-muted-foreground truncate text-[11px] font-medium tracking-wider uppercase sm:text-xs'>
              {item.label}
            </div>
          </div>
          <div className='text-foreground mt-1.5 font-mono text-sm font-bold tracking-tight break-all tabular-nums sm:mt-2.5 sm:text-2xl'>
            {item.value}
          </div>
          <div className='text-muted-foreground/60 mt-1 text-xs'>
            {item.description}
          </div>
        </div>
      ))}
    </div>
  )
}
