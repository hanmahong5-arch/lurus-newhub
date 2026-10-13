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
import type { OnChangeFn, PaginationState } from '@tanstack/react-table'
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTablePage,
  DataTableRow,
  useDataTable,
} from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { cn } from '@/lib/utils'

import { LOG_TYPE } from '../lib/log-view'
import type { LogFilters, UsageLog } from '../lib/logs'
import { CommonLogsFilterBar } from './common-logs-filter-bar'
import { useLogColumns } from './log-columns'
import { LogMobileList } from './log-mobile-list'

const ROW_TINT: Record<number, string> = {
  [LOG_TYPE.ERROR]: 'bg-rose-50/40 dark:bg-rose-950/20',
  [LOG_TYPE.REFUND]: 'bg-blue-50/30 dark:bg-blue-950/15',
}

interface UsageLogsTableProps {
  rows: UsageLog[]
  total: number
  isLoading: boolean
  isFetching: boolean
  /** A failed read: shown in place of the rows, never as an empty list. */
  error: Error | null
  onRetry: () => void
  pagination: PaginationState
  onPaginationChange: OnChangeFn<PaginationState>
  isStaff: boolean
  tenantWide: boolean
  onOpen: (row: UsageLog) => void
  draft: LogFilters
  applied: LogFilters
  onFilterChange: (patch: Partial<LogFilters>) => void
  onSearch: () => void
  onReset: () => void
  /** Rendered next to Reset / Search (the export button). */
  toolbarExtra?: React.ReactNode
}

/**
 * The usage table: filter card, desktop table with column visibility, phone
 * cards, pagination in the page footer. Paging and filtering are the
 * server's (`manual*`); this component never filters rows itself.
 */
export function UsageLogsTable(props: UsageLogsTableProps) {
  const { t } = useTranslation()
  const columns = useLogColumns({
    isStaff: props.isStaff,
    tenantWide: props.tenantWide,
    onOpen: props.onOpen,
  })

  const { table } = useDataTable<UsageLog>({
    data: props.rows,
    columns,
    getRowId: (r) => String(r.id),
    pagination: props.pagination,
    onPaginationChange: props.onPaginationChange,
    manualPagination: true,
    manualFiltering: true,
    totalCount: props.total,
    enableRowSelection: false,
    enableSorting: false,
    columnVisibilityStorageKey: `usage-logs:columns:${props.isStaff ? 'staff' : 'user'}:${props.tenantWide ? 'all' : 'mine'}`,
  })

  const columnClassName = useCallback(() => 'py-2', [])

  const toolbar = (
    <CommonLogsFilterBar
      table={table}
      draft={props.draft}
      applied={props.applied}
      onChange={props.onFilterChange}
      onSearch={props.onSearch}
      onReset={props.onReset}
      searching={props.isFetching}
      actionStart={props.toolbarExtra}
    />
  )

  if (props.error) {
    return (
      <div className='space-y-3'>
        {toolbar}
        <ErrorState
          title={t('Failed to load logs')}
          description={props.error.message}
          onRetry={props.onRetry}
        />
      </div>
    )
  }

  return (
    <DataTablePage
      table={table}
      columns={columns}
      fixedHeight={false}
      isLoading={props.isLoading}
      isFetching={props.isFetching}
      emptyTitle={t('No logs in this window')}
      emptyDescription={t('Logs appear here once API calls are made.')}
      skeletonKeyPrefix='usage-log-skeleton'
      applyHeaderSize
      getColumnClassName={columnClassName}
      tableClassName='[&_[data-slot=table]]:text-[13px] [&_[data-slot=table]_td]:text-[13px] [&_[data-slot=table]_td_*]:text-[13px] [&_[data-slot=table]_th]:text-[13px] [&_[data-slot=table]_th_*]:text-[13px]'
      toolbar={toolbar}
      mobile={
        <LogMobileList
          table={table}
          isLoading={props.isLoading}
          isStaff={props.isStaff}
          onOpen={props.onOpen}
        />
      }
      renderRow={(row) => (
        <DataTableRow
          key={row.id}
          row={row}
          data-testid='log-row'
          className={cn(
            'cursor-pointer transition-colors',
            ROW_TINT[row.original.type]
          )}
          getColumnClassName={columnClassName}
          cellRenderColumns={table.options.columns}
          onClick={() => props.onOpen(row.original)}
        />
      )}
    />
  )
}
