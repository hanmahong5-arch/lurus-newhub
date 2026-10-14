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

import { BadgeCell } from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import { LongText } from '@/components/long-text'
import { StatusBadge } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useQuota } from '@/hooks/use-quota'

import {
  USER_ROLES,
  USER_STATUS,
  USER_STATUSES,
  isUserDeleted,
} from '../constants'
import type { AdminUser } from '../types'
import { DataTableRowActions } from './data-table-row-actions'
import { UserQuotaCell } from './user-quota-cell'

export function useUsersColumns(): ColumnDef<AdminUser>[] {
  const { t } = useTranslation()
  const { symbol, isTokens } = useQuota()
  const quotaUnit = isTokens ? t('Tokens') : symbol

  return useMemo<ColumnDef<AdminUser>[]>(
    () => [
      {
        accessorKey: 'id',
        header: t('ID'),
        cell: ({ row }) => (
          <TableId
            value={row.original.id}
            className='w-[60px] [font-family:inherit] text-sm'
          />
        ),
        enableSorting: false,
        size: 80,
        meta: { mobileOrder: 10 },
      },
      {
        accessorKey: 'username',
        header: t('Username'),
        cell: ({ row }) => {
          const { username, display_name: displayName, remark } = row.original
          return (
            <div className='flex min-w-[160px] flex-col gap-1'>
              <div className='flex items-center gap-2'>
                <LongText className='max-w-[140px] text-sm font-normal'>
                  {username}
                </LongText>
                {remark && (
                  <Tooltip>
                    <TooltipTrigger
                      render={
                        <StatusBadge
                          variant='success'
                          copyable={false}
                          className='font-normal'
                        />
                      }
                    >
                      <LongText className='max-w-[80px]'>{remark}</LongText>
                    </TooltipTrigger>
                    <TooltipContent>
                      <p className='text-xs'>{remark}</p>
                    </TooltipContent>
                  </Tooltip>
                )}
              </div>
              {displayName && displayName !== username && (
                <div
                  data-table-text='secondary'
                  className='text-muted-foreground max-w-[180px] text-xs font-normal'
                >
                  <LongText>{displayName}</LongText>
                </div>
              )}
            </div>
          )
        },
        enableHiding: false,
        enableSorting: false,
        size: 220,
        meta: { mobileTitle: true },
      },
      {
        accessorKey: 'status',
        header: t('Status'),
        cell: ({ row }) => {
          const user = row.original
          const key = isUserDeleted(user) ? USER_STATUS.DELETED : user.status
          const config = USER_STATUSES[key as keyof typeof USER_STATUSES]
          if (!config) return null
          return (
            <Tooltip>
              <TooltipTrigger render={<div className='-ml-1.5 cursor-help' />}>
                <StatusBadge
                  label={t(config.labelKey)}
                  variant={isUserDeleted(user) ? 'neutral' : config.variant}
                  copyable={false}
                  className='font-normal'
                />
              </TooltipTrigger>
              <TooltipContent>
                <p className='text-xs'>
                  {t('Requests:')} {(user.request_count ?? 0).toLocaleString()}
                </p>
              </TooltipContent>
            </Tooltip>
          )
        },
        enableSorting: false,
        size: 120,
        meta: { mobileBadge: true },
      },
      {
        id: 'quota',
        accessorKey: 'quota',
        header: quotaUnit
          ? `${t('Available Balance')} (${quotaUnit})`
          : t('Available Balance'),
        cell: ({ row }) => (
          <UserQuotaCell
            remaining={row.original.quota}
            used={row.original.used_quota}
          />
        ),
        enableSorting: false,
        size: 180,
        minSize: 160,
        meta: { mobileOrder: 40 },
      },
      {
        accessorKey: 'group',
        header: t('User Group'),
        cell: ({ row }) => (
          <BadgeCell>
            <GroupBadge group={row.original.group} className='font-normal' />
          </BadgeCell>
        ),
        enableSorting: false,
        size: 140,
        meta: { mobileOrder: 30 },
      },
      {
        accessorKey: 'role',
        header: t('Role'),
        cell: ({ row }) => {
          const config = USER_ROLES[row.original.role as keyof typeof USER_ROLES]
          if (!config) return null
          return <span className='text-sm'>{t(config.labelKey)}</span>
        },
        enableSorting: false,
        size: 120,
        meta: { mobileOrder: 20 },
      },
      {
        accessorKey: 'tenant_id',
        header: t('Tenant'),
        cell: ({ row }) =>
          row.original.tenant_id ? (
            <LongText className='text-muted-foreground max-w-[140px] font-mono text-xs'>
              {row.original.tenant_id}
            </LongText>
          ) : (
            <span className='text-muted-foreground text-sm'>-</span>
          ),
        enableSorting: false,
        size: 160,
        meta: { mobileHidden: true },
      },
      {
        id: 'actions',
        header: () => t('Actions'),
        cell: ({ row }) => <DataTableRowActions user={row.original} />,
        enableHiding: false,
        enableSorting: false,
        meta: { pinned: 'right' as const },
      },
    ],
    [t, quotaUnit]
  )
}
