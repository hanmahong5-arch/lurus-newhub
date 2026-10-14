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
  ColumnDef,
  OnChangeFn,
  PaginationState,
  Table,
} from '@tanstack/react-table'
import { useCallback, type ReactNode } from 'react'

import {
  DataTablePage,
  DataTableRow,
  useDataTable,
} from '@/components/data-table'
import { ErrorState } from '@/components/error-state'

interface JobsTableProps<T> {
  rows: T[]
  total: number
  columns: ColumnDef<T>[]
  getRowId: (row: T) => string
  isLoading: boolean
  isFetching: boolean
  error: Error | null
  onRetry: () => void
  pagination: PaginationState
  onPaginationChange: OnChangeFn<PaginationState>
  columnVisibilityStorageKey: string
  emptyTitle: string
  emptyDescription: string
  errorTitle: string
  rowTestId: string
  onOpen: (row: T) => void
  /** The filter card; gets the table for its column-visibility menu. */
  renderToolbar: (table: Table<T>) => ReactNode
}

/**
 * A server-paged list of jobs: filter card on top, table on desktop, cards
 * on a phone, pagination in the page footer. A failed read replaces the rows
 * with an error; it is never shown as an empty list.
 */
export function JobsTable<T>(props: JobsTableProps<T>) {
  const { table } = useDataTable<T>({
    data: props.rows,
    columns: props.columns,
    getRowId: props.getRowId,
    pagination: props.pagination,
    onPaginationChange: props.onPaginationChange,
    manualPagination: true,
    manualFiltering: true,
    totalCount: props.total,
    enableRowSelection: false,
    enableSorting: false,
    columnVisibilityStorageKey: props.columnVisibilityStorageKey,
  })
  const columnClassName = useCallback(() => 'py-2.5', [])
  const toolbar = props.renderToolbar(table)

  if (props.error) {
    return (
      <div className='space-y-3'>
        {toolbar}
        <ErrorState
          title={props.errorTitle}
          description={props.error.message}
          onRetry={props.onRetry}
        />
      </div>
    )
  }

  return (
    <DataTablePage
      table={table}
      columns={props.columns}
      fixedHeight={false}
      isLoading={props.isLoading}
      isFetching={props.isFetching}
      emptyTitle={props.emptyTitle}
      emptyDescription={props.emptyDescription}
      skeletonKeyPrefix={props.rowTestId}
      applyHeaderSize
      getColumnClassName={columnClassName}
      toolbar={toolbar}
      renderRow={(row) => (
        <DataTableRow
          key={row.id}
          row={row}
          data-testid={props.rowTestId}
          className='cursor-pointer'
          getColumnClassName={columnClassName}
          cellRenderColumns={table.options.columns}
          onClick={() => props.onOpen(row.original)}
        />
      )}
    />
  )
}
