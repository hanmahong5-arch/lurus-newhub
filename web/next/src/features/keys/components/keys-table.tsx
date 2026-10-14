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
import type {
  ColumnFiltersState,
  OnChangeFn,
  PaginationState,
  RowSelectionState,
  Table as TanstackTable,
} from '@tanstack/react-table'
import { Database, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DISABLED_ROW_DESKTOP,
  DISABLED_ROW_MOBILE,
  DataTableBulkActions,
  DataTablePage,
  useDataTable,
} from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import type { MoneyConfig } from '@/lib/money'
import { cn } from '@/lib/utils'

import { tokenState } from '../lib/map'
import { STATE_VARIANT, TOKEN_STATES, useStateLabels } from '../lib/state'
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
import { useKeyColumns } from './key-columns'
import { KeyRowActions, type KeyRowHandlers } from './key-row-actions'

const COLUMN_VISIBILITY_KEY = 'keys:column-visibility'
const INITIAL_VISIBILITY = { allow_ips: false }
const MOBILE_SKELETONS = ['a', 'b', 'c', 'd', 'e']

export interface KeysTableProps {
  tokens: ApiToken[]
  /** Server-side total, for the page count. */
  total: number
  isLoading: boolean
  isFetching: boolean
  money: MoneyConfig | null
  secrets: Record<number, string>
  busyId: number | null
  projectNames: Map<number, string>
  pagination: PaginationState
  onPaginationChange: OnChangeFn<PaginationState>
  ensurePageInRange: (pageCount: number) => void
  rowSelection: RowSelectionState
  onRowSelectionChange: OnChangeFn<RowSelectionState>
  onToggleStatus: (token: ApiToken, enable: boolean) => void
  handlers: KeyRowHandlers
  /** Ids of the selected rows that are currently visible. */
  onBulkDelete: (ids: number[]) => void
}

function isInactive(token: ApiToken, nowSec: number): boolean {
  const state = tokenState(token, nowSec)
  return state !== 'enabled' && state !== 'near-cap'
}

function KeysMobileList(props: {
  table: TanstackTable<ApiToken>
  isLoading: boolean
  nowMs: number
  money: MoneyConfig | null
  secrets: Record<number, string>
  busyId: number | null
  onToggleStatus: (token: ApiToken, enable: boolean) => void
  handlers: KeyRowHandlers
}) {
  const { t } = useTranslation()
  const labels = useStateLabels()
  const rows = props.table.getRowModel().rows
  const nowSec = Math.floor(props.nowMs / 1000)

  if (props.isLoading) {
    return (
      <div className='min-w-0 space-y-3'>
        {MOBILE_SKELETONS.map((id) => (
          <div
            key={id}
            className='border-border/60 bg-card space-y-2 rounded-xl border p-3.5'
          >
            <div className='flex items-center justify-between'>
              <Skeleton className='h-4 w-32' />
              <Skeleton className='h-5 w-16 rounded-md' />
            </div>
            <Skeleton className='h-7 w-44' />
            <Skeleton className='h-3 w-28' />
          </div>
        ))}
      </div>
    )
  }

  if (rows.length === 0) {
    return (
      <div className='rounded-lg border p-8'>
        <Empty className='border-none p-0'>
          <EmptyHeader>
            <EmptyMedia variant='icon'>
              <Database className='size-6' />
            </EmptyMedia>
            <EmptyTitle>{t('No keys match')}</EmptyTitle>
            <EmptyDescription>
              {t('Try a different search or clear the status filter.')}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      </div>
    )
  }

  return (
    <div className='min-w-0 space-y-3' data-testid='keys-mobile-list'>
      {rows.map((row) => {
        const token = row.original
        const state = tokenState(token, nowSec)
        return (
          <div
            key={row.id}
            data-testid={`key-card-${token.id}`}
            className={cn(
              'border-border/60 bg-card min-w-0 space-y-2 rounded-xl border p-3.5 text-xs leading-4',
              isInactive(token, nowSec) && DISABLED_ROW_MOBILE
            )}
          >
            <div className='flex items-start justify-between gap-3'>
              <div className='flex min-w-0 items-start gap-2'>
                <Checkbox
                  checked={row.getIsSelected()}
                  onCheckedChange={(v) => row.toggleSelected(!!v)}
                  aria-label={t('Select {{name}}', { name: token.name })}
                  className='mt-0.5'
                />
                <div className='min-w-0 text-sm leading-5 font-semibold break-words'>
                  {token.name}
                </div>
              </div>
              <StatusBadge
                label={labels[state]}
                variant={STATE_VARIANT[state]}
                copyable={false}
                className='shrink-0'
              />
            </div>

            <div className='flex min-w-0 items-center justify-between gap-2'>
              <div className='min-w-0 flex-1'>
                <KeyCell token={token} secret={props.secrets[token.id]} />
              </div>
              <div className='flex shrink-0 items-center gap-1'>
                <StatusToggle
                  token={token}
                  busy={props.busyId === token.id}
                  onToggle={props.onToggleStatus}
                />
                <KeyRowActions token={token} handlers={props.handlers} />
              </div>
            </div>

            <div className='min-w-0 py-1'>
              <QuotaCell token={token} money={props.money} variant='card' />
            </div>

            <div className='flex flex-wrap items-center gap-x-5 gap-y-1'>
              <ModelLimitsCell token={token} inline />
              <IpRestrictionsCell token={token} inline />
            </div>

            <div className='grid grid-cols-3 items-start gap-3 border-t pt-2'>
              <div className='col-span-2 min-w-0'>
                <ActivityCell token={token} nowMs={props.nowMs} layout='columns' />
              </div>
              <div className='min-w-0 space-y-1'>
                <div className='text-muted-foreground'>{t('Expires')}</div>
                <ExpiryCell token={token} nowMs={props.nowMs} />
              </div>
            </div>
          </div>
        )
      })}
    </div>
  )
}

