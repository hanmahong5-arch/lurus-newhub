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
import { Checkbox } from '@/components/ui/checkbox'
import type { MoneyConfig } from '@/lib/money'

import { tokenState } from '../lib/map'
import { STATE_VARIANT, useStateLabels } from '../lib/state'
import type { ApiToken } from '../types'
import {
  ActivityCell,
  ExpiryCell,
  IpRestrictionsCell,
  KeyCell,
  ModelLimitsCell,
  QuotaCell,
  StatusToggle,
} from './key-cells'
import { KeyRowActions, type KeyRowHandlers } from './key-row-actions'

export interface KeyColumnsOptions {
  money: MoneyConfig | null
  nowMs: number
  /** Plaintext keys known in this session (create / rotate), by token id. */
  secrets: Record<number, string>
  busyId: number | null
  /** project id -> name */
  projectNames: Map<number, string>
  onToggleStatus: (token: ApiToken, enable: boolean) => void
  handlers: KeyRowHandlers
}

export function useKeyColumns(opts: KeyColumnsOptions): ColumnDef<ApiToken>[] {
  const { t } = useTranslation()
  const labels = useStateLabels()
  const {
    money,
    nowMs,
    secrets,
    busyId,
    projectNames,
    onToggleStatus,
    handlers,
  } = opts
  const nowSec = Math.floor(nowMs / 1000)

  return useMemo<ColumnDef<ApiToken>[]>(
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
            aria-label={t('Select all on this page')}
            className='translate-y-[2px]'
          />
        ),
        cell: ({ row }) => (
          <Checkbox
            checked={row.getIsSelected()}
            onCheckedChange={(value) => row.toggleSelected(!!value)}
            aria-label={t('Select {{name}}', { name: row.original.name })}
            className='translate-y-[2px]'
          />
        ),
        enableHiding: false,
        size: 40,
      },
      {
        accessorKey: 'name',
        header: t('Name'),
        cell: ({ row }) => (
          <span className='block max-w-56 truncate font-medium'>
            {row.original.name}
          </span>
        ),
        size: 180,
        meta: { mobileTitle: true },
      },
      {
        id: 'status',
        accessorFn: (token) => tokenState(token, nowSec),
        header: t('Status'),
        cell: ({ row }) => {
          const token = row.original
          const state = tokenState(token, nowSec)
          return (
            <div className='flex items-center gap-2'>
              <StatusToggle
                token={token}
                busy={busyId === token.id}
                onToggle={onToggleStatus}
              />
              <StatusBadge
                label={labels[state]}
                variant={STATE_VARIANT[state]}
                copyable={false}
              />
            </div>
          )
        },
        filterFn: (row, id, value: string[]) =>
          value.includes(String(row.getValue(id))),
        size: 190,
        meta: { mobileBadge: true },
      },
      {
        id: 'key',
        accessorFn: (token) => token.key,
        header: t('Key'),
        cell: ({ row }) => (
          <KeyCell token={row.original} secret={secrets[row.original.id]} />
        ),
        size: 200,
      },
      {
        id: 'quota',
        accessorFn: (token) => token.remain_quota,
        header: t('Quota'),
        cell: ({ row }) => <QuotaCell token={row.original} money={money} />,
        size: 200,
        minSize: 180,
      },
      {
        id: 'project',
        accessorFn: (token) => token.project_id,
        header: t('Project'),
        cell: ({ row }) => {
          const id = row.original.project_id
          if (!(id > 0)) {
            return <span className='text-muted-foreground'>-</span>
          }
          return (
            <span className='block max-w-40 truncate text-sm'>
              {projectNames.get(id) ?? `#${id}`}
            </span>
          )
        },
        size: 150,
      },
      {
        id: 'model_limits',
        accessorFn: (token) => token.model_limits,
        header: t('Models'),
        cell: ({ row }) => <ModelLimitsCell token={row.original} />,
        size: 130,
      },
      {
        id: 'allow_ips',
        accessorFn: (token) => token.allow_ips,
        header: t('Allowed IPs'),
        cell: ({ row }) => <IpRestrictionsCell token={row.original} />,
        size: 130,
      },
      {
        id: 'activity',
        accessorFn: (token) => token.created_time,
        header: t('Activity'),
        cell: ({ row }) => <ActivityCell token={row.original} nowMs={nowMs} />,
        size: 220,
      },
      {
        id: 'expired_time',
        accessorFn: (token) => token.expired_time,
        header: t('Expires'),
        cell: ({ row }) => <ExpiryCell token={row.original} nowMs={nowMs} />,
        size: 150,
      },
      {
        id: 'actions',
        header: () => t('Actions'),
        cell: ({ row }) => (
          <KeyRowActions token={row.original} handlers={handlers} />
        ),
        enableHiding: false,
        size: 100,
      },
    ],
    [
      t,
      labels,
      money,
      nowMs,
      nowSec,
      secrets,
      busyId,
      projectNames,
      onToggleStatus,
      handlers,
    ]
  )
}
