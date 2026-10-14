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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { formatTimestampToDate } from '@/components/format'
import { StatusBadge } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useQuota } from '@/hooks/use-quota'

import { REDEMPTION_FILTER_EXPIRED } from '../constants'
import { isRedemptionExpired, isTimestampExpired } from '../lib/utils'
import type { Redemption } from '../types'
import { DataTableRowActions } from './data-table-row-actions'
import { RedemptionCodeCell } from './redemption-code-cell'
import { RedemptionStatusBadge } from './redemption-status-cell'

export function useRedemptionsColumns(): ColumnDef<Redemption>[] {
  const { t } = useTranslation()
  const { formatQuota } = useQuota()

  return useMemo<ColumnDef<Redemption>[]>(
    () => [
      {
        id: 'select',
        header: ({ table }) => (
          <Checkbox
            checked={table.getIsAllPageRowsSelected()}
            indeterminate={table.getIsSomePageRowsSelected()}
            onCheckedChange={(value) =>
              table.toggleAllPageRowsSelected(!!value)
            }
            aria-label={t('Select all')}
            className='translate-y-[2px]'
          />
        ),
        cell: ({ row }) => (
          <Checkbox
            checked={row.getIsSelected()}
            onCheckedChange={(value) => row.toggleSelected(!!value)}
            aria-label={t('Select row')}
            className='translate-y-[2px]'
          />
        ),
        enableSorting: false,
        enableHiding: false,
        size: 40,
      },
      {
        accessorKey: 'id',
        header: t('ID'),
        meta: { mobileHidden: true },
        cell: ({ row }) => (
          <TableId value={row.original.id} className='w-[60px]' />
        ),
        enableSorting: false,
        size: 80,
      },
      {
        accessorKey: 'name',
        header: t('Name'),
        meta: { mobileTitle: true },
        cell: ({ row }) => (
          <span className='font-medium'>{row.original.name}</span>
        ),
        enableHiding: false,
        enableSorting: false,
        size: 180,
      },
      {
        accessorKey: 'status',
        header: t('Status'),
        meta: { mobileBadge: true },
        cell: ({ row }) => (
          <RedemptionStatusBadge
            redemption={row.original}
            className='-ml-1.5'
          />
        ),
        filterFn: (row, id, value: string[]) => {
          if (
            value.includes(REDEMPTION_FILTER_EXPIRED) &&
            isRedemptionExpired(row.original)
          ) {
            return true
          }
          return value.includes(String(row.getValue(id)))
        },
        enableSorting: false,
        size: 120,
      },
      {
        id: 'code',
        accessorKey: 'key',
        header: t('Redemption Code'),
        cell: ({ row }) => <RedemptionCodeCell value={row.original.key} />,
        enableSorting: false,
        size: 320,
      },
      {
        accessorKey: 'quota',
        header: t('Quota'),
        cell: ({ row }) => (
          <StatusBadge
            label={formatQuota(row.original.quota)}
            variant='neutral'
            copyable={false}
            className='-ml-1.5'
          />
        ),
        enableSorting: false,
        size: 120,
      },
      {
        accessorKey: 'created_time',
        header: t('Created'),
        meta: { mobileHidden: true },
        cell: ({ row }) => (
          <div className='min-w-[160px] font-mono text-sm'>
            {formatTimestampToDate(row.original.created_time)}
          </div>
        ),
        enableSorting: false,
        size: 180,
      },
      {
        accessorKey: 'expired_time',
        header: t('Expires'),
        meta: { mobileHidden: true },
        cell: ({ row }) => {
          const expiredTime = row.original.expired_time
          if (expiredTime === 0) {
            return (
              <StatusBadge
                label={t('Never')}
                variant='neutral'
                copyable={false}
                className='-ml-1.5'
              />
            )
          }
          return (
            <div
              className={`min-w-[160px] font-mono text-sm ${
                isTimestampExpired(expiredTime) ? 'text-destructive' : ''
              }`}
            >
              {formatTimestampToDate(expiredTime)}
            </div>
          )
        },
        enableSorting: false,
        size: 180,
      },
      {
        accessorKey: 'used_user_id',
        header: t('Redeemed By'),
        meta: { mobileHidden: true },
        cell: ({ row }) => {
          const { used_user_id: userId, redeemed_time: redeemedAt } =
            row.original
          if (!userId) {
            return <span className='text-muted-foreground text-sm'>-</span>
          }
          return (
            <Tooltip>
              <TooltipTrigger
                render={
                  <StatusBadge
                    label={t('User {{id}}', { id: userId })}
                    variant='neutral'
                    copyable={false}
                    className='cursor-help'
                  />
                }
              />
              <TooltipContent>
                <div className='space-y-1 text-xs'>
                  <div>
                    {t('User ID:')} {userId}
                  </div>
                  {redeemedAt > 0 && (
                    <div>
                      {t('Redeemed:')} {formatTimestampToDate(redeemedAt)}
                    </div>
                  )}
                </div>
              </TooltipContent>
            </Tooltip>
          )
        },
        enableSorting: false,
        size: 140,
      },
      {
        id: 'actions',
        header: () => t('Actions'),
        cell: ({ row }) => <DataTableRowActions redemption={row.original} />,
        enableHiding: false,
        enableSorting: false,
        meta: { pinned: 'right' as const },
      },
    ],
    [t, formatQuota]
  )
}