function BulkBar(props: {
  table: TanstackTable<ApiToken>
  onBulkDelete: (ids: number[]) => void
}) {
  const { t } = useTranslation()
  return (
    <DataTableBulkActions table={props.table} entityName={t('API key')}>
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='destructive'
              size='icon'
              className='size-8'
              aria-label={t('Delete selected keys')}
              onClick={() =>
                props.onBulkDelete(
                  props.table
                    .getFilteredSelectedRowModel()
                    .rows.map((r) => r.original.id)
                )
              }
            />
          }
        >
          <Trash2 />
        </TooltipTrigger>
        <TooltipContent>
          <p>{t('Delete selected keys')}</p>
        </TooltipContent>
      </Tooltip>
    </DataTableBulkActions>
  )
}

export function KeysTable(props: KeysTableProps) {
  const { t } = useTranslation()
  const labels = useStateLabels()
  const [nowMs, setNowMs] = useState(() => Date.now())
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([])

  useEffect(() => {
    const id = window.setInterval(() => setNowMs(Date.now()), 30_000)
    return () => window.clearInterval(id)
  }, [])

  const columns = useKeyColumns({
    money: props.money,
    nowMs,
    secrets: props.secrets,
    busyId: props.busyId,
    projectNames: props.projectNames,
    onToggleStatus: props.onToggleStatus,
    handlers: props.handlers,
  })

  const { table } = useDataTable({
    data: props.tokens,
    columns,
    getRowId: (token) => String(token.id),
    enableRowSelection: true,
    rowSelection: props.rowSelection,
    onRowSelectionChange: props.onRowSelectionChange,
    columnFilters,
    onColumnFiltersChange: setColumnFilters,
    globalFilter: '',
    columnVisibilityStorageKey: COLUMN_VISIBILITY_KEY,
    initialColumnVisibility: INITIAL_VISIBILITY,
    // The server pages the list; search and status narrow the loaded page.
    manualPagination: true,
    pagination: props.pagination,
    onPaginationChange: props.onPaginationChange,
    totalCount: props.total,
    ensurePageInRange: props.ensurePageInRange,
  })

  const nowSec = Math.floor(nowMs / 1000)
  const filtered = columnFilters.length > 0

  return (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={props.isLoading}
      isFetching={props.isFetching}
      emptyTitle={t('No keys match')}
      emptyDescription={t(
        'Try a different search or clear the status filter.'
      )}
      skeletonKeyPrefix='keys-skeleton'
      applyHeaderSize
      pinnedColumns={[{ columnId: 'actions', side: 'right' }]}
      toolbarProps={{
        searchKey: 'name',
        searchPlaceholder: t('Filter by name...'),
        filters: [
          {
            columnId: 'status',
            title: t('Status'),
            singleSelect: true,
            options: TOKEN_STATES.map((state) => ({
              value: state,
              label: labels[state],
            })),
          },
        ],
      }}
      mobile={
        <KeysMobileList
          table={table}
          isLoading={props.isLoading}
          nowMs={nowMs}
          money={props.money}
          secrets={props.secrets}
          busyId={props.busyId}
          onToggleStatus={props.onToggleStatus}
          handlers={props.handlers}
        />
      }
      getRowClassName={(row) =>
        isInactive(row.original, nowSec) ? DISABLED_ROW_DESKTOP : undefined
      }
      afterTable={
        filtered ? (
          <p className='text-muted-foreground text-xs'>
            {t('Search and status filters apply to the keys on this page.')}
          </p>
        ) : null
      }
      bulkActions={
        <BulkBar table={table} onBulkDelete={props.onBulkDelete} />
      }
    />
  )
}
