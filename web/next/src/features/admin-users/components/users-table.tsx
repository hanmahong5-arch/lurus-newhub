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

import { groupsQueryOptions, listUsers, usersQueryKey } from '../api'
import {
  DEFAULT_PAGE_SIZE,
  USER_STATUS,
  getUserRoleOptions,
  getUserStatusOptions,
  isUserDeleted,
} from '../constants'
import type { AdminUser } from '../types'
import { useUsersColumns } from './users-columns'

const route = getRouteApi('/_authenticated/admin/users/')

function isDisabledRow(user: AdminUser) {
  return isUserDeleted(user) || user.status === USER_STATUS.DISABLED
}

function facetValue(
  filters: { id: string; value: unknown }[],
  id: string
): string[] {
  const value = filters.find((f) => f.id === id)?.value
  return Array.isArray(value) ? (value as string[]) : []
}

/** Status of a row as the status facet names it: deleted wins. */
function rowStatus(user: AdminUser): string {
  return String(isUserDeleted(user) ? USER_STATUS.DELETED : user.status)
}

export function UsersTable() {
  const { t } = useTranslation()
  const columns = useUsersColumns()
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
    columnFilters: [
      { columnId: 'status', searchKey: 'status', type: 'array' },
      { columnId: 'role', searchKey: 'role', type: 'array' },
      { columnId: 'group', searchKey: 'group', type: 'array' },
    ],
  })

  const statusFilter = facetValue(columnFilters, 'status')[0] ?? ''
  const roleFilter = facetValue(columnFilters, 'role')[0] ?? ''
  const groupFilter = facetValue(columnFilters, 'group')[0] ?? ''
  const keyword = globalFilter ?? ''

  const { data: groups } = useQuery(groupsQueryOptions)

  const { data, isLoading, isFetching, isError, error, refetch } = useQuery({
    queryKey: [
      ...usersQueryKey,
      'list',
      pagination.pageIndex + 1,
      pagination.pageSize,
      keyword,
      groupFilter,
    ],
    queryFn: () =>
      listUsers({
        page: pagination.pageIndex + 1,
        pageSize: pagination.pageSize,
        keyword,
        group: groupFilter,
      }),
    placeholderData: keepPreviousData,
  })

  // The server filters on keyword and group only. Status and role narrow the
  // page that was fetched; the total below stays the server's count.
  const rows = useMemo(() => {
    const items = data?.items ?? []
    return items.filter(
      (user) =>
        (statusFilter === '' || rowStatus(user) === statusFilter) &&
        (roleFilter === '' || String(user.role) === roleFilter)
    )
  }, [data, statusFilter, roleFilter])

  const { table } = useDataTable({
    data: rows,
    columns,
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
    columnVisibilityStorageKey: 'admin-users-columns',
    initialColumnVisibility: { tenant_id: false },
  })

  if (isError && !data) {
    return (
      <ErrorState
        title={t('Failed to load users')}
        description={getServerErrorMessage(error)}
        onRetry={() => void refetch()}
      />
    )
  }

  const pageFiltered = statusFilter !== '' || roleFilter !== ''

  const page = (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={isLoading}
      isFetching={isFetching}
      emptyTitle={t('No Users Found')}
      emptyDescription={
        pageFiltered
          ? t('No users on this page match the status or role filter.')
          : t('No users available. Try adjusting your search or filters.')
      }
      skeletonKeyPrefix='users-skeleton'
      applyHeaderSize
      toolbarProps={{
        searchPlaceholder: t('Filter by username, name or email...'),
        searchDebounceMs: 500,
        filters: [
          {
            columnId: 'status',
            title: t('Status'),
            options: getUserStatusOptions(t),
            singleSelect: true,
          },
          {
            columnId: 'role',
            title: t('Role'),
            options: getUserRoleOptions(t),
            singleSelect: true,
          },
          {
            columnId: 'group',
            title: t('User Group'),
            options: (groups ?? []).map((group) => ({
              label: group,
              value: group,
            })),
            singleSelect: true,
          },
        ],
      }}
      getRowClassName={(row, { isMobile: mobile }) => {
        if (!isDisabledRow(row.original)) return undefined
        return mobile ? DISABLED_ROW_MOBILE : DISABLED_ROW_DESKTOP
      }}
    />
  )

  return (
    <div className='flex min-h-0 flex-1 flex-col gap-2'>
      {pageFiltered && (
        <p className='text-muted-foreground px-1 text-xs' role='note'>
          {t('Status and role filters apply to the users on this page only.')}
        </p>
      )}
      {page}
    </div>
  )
}
