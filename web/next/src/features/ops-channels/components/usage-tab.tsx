import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toApiError } from '@/lib/api'
import { formatQuota } from '@/lib/money'
import { useMoneyConfig } from '@/lib/status'

import { channelUsageQueryOptions } from '../api'
import { formatCny4, formatPercent } from '../lib/map'
import { USAGE_WINDOWS, type UsageWindow } from '../types'
import { RatioBar } from './shared'

export function UsageTab(props: { channelId: number }) {
  const { t } = useTranslation()
  const money = useMoneyConfig()
  const [window, setWindow] = useState<UsageWindow>('24h')
  const q = useQuery(channelUsageQueryOptions(props.channelId, window))

  const windowLabel: Record<UsageWindow, string> = {
    '5h': t('Last 5 hours'),
    '24h': t('Last 24 hours'),
    '7d': t('Last 7 days'),
    '30d': t('Last 30 days'),
  }

  let body
  if (q.isPending) {
    body = <LoadingState />
  } else if (q.isError) {
    body = (
      <ErrorState
        title={t('Could not load channel usage')}
        description={toApiError(q.error).message}
        onRetry={() => void q.refetch()}
      />
    )
  } else {
    const u = q.data
    const errRate =
      u.total.requests > 0 ? u.total.errors / u.total.requests : null
    body = (
      <div className='grid gap-4' data-testid='usage-body'>
        <dl className='grid grid-cols-2 gap-3 text-sm'>
          <div>
            <dt className='text-muted-foreground'>{t('Requests')}</dt>
            <dd data-testid='usage-requests'>{u.total.requests}</dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>{t('Error rate')}</dt>
            <dd>{errRate === null ? '--' : formatPercent(errRate)}</dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>{t('Tokens')}</dt>
            <dd>{u.total.promptTokens + u.total.completionTokens}</dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>{t('Billed')}</dt>
            <dd>{formatQuota(u.total.quota, money, 4)}</dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>{t('Upstream cost')}</dt>
            <dd data-testid='usage-cost'>{formatCny4(u.total.costCny4)}</dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>{t('Plan fee for window')}</dt>
            <dd>
              {u.planMonthlyFeeCny4 > 0 ? formatCny4(u.proratedFeeCny4) : '--'}
            </dd>
          </div>
        </dl>

        {u.utilization != null ? (
          <div className='grid gap-1'>
            <div className='flex justify-between text-sm'>
              <span>{t('Plan utilization')}</span>
              <span data-testid='usage-utilization'>
                {formatPercent(u.utilization)}
              </span>
            </div>
            <RatioBar
              ratio={u.utilization}
              label={t('Plan utilization')}
              warnAt={1}
            />
            {u.utilization > 1 && (
              <p className='text-destructive text-xs'>
                {t('Cost exceeds the plan fee for this window.')}
              </p>
            )}
          </div>
        ) : (
          <p className='text-muted-foreground text-xs'>
            {t(
              'Set a monthly plan fee in Plan settings to see utilization.'
            )}
          </p>
        )}

        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Key')}</TableHead>
              <TableHead className='text-end'>{t('Requests')}</TableHead>
              <TableHead className='text-end'>{t('Errors')}</TableHead>
              <TableHead className='text-end'>{t('Cost')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {u.keys.map((k) => (
              <TableRow key={k.keyIdx} data-testid={`usage-key-${k.keyIdx}`}>
                <TableCell>
                  {k.keyIdx < 0
                    ? t('Unattributed')
                    : t('Key #{{index}}', { index: k.keyIdx })}
                </TableCell>
                <TableCell className='text-end'>{k.requests}</TableCell>
                <TableCell className='text-end'>{k.errors}</TableCell>
                <TableCell className='text-end'>
                  {formatCny4(k.costCny4)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    )
  }

  return (
    <div className='grid gap-4'>
      <div className='flex flex-wrap gap-1' role='group'>
        {USAGE_WINDOWS.map((w) => (
          <Button
            key={w}
            size='sm'
            variant={w === window ? 'default' : 'outline'}
            aria-pressed={w === window}
            onClick={() => setWindow(w)}
          >
            {windowLabel[w]}
          </Button>
        ))}
      </div>
      {body}
    </div>
  )
}
