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
import { useQuery } from '@tanstack/react-query'
import type { ColumnFiltersState } from '@tanstack/react-table'
import { Plus } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DISABLED_ROW_DESKTOP,
  DISABLED_ROW_MOBILE,
  DataTablePage,
  useDataTable,
} from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { ApiError } from '@/lib/api'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { listTenants, tenantsQueryKey } from '../api'
import { DEFAULT_PAGE_SIZE, TENANT_STATUS } from '../constants'
import { statusLabel } from '../lib/labels'
import type { Tenant } from '../types'
import { tenantMatchesSearch } from '../lib/search'
import { useTenantsColumns } from './tenants-columns'
import { useTenants } from './tenants-provider'

const EMPTY_TENANTS: Tenant[] = []

export function TenantsTable() {
  const { t } = useTranslation()
  const { setDialog } = useTenants()
  const columns = useTenantsColumns()
  const [globalFilter, setGlobalFilter] = useState('')
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([])

  const { data, isLoading, isFetching, isError, error, refetch } = useQuery({
    queryKey: [...tenantsQueryKey, 'list'],
    queryFn: listTenants,
    // The failure is shown in place; no second toast on top of it.
    meta: { errorToast: false },
  })

  const { table } = useDataTable({
    data: data?.tenants ?? EMPTY_TENANTS,
    columns,
    getRowId: (row) => row.id,
    globalFilter,
    onGlobalFilterChange: setGlobalFilter,
    columnFilters,
    onColumnFiltersChange: setColumnFilters,
    globalFilterFn: (row, _columnId, value: string) =>
      tenantMatchesSearch(row.original, value),
    initialPagination: { pageIndex: 0, pageSize: DEFAULT_PAGE_SIZE },
    columnVisibilityStorageKey: 'admin-tenants-columns',
  })

  const statusOptions = useMemo(
    () =>
      [
        TENANT_STATUS.ENABLED,
        TENANT_STATUS.DISABLED,
        TENANT_STATUS.SUSPENDED,
      ].map((status) => ({
        label: statusLabel(status, t),
        value: String(status),
      })),
    [t]
  )

  if (isError && !data) {
    // A failed read is never an empty table: no count, no "create one" prompt.
    if (error instanceof ApiError && error.status === 403) {
      return (
        <ErrorState
          title={t('Administrator access required')}
          description={t(
            'You do not have permission to manage tenants. Contact a platform administrator.'
          )}
        />
      )
    }
    return (
      <ErrorState
        title={t('Failed to load tenants')}
        description={getServerErrorMessage(error)}
        onRetry={() => void refetch()}
      />
    )
  }

  const filtered = globalFilter.trim() !== '' || columnFilters.length > 0
  const loaded = data?.tenants.length ?? 0
  const total = data?.total ?? 0

  return (
    <div className='flex min-h-0 flex-1 flex-col gap-2'>
      {isError && data && (
        <div
          role='alert'
          className='border-destructive/40 bg-destructive/5 text-destructive flex items-center justify-between gap-3 rounded-md border px-3 py-2 text-sm'
        >
          <span>
            {t('Could not refresh tenants. Showing the last list that loaded.')}
          </span>
          <Button size='sm' variant='outline' onClick={() => void refetch()}>
            {t('Retry')}
          </Button>
        </div>
      )}
      {data && (
        <p
          className='text-muted-foreground px-1 text-xs'
          data-testid='tenants-count'
        >
          {t('{{count}} tenants', { count: total })}
          {total > loaded &&
            ` · ${t('Showing the first {{loaded}}', { loaded })}`}
        </p>
      )}
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={isLoading}
        isFetching={isFetching}
        emptyTitle={t('No tenants found')}
        emptyDescription={
          filtered
            ? t('No tenants match your search.')
            : t('No tenants yet. Create one to get started.')
        }
        emptyAction={
          filtered ? undefined : (
            <Button size='sm' onClick={() => setDialog({ kind: 'create' })}>
              <Plus className='h-4 w-4' />
              {t('New tenant')}
            </Button>
          )
        }
        skeletonKeyPrefix='tenants-skeleton'
        applyHeaderSize
        toolbarProps={{
          searchPlaceholder: t('Search by name, slug or ID...'),
          filters: [
            {
              columnId: 'status',
              title: t('Status'),
              options: statusOptions,
              singleSelect: true,
            },
          ],
        }}
        getRowClassName={(row, { isMobile }) => {
          if (row.original.status === TENANT_STATUS.ENABLED) return undefined
          return isMobile ? DISABLED_ROW_MOBILE : DISABLED_ROW_DESKTOP
        }}
      />
    </div>
  )
}
