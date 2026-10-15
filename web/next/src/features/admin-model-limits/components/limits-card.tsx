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
import { Gauge, Pencil, Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { TitledCard } from '@/components/ui/titled-card'
import { formatNumber } from '@/components/format'
import { toApiError } from '@/lib/api'

import type { ModelLimit } from '../types'

interface LimitsCardProps {
  rows: ModelLimit[] | undefined
  loading: boolean
  error: unknown
  onRetry: () => void
  onCreate: () => void
  onEdit: (row: ModelLimit) => void
  onDelete: (row: ModelLimit) => void
}

function Cap(props: { value: number }) {
  const { t } = useTranslation()
  if (props.value > 0) {
    return <span className='tabular-nums'>{formatNumber(props.value)}</span>
  }
  return <span className='text-muted-foreground'>{t('Unlimited')}</span>
}

export function LimitsCard(props: LimitsCardProps) {
  const { t } = useTranslation()
  const { rows } = props

  // A failed read is never an empty list: under a "0 = unlimited" subtitle,
  // zero rows would read as "every model is uncapped here".
  let count: string
  if (props.loading) count = '…'
  else if (props.error != null) count = t('Count unavailable')
  else count = t('{{count}} model limits', { count: rows?.length ?? 0 })

  let body
  if (props.loading) {
    body = <LoadingState className='min-h-[160px]' />
  } else if (props.error != null) {
    body = (
      <div data-testid='limits-error'>
        <ErrorState
          className='min-h-[160px]'
          title={t('Couldn’t load model rate limits')}
          description={toApiError(props.error).message}
          onRetry={props.onRetry}
        />
      </div>
    )
  } else if (!rows || rows.length === 0) {
    body = (
      <EmptyState
        className='min-h-[160px]'
        icon={Gauge}
        title={t('No per-model limits set for this tenant.')}
        description={t('Every model is uncapped until a limit is added.')}
        action={
          <Button size='sm' onClick={props.onCreate}>
            <Plus />
            {t('New model limit')}
          </Button>
        }
      />
    )
  } else {
    body = (
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Model')}</TableHead>
            <TableHead className='text-right'>{t('RPM limit')}</TableHead>
            <TableHead className='text-right'>{t('TPM limit')}</TableHead>
            <TableHead className='w-24 text-right'>{t('Actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => (
            <TableRow key={row.id} data-testid={`limit-row-${row.id}`}>
              <TableCell className='font-mono text-xs font-medium'>
                {row.model}
              </TableCell>
              <TableCell className='text-right font-mono text-xs'>
                <Cap value={row.rate_limit_rpm} />
              </TableCell>
              <TableCell className='text-right font-mono text-xs'>
                <Cap value={row.rate_limit_tpm} />
              </TableCell>
              <TableCell className='text-right whitespace-nowrap'>
                <Button
                  variant='ghost'
                  size='icon-sm'
                  aria-label={t('Edit {{model}}', { model: row.model })}
                  onClick={() => props.onEdit(row)}
                >
                  <Pencil />
                </Button>
                <Button
                  variant='ghost'
                  size='icon-sm'
                  aria-label={t('Delete {{model}}', { model: row.model })}
                  onClick={() => props.onDelete(row)}
                >
                  <Trash2 />
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    )
  }

  return (
    <TitledCard
      title={t('Rate limits')}
      description={t('Per-tenant · per-model RPM / TPM caps · 0 = unlimited')}
      icon={<Gauge className='size-4' />}
      action={<Badge variant='secondary'>{count}</Badge>}
      contentClassName='p-0'
    >
      {body}
    </TitledCard>
  )
}
