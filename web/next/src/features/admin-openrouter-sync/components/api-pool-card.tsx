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
import { useQuery } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toApiError } from '@/lib/api'

import { POOL_REFRESH_MS, poolQueryOptions } from '../api'
import {
  CHANNEL_STATUS_VARIANT,
  KEY_STATUS_VARIANT,
  channelStatusLabel,
  formatRemaining,
  keyStatusLabel,
} from '../lib'
import type { PoolChannel } from '../types'

function ChannelPool(props: { channel: PoolChannel }) {
  const { t } = useTranslation()
  const ch = props.channel
  const keys = ch.keys ?? []
  return (
    <div
      className='flex flex-col gap-2 rounded-lg border p-3'
      data-testid={`api-pool-channel-${ch.channel_id}`}
    >
      <div className='flex flex-wrap items-center gap-x-2 gap-y-1'>
        <span className='font-medium'>
          {ch.channel_name || `#${ch.channel_id}`}
        </span>
        <span className='text-muted-foreground text-xs'>
          {t('(channel #{{id}})', { id: ch.channel_id })}
        </span>
        <StatusBadge
          label={channelStatusLabel(ch.status, t)}
          variant={CHANNEL_STATUS_VARIANT[ch.status] ?? 'neutral'}
          copyable={false}
        />
      </div>
      <div className='flex flex-wrap items-center gap-2'>
        <StatusBadge
          label={t('Enabled {{n}}', { n: ch.enabled_count })}
          variant='success'
          copyable={false}
        />
        <StatusBadge
          label={t('Cooling {{n}}', { n: ch.cooling_count })}
          variant='warning'
          copyable={false}
        />
        <StatusBadge
          label={t('Permanently disabled {{n}}', {
            n: ch.permanent_disabled_count,
          })}
          variant='danger'
          copyable={false}
        />
        <span className='text-muted-foreground text-xs'>
          {t('Total {{n}}', { n: ch.key_count })}
        </span>
      </div>
      <div className='overflow-x-auto'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className='w-12'>#</TableHead>
              <TableHead>{t('Key')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead>{t('Cooldown remaining')}</TableHead>
              <TableHead>{t('Reason')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {keys.map((k) => (
              <TableRow key={k.index}>
                <TableCell className='text-muted-foreground'>
                  {k.index}
                </TableCell>
                <TableCell>
                  <code className='bg-muted rounded px-1.5 py-0.5 font-mono text-xs'>
                    {k.key_prefix}
                  </code>
                </TableCell>
                <TableCell>
                  <StatusBadge
                    label={keyStatusLabel(k.status, t)}
                    variant={KEY_STATUS_VARIANT[k.status] ?? 'neutral'}
                    copyable={false}
                  />
                </TableCell>
                <TableCell>
                  {k.status === 'cooling'
                    ? formatRemaining(k.cooldown_seconds_remaining, t)
                    : '—'}
                </TableCell>
                <TableCell className='max-w-60'>
                  {k.disable_reason ? (
                    <span
                      className='text-muted-foreground block truncate text-xs'
                      title={k.disable_reason}
                    >
                      {k.disable_reason}
                    </span>
                  ) : (
                    '—'
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}

/**
 * Per-key state of every multi-key OpenRouter channel. Refreshes itself; the
 * keys are masked server-side. A failed read is shown as an error (with the
 * last good snapshot kept underneath), never as "no pool configured".
 */
export function ApiPoolCard() {
  const { t } = useTranslation()
  const pool = useQuery({
    ...poolQueryOptions,
    refetchInterval: POOL_REFRESH_MS,
  })
  const channels = pool.data ?? []

  let body
  if (pool.isPending) {
    body = <LoadingState size='sm' className='min-h-24' />
  } else if (pool.isError && pool.data === undefined) {
    body = (
      <Alert variant='destructive' data-testid='api-pool-error'>
        <AlertTitle>{t('Could not load the API pool status')}</AlertTitle>
        <AlertDescription>
          <p>{toApiError(pool.error).message}</p>
          <Button
            variant='outline'
            size='sm'
            className='mt-2'
            onClick={() => void pool.refetch()}
          >
            {t('Retry')}
          </Button>
        </AlertDescription>
      </Alert>
    )
  } else if (channels.length === 0) {
    body = (
      <p className='text-muted-foreground text-sm' data-testid='api-pool-empty'>
        {t(
          'No OpenRouter channel has multi-key mode enabled. Switch a channel to multi-key mode in channel management and this card will show the state of every key in its pool.'
        )}
      </p>
    )
  } else {
    body = (
      <div className='flex flex-col gap-3'>
        {pool.isError && (
          <Alert variant='destructive' data-testid='api-pool-stale'>
            <AlertDescription>
              {t(
                'Refreshing the pool status failed, showing the last snapshot. {{message}}',
                {
                  message: toApiError(pool.error).message,
                }
              )}
            </AlertDescription>
          </Alert>
        )}
        {channels.map((ch) => (
          <ChannelPool key={ch.channel_id} channel={ch} />
        ))}
      </div>
    )
  }

  return (
    <Card size='sm' data-testid='api-pool-card'>
      <CardHeader>
        <CardTitle className='flex items-center justify-between gap-2'>
          <span>{t('API pool status')}</span>
          <Button
            variant='outline'
            size='sm'
            onClick={() => void pool.refetch()}
            disabled={pool.isFetching}
            aria-label={t('Refresh API pool status')}
          >
            <RefreshCw className={pool.isFetching ? 'animate-spin' : ''} />
            {t('Refresh')}
          </Button>
        </CardTitle>
        <CardDescription>
          {t(
            'Refreshes every 10 seconds. A cooling key is restored automatically by the reaper once its cooldown ends.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent>{body}</CardContent>
    </Card>
  )
}
