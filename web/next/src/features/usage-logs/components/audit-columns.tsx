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
import type { ColumnDef } from '@tanstack/react-table'
import { Eye } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { TruncatedCell } from '@/components/data-table'
import { formatTimestampToDate } from '@/components/format'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'

import {
  detailsPreview,
  shortHash,
  type AuditRow,
  type AuditScope,
} from '../lib/audit'
import { Dash } from './log-cells'

/** Columns of the audit table; the platform feed adds tenant and hash. */
export function useAuditColumns(
  scope: AuditScope,
  onOpen: (row: AuditRow) => void
): ColumnDef<AuditRow>[] {
  const { t } = useTranslation()
  return useMemo(() => {
    const columns: ColumnDef<AuditRow>[] = [
      {
        accessorKey: 'timestamp',
        header: t('Time'),
        enableHiding: false,
        size: 170,
        cell: ({ row }) => (
          <span className='font-mono text-xs tabular-nums'>
            {row.original.timestamp > 0
              ? formatTimestampToDate(row.original.timestamp)
              : '--'}
          </span>
        ),
        meta: { label: t('Time'), mobileTitle: true },
      },
      {
        accessorKey: 'action',
        header: t('Action'),
        size: 200,
        cell: ({ row }) =>
          row.original.action ? (
            <StatusBadge
              label={row.original.action}
              autoColor={row.original.action.split('.')[0]}
              copyText={row.original.action}
              size='sm'
              className='font-mono'
            />
          ) : (
            <Dash />
          ),
        meta: { label: t('Action'), mobileBadge: true },
      },
      {
        id: 'actor',
        header: t('Operator'),
        accessorFn: (r) => r.actorType,
        cell: ({ row }) => (
          <span className='text-sm whitespace-nowrap'>
            {row.original.actorType || '-'}
            {row.original.actorId > 0 && (
              <span className='text-muted-foreground font-mono text-xs'>
                {' '}
                #{row.original.actorId}
              </span>
            )}
          </span>
        ),
        meta: { label: t('Operator') },
      },
      {
        id: 'resource',
        header: t('Resource'),
        accessorFn: (r) => r.resource,
        cell: ({ row }) =>
          row.original.resource ? (
            <span className='text-sm whitespace-nowrap'>
              {row.original.resource}
              {row.original.resourceId > 0 && (
                <span className='text-muted-foreground font-mono text-xs'>
                  {' '}
                  #{row.original.resourceId}
                </span>
              )}
            </span>
          ) : (
            <Dash />
          ),
        meta: { label: t('Resource') },
      },
    ]
    if (scope === 'platform') {
      columns.push({
        accessorKey: 'tenantId',
        header: t('Tenant'),
        cell: ({ row }) => (
          <span className='font-mono text-xs'>
            {row.original.tenantId || '-'}
          </span>
        ),
        meta: { label: t('Tenant') },
      })
    }
    columns.push(
      {
        accessorKey: 'ip',
        header: t('IP'),
        cell: ({ row }) => (
          <span className='font-mono text-xs'>{row.original.ip || '—'}</span>
        ),
        meta: { label: t('IP') },
      },
      {
        accessorKey: 'requestId',
        header: t('Request ID'),
        cell: ({ row }) =>
          row.original.requestId ? (
            <StatusBadge
              label={row.original.requestId}
              copyText={row.original.requestId}
              variant='neutral'
              size='sm'
              className='max-w-40 font-mono'
            />
          ) : (
            <Dash />
          ),
        meta: { label: t('Request ID') },
      }
    )
    if (scope === 'platform') {
      columns.push({
        accessorKey: 'rowHash',
        header: t('Chain hash'),
        cell: ({ row }) =>
          row.original.rowHash ? (
            <span
              className='font-mono text-xs'
              title={row.original.rowHash}
            >
              {shortHash(row.original.rowHash)}
            </span>
          ) : (
            <span className='text-muted-foreground text-xs'>
              {t('Unchained')}
            </span>
          ),
        meta: { label: t('Chain hash') },
      })
    }
    columns.push({
      accessorKey: 'details',
      header: t('Details'),
      size: 220,
      cell: ({ row }) => {
        const preview = detailsPreview(row.original.details)
        return (
          <div className='flex max-w-[260px] items-center gap-1'>
            <TruncatedCell className='text-muted-foreground min-w-0 flex-1 font-mono text-xs'>
              {preview || '--'}
            </TruncatedCell>
            <Button
              type='button'
              variant='ghost'
              size='icon'
              className='size-7 shrink-0'
              aria-label={t('View details')}
              onClick={(e) => {
                e.stopPropagation()
                onOpen(row.original)
              }}
            >
              <Eye />
            </Button>
          </div>
        )
      },
      meta: { label: t('Details') },
    })
    return columns
  }, [scope, t, onOpen])
}
