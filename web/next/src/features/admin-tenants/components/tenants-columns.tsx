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

import { StatusBadge } from '@/components/status-badge'
import { useQuota } from '@/hooks/use-quota'

import type { Tenant } from '../types'
import {
  formatIsoDate,
  planLabel,
  planVariant,
  statusLabel,
  statusVariant,
} from '../lib/labels'
import { TenantRowActions } from './tenant-row-actions'
import { Limit } from './ui-bits'

export function useTenantsColumns(): ColumnDef<Tenant>[] {
  const { t } = useTranslation()
  const { formatQuota } = useQuota()

  return useMemo<ColumnDef<Tenant>[]>(
    () => [
      {
        accessorKey: 'name',
        header: t('Tenant'),
        meta: { mobileTitle: true },
        cell: ({ row }) => {
          const tenant = row.original
          const initial = (tenant.name || tenant.slug || '?')
            .slice(0, 1)
            .toUpperCase()
          return (
            <div className='flex min-w-0 items-center gap-2.5'>
              <div
                aria-hidden='true'
                className='bg-muted text-foreground flex size-8 shrink-0 items-center justify-center rounded-md text-sm font-semibold'
              >
                {initial}
              </div>
              <div className='min-w-0'>
                <div className='truncate font-medium'>{tenant.name}</div>
                <div className='text-muted-foreground truncate font-mono text-xs'>
                  {tenant.slug}
                </div>
              </div>
            </div>
          )
        },
        enableHiding: false,
        enableSorting: false,
        size: 240,
      },
      {
        accessorKey: 'status',
        header: t('Status'),
        meta: { mobileBadge: true },
        cell: ({ row }) => (
          <StatusBadge
            label={statusLabel(row.original.status, t)}
            variant={statusVariant(row.original.status)}
            copyable={false}
            className='-ml-1.5'
            data-testid={`tenant-status-${row.original.id}`}
          />
        ),
        filterFn: (row, id, value: string[]) =>
          value.includes(String(row.getValue(id))),
        enableSorting: false,
        size: 120,
      },
      {
        accessorKey: 'plan_type',
        header: t('Plan'),
        cell: ({ row }) => (
          <StatusBadge
            label={planLabel(row.original.plan_type, t)}
            variant={planVariant(row.original.plan_type)}
            copyable={false}
            className='-ml-1.5'
          />
        ),
        enableSorting: false,
        size: 120,
      },
      {
        accessorKey: 'max_users',
        header: t('Seats'),
        cell: ({ row }) => <Limit value={row.original.max_users} />,
        enableSorting: false,
        size: 90,
      },
      {
        accessorKey: 'max_quota',
        header: t('Monthly quota cap'),
        cell: ({ row }) =>
          row.original.max_quota > 0 ? (
            <span className='tabular-nums'>
              {formatQuota(row.original.max_quota)}
            </span>
          ) : (
            <span className='text-muted-foreground'>{t('Unlimited')}</span>
          ),
        enableSorting: false,
        size: 150,
      },
      {
        id: 'rate_limits',
        header: t('Rate limits'),
        meta: { mobileHidden: true },
        cell: ({ row }) => (
          <div className='flex items-center gap-1.5 text-xs'>
            <span className='text-muted-foreground'>{t('RPM')}</span>
            <Limit value={row.original.rate_limit_rpm} />
            <span className='text-muted-foreground'>·</span>
            <span className='text-muted-foreground'>{t('TPM')}</span>
            <Limit value={row.original.rate_limit_tpm} />
          </div>
        ),
        enableSorting: false,
        size: 190,
      },
      {
        accessorKey: 'created_at',
        header: t('Created'),
        meta: { mobileHidden: true },
        cell: ({ row }) => (
          <div className='min-w-[130px] font-mono text-sm'>
            {formatIsoDate(row.original.created_at)}
          </div>
        ),
        enableSorting: false,
        size: 160,
      },
      {
        id: 'actions',
        header: () => t('Actions'),
        cell: ({ row }) => <TenantRowActions tenant={row.original} />,
        enableHiding: false,
        enableSorting: false,
        meta: { pinned: 'right' as const },
      },
    ],
    [t, formatQuota]
  )
}
