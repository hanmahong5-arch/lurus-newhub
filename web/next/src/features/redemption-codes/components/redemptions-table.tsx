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
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DISABLED_ROW_DESKTOP,
  DISABLED_ROW_MOBILE,
  DataTablePage,
  useDataTable,
} from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { useMediaQuery } from '@/hooks'
import { useTableUrlState, type NavigateFn } from '@/hooks/use-table-url-state'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { listRedemptions, redemptionsQueryKey } from '../api'
import {
  DEFAULT_PAGE_SIZE,
  REDEMPTION_FILTER_EXPIRED,
  REDEMPTION_STATUS,
  getRedemptionStatusOptions,
} from '../constants'
import { isRedemptionExpired } from '../lib/utils'
import type { Redemption } from '../types'
import { DataTableBulkActions } from './data-table-bulk-actions'
import { useRedemptionsColumns } from './redemptions-columns'
import { RedemptionsMobileList } from './redemptions-mobile-list'

const route = getRouteApi('/_authenticated/admin/redemption-codes/')

function isInactive(redemption: Redemption) {
  return (
    redemption.status !== REDEMPTION_STATUS.ENABLED ||
    isRedemptionExpired(redemption)
  )
}

function matchesStatus(redemption: Redemption, filter: string): boolean {
  if (filter === '') return true
  if (filter === REDEMPTION_FILTER_EXPIRED) {
    return isRedemptionExpired(redemption)
  }
  return String(redemption.status) === filter
}

export function RedemptionsTable() {
  const { t } = useTranslation()
  const columns = useRedemptionsColumns()
  const isMobile = useMediaQuery('(max-width: 640px)')
  const search = route.useSearch() as Record<string, unknown>
  const navigate = route.useNavigate() as unknown as NavigateFn

  const {
    globalFilter,
    onGlobalFilterChange,
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    ensurePageInRange,
  } = useTableUrlState({
    search,
    navigate,
    pagination: {
      defaultPage: 1,
      defaultPageSize: isMobile ? 10 : DEFAULT_PAGE_SIZE,
    },
    globalFilter: { enabled: true, key: 'filter' },
    columnFilters: [{ columnId: 'status', searchKey: 'status', type: 'array' }],
  })

  const statusValue = columnFilters.find((f) => f.id === 'status')?.value
  const statusFilter = Array.isArray(statusValue)
    ? String((statusValue as string[])[0] ?? '')
    : ''
  const keyword = globalFilter ?? ''

  const { data, isLoading, isFetching, isError, error, refetch } = useQuery({
    queryKey: [
      ...redemptionsQueryKey,
      'list',
      pagination.pageIndex + 1,
      pagination.pageSize,
      keyword,
    ],
    queryFn: () =>
      listRedemptions({
        page: pagination.pageIndex + 1,
        pageSize: pagination.pageSize,
        keyword,
      }),
    placeholderData: keepPreviousData,
  })

  // The tenant list filters on name / id only. Status narrows the page that
  // was fetched; the total below stays the server's count.
  const rows = useMemo(
    () =>
      (data?.redemptions ?? []).filter((item) =>
        matchesStatus(item, statusFilter)
      ),
    [data, statusFilter]
  )

  const { table } = useDataTable({
    data: rows,
    columns,
    enableRowSelection: true,
    getRowId: (row) => String(row.id),
    columnFilters,
    globalFilter,
    pagination,
    onPaginationChange,
    onGlobalFilterChange,
    onColumnFiltersChange,
    manualPagination: true,
    manualFiltering: true,
    totalCount: data?.total ?? 0,
    ensurePageInRange,
    columnVisibilityStorageKey: 'redemption-codes-columns',
  })

  if (isError && !data) {
    return (
      <ErrorState
        title={t('Failed to load redemption codes')}
        description={getServerErrorMessage(error)}
        onRetry={() => void refetch()}
      />
    )
  }

  const pageFiltered = statusFilter !== ''

  return (
    <div className='flex min-h-0 flex-1 flex-col gap-2'>
      {pageFiltered && (
        <p className='text-muted-foreground px-1 text-xs' role='note'>
          {t('The status filter applies to the codes on this page only.')}
        </p>
      )}
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={isLoading}
        isFetching={isFetching}
        emptyTitle={t('No Redemption Codes Found')}
        emptyDescription={
          pageFiltered
            ? t('No codes on this page match the status filter.')
            : t(
                'No redemption codes available. Create your first redemption code to get started.'
              )
        }
        skeletonKeyPrefix='redemptions-skeleton'
        applyHeaderSize
        toolbarProps={{
          searchPlaceholder: t('Filter by name or ID...'),
          searchDebounceMs: 500,
          filters: [
            {
              columnId: 'status',
              title: t('Status'),
              options: getRedemptionStatusOptions(t),
              singleSelect: true,
            },
          ],
        }}
        mobile={<RedemptionsMobileList table={table} isLoading={isLoading} />}
        getRowClassName={(row, { isMobile: mobile }) => {
          if (!isInactive(row.original)) return undefined
          return mobile ? DISABLED_ROW_MOBILE : DISABLED_ROW_DESKTOP
        }}
        bulkActions={<DataTableBulkActions table={table} />}
      />
    </div>
  )
}
