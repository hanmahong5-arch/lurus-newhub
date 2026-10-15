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

import { formatNumber, formatTimestampToDate } from '@/components/format'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Badge } from '@/components/ui/badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { formatLatency, truncate } from '../lib/health'
import { channelTypeLabel } from '../lib/channel-type'
import type { PoolChannel } from '../types'
import { HealthBadge } from './health-badge'

const STATUS_VARIANT: Record<string, StatusVariant> = {
  enabled: 'success',
  auto_disabled: 'danger',
  manually_disabled: 'neutral',
}

const LAST_ERROR_MAX = 40

function Meta(props: { label: string; children: React.ReactNode }) {
  return (
    <span className='text-muted-foreground inline-flex items-baseline gap-1 text-xs'>
      <span>{props.label}</span>
      <span className='text-foreground font-mono'>{props.children}</span>
    </span>
  )
}

export function ChannelSection(props: { channel: PoolChannel }) {
  const { t } = useTranslation()
  const ch = props.channel

  const statusLabels: Record<string, string> = {
    enabled: t('Enabled'),
    auto_disabled: t('Auto-disabled'),
    manually_disabled: t('Manually disabled'),
  }

  return (
    <section
      className='grid gap-2 border-t pt-3 first:border-t-0 first:pt-0'
      aria-label={ch.name}
    >
      <div className='flex flex-wrap items-center gap-x-4 gap-y-1.5'>
        <h3 className='text-sm font-semibold'>{ch.name}</h3>
        <Badge variant='outline'>{channelTypeLabel(ch.type)}</Badge>
        <StatusBadge
          copyable={false}
          variant={STATUS_VARIANT[ch.status] ?? 'warning'}
          label={statusLabels[ch.status] ?? ch.status}
        />
        <Meta label={t('Priority')}>{ch.priority}</Meta>
        <Meta label={t('Keys')}>
          {t(
            '{{enabled}} enabled / {{cooling}} cooling / {{disabled}} disabled',
            {
              enabled: formatNumber(ch.keys.enabled),
              cooling: formatNumber(ch.keys.cooling),
              disabled: formatNumber(ch.keys.disabled),
            }
          )}
        </Meta>
        <Meta label={t('Last test')}>
          {formatTimestampToDate(ch.test_time)}
        </Meta>
      </div>

      {ch.models.length === 0 ? (
        <p className='text-muted-foreground text-sm'>
          {t('No models configured')}
        </p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Model')}</TableHead>
              <TableHead>{t('Health')}</TableHead>
              <TableHead className='text-right'>{t('Latency')}</TableHead>
              <TableHead>{t('Last probe')}</TableHead>
              <TableHead className='text-right'>
                {t('Requests (24h)')}
              </TableHead>
              <TableHead className='text-right'>{t('Errors (24h)')}</TableHead>
              <TableHead>{t('Last error')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {ch.models.map((m) => (
              <TableRow key={m.model}>
                <TableCell className='font-mono text-xs font-medium'>
                  {m.model}
                </TableCell>
                <TableCell data-testid={`pool-health-${ch.id}-${m.model}`}>
                  <HealthBadge health={m.health} />
                </TableCell>
                <TableCell className='text-right font-mono text-xs tabular-nums'>
                  {formatLatency(m.health?.latency_ms)}
                </TableCell>
                <TableCell className='font-mono text-xs whitespace-nowrap'>
                  {formatTimestampToDate(m.health?.last_probe_at)}
                </TableCell>
                <TableCell className='text-right font-mono text-xs tabular-nums'>
                  {formatNumber(m.requests_24h)}
                </TableCell>
                <TableCell className='text-right font-mono text-xs tabular-nums'>
                  {formatNumber(m.errors_24h)}
                </TableCell>
                <TableCell
                  className='text-muted-foreground font-mono text-xs'
                  title={m.health?.last_error || undefined}
                  data-testid={`pool-last-error-${ch.id}-${m.model}`}
                >
                  {truncate(m.health?.last_error, LAST_ERROR_MAX)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  )
}
