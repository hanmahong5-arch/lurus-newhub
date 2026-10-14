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
import type {
  ColumnFiltersState,
  OnChangeFn,
  PaginationState,
  Row,
} from '@tanstack/react-table'
import { Eye, EyeOff, RefreshCw } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DISABLED_ROW_DESKTOP,
  DISABLED_ROW_MOBILE,
  DataTablePage,
  useDataTable,
  useDebouncedColumnFilter,
} from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useMediaQuery } from '@/hooks'
import { toApiError } from '@/lib/api'
import { cn } from '@/lib/utils'

import {
  channelOpsOverviewQueryOptions,
  getChannels,
  getGroups,
  requireServerSuccess,
  searchChannels,
} from '../api'
import {
  CHANNEL_STATUS,
  CHANNEL_STATUS_OPTIONS,
  DEFAULT_PAGE_SIZE,
} from '../constants'
import {
  aggregateChannelsByTag,
  channelsQueryKeys,
  getChannelTableRowId,
  getChannelTypeLabel,
  isTagAggregateRow,
} from '../lib'
import type { Channel, ChannelListItem } from '../types'
import { ChannelCard } from './channel-card'
import { ChannelOpsInfoContext } from './channel-ops-context'
import { ChannelTypeTabs } from './channel-type-tabs'
import { ChannelTypeLogo } from './channel-type-badge'
import { useChannelsColumns } from './channels-columns'
import { useChannels } from './channels-provider'
import { DataTableBulkActions } from './data-table-bulk-actions'

const COLUMN_VISIBILITY_STORAGE_KEY = 'channels:column-visibility'
const COLUMN_SIZING_STORAGE_KEY = 'channels:column-sizing'
const VIEW_MODE_STORAGE_KEY = 'channels:view-mode'
const STATUS_FILTER_STORAGE_KEY = 'channel-status-filter'
const EMPTY_OPS_INFO: ReadonlyMap<number, ChannelListItem> = new Map()

function isDisabledChannelRow(channel: Channel) {
  return (
    !isTagAggregateRow(channel) && channel.status !== CHANNEL_STATUS.ENABLED
  )
}

function readStoredStatusFilter(): ColumnFiltersState {
  try {
    const stored = localStorage.getItem(STATUS_FILTER_STORAGE_KEY)
    return stored === 'enabled' || stored === 'disabled'
      ? [{ id: 'status', value: [stored] }]
      : []
  } catch {
    return []
  }
}

function firstFilterValue(
  filters: ColumnFiltersState,
  id: string
): string | undefined {
  const value = filters.find((f) => f.id === id)?.value
  if (!Array.isArray(value)) return undefined
  const first = String(value[0] ?? '')
  return first === '' || first === 'all' ? undefined : first
}

export function ChannelsTable() {
  const { t } = useTranslation()
  const {
    enableTagMode,
    idSort,
    batchMode,
    sensitiveVisible,
    setSensitiveVisible,
  } = useChannels()
  const isMobile = useMediaQuery('(max-width: 640px)')

  const [globalFilter, setGlobalFilter] = useState('')
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>(
    readStoredStatusFilter
  )
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: DEFAULT_PAGE_SIZE,
  })

  const backToFirstPage = useCallback(() => {
    setPagination((prev) =>
      prev.pageIndex === 0 ? prev : { ...prev, pageIndex: 0 }
    )
  }, [])

  // A different grouping or sort order starts over from the first page.
  useEffect(() => {
    backToFirstPage()
  }, [enableTagMode, idSort, backToFirstPage])

  const handleGlobalFilterChange: OnChangeFn<string> = (updater) => {
    setGlobalFilter((prev) =>
      typeof updater === 'function' ? updater(prev) : updater
    )
    backToFirstPage()
  }

  const handleColumnFiltersChange: OnChangeFn<ColumnFiltersState> = (
    updater
  ) => {
    setColumnFilters((previous) => {
      const next = typeof updater === 'function' ? updater(previous) : updater
      try {
        localStorage.setItem(
          STATUS_FILTER_STORAGE_KEY,
          firstFilterValue(next, 'status') ?? 'all'
        )
      } catch {
        /* storage may be unavailable; the filter still works */
      }
      return next
    })
    backToFirstPage()
  }

  const statusFilter = firstFilterValue(columnFilters, 'status')
  const typeFilter = firstFilterValue(columnFilters, 'type')
  const groupFilter = firstFilterValue(columnFilters, 'group')
  const {
    value: modelFilter,
    inputValue: modelFilterInput,
    onChange: onModelFilterInputChange,
    onCompositionStart: onModelFilterCompositionStart,
    onCompositionEnd: onModelFilterCompositionEnd,
    resetInput: resetModelFilterInput,
  } = useDebouncedColumnFilter({
    columnFilters,
    columnId: 'model',
    onColumnFiltersChange: handleColumnFiltersChange,
  })

  // The search endpoint takes keyword/model; the list endpoint does not.
  const keyword = globalFilter.trim()
  const shouldSearch = Boolean(keyword || modelFilter.trim())

  const { data: groupsData } = useQuery({
    queryKey: [...channelsQueryKeys.all, 'groups'],
    queryFn: async () => requireServerSuccess(await getGroups()),
    staleTime: 60_000,
  })
  const groupOptions = useMemo(
    () => (groupsData?.data ?? []).map((g) => ({ label: g, value: g })),
    [groupsData]
  )

  const requestParams = {
    group: groupFilter,
    status: statusFilter,
    type: typeFilter === undefined ? undefined : Number(typeFilter),
    tag_mode: enableTagMode,
    id_sort: idSort,
    p: pagination.pageIndex + 1,
    page_size: pagination.pageSize,
  }

  const query = useQuery({
    queryKey: channelsQueryKeys.list({
      keyword,
      model: modelFilter,
      ...requestParams,
    }),
    queryFn: async () => {
      if (shouldSearch) {
        return requireServerSuccess(
          await searchChannels({
            keyword,
            model: modelFilter,
            ...requestParams,
          })
        )
      }
      return requireServerSuccess(await getChannels(requestParams))
    },
    placeholderData: (previous) => previous,
  })
  const { data, isLoading, isFetching, refetch } = query

  // Ops fields join on id; the overview refetches whenever the list does.
  const overview = useQuery({
    ...channelOpsOverviewQueryOptions(query.dataUpdatedAt),
    enabled: query.isSuccess,
    placeholderData: keepPreviousData,
  })

  const channels = useMemo(() => {
    const raw = data?.data?.items ?? []
    return enableTagMode && raw.length > 0 ? aggregateChannelsByTag(raw) : raw
  }, [data, enableTagMode])

  const totalCount = data?.data?.total ?? 0
  const typeCounts = data?.data?.type_counts

  const columns = useChannelsColumns({ enableSelection: batchMode })

  const ensurePageInRange = useCallback((pageCount: number) => {
    setPagination((prev) =>
      pageCount > 0 && prev.pageIndex >= pageCount
        ? { ...prev, pageIndex: pageCount - 1 }
        : prev
    )
  }, [])

  const { table } = useDataTable({
    data: channels,
    columns,
    totalCount,
    initialColumnVisibility: {
      models: false,
      tag: false,
      modality: false,
      plan: false,
    },
    columnVisibilityStorageKey: COLUMN_VISIBILITY_STORAGE_KEY,
    columnSizingStorageKey: isMobile ? false : COLUMN_SIZING_STORAGE_KEY,
    columnFilters,
    pagination,
    globalFilter,
    enableRowSelection: batchMode
      ? (row: Row<Channel>) => !isTagAggregateRow(row.original)
      : false,
    onColumnFiltersChange: handleColumnFiltersChange,
    onPaginationChange: setPagination,
    onGlobalFilterChange: handleGlobalFilterChange,
    getRowId: getChannelTableRowId,
    getSubRows: (row: Channel & { children?: Channel[] }) => row.children,
    manualPagination: true,
    manualSorting: true,
    manualFiltering: true,
    withExpandedRowModel: true,
    enableColumnResizing: !isMobile,
    ensurePageInRange,
  })

  useEffect(() => {
    if (!batchMode) table.resetRowSelection()
  }, [batchMode, table])

  // Type tabs: only types that exist, with their counts, plus "All Types".
  const typeFilterOptions = useMemo(() => {
    const counts = typeCounts ?? {}
    const entries = Object.entries(counts)
      .map(([type, count]) => ({
        type: Number(type),
        count: Number(count) || 0,
      }))
      .filter((item) => item.type > 0 && item.count > 0)
      .sort((a, b) =>
        t(getChannelTypeLabel(a.type)).localeCompare(
          t(getChannelTypeLabel(b.type)),
          undefined,
          { sensitivity: 'base' }
        )
      )
    if (typeFilter !== undefined) {
      const selected = Number(typeFilter)
      if (selected > 0 && !entries.some((item) => item.type === selected)) {
        entries.push({ type: selected, count: 0 })
      }
    }
    const totalTypes = entries.reduce((sum, item) => sum + item.count, 0)
    return [
      { label: 'All Types', value: 'all', count: totalTypes },
      ...entries.map((item) => ({
        label: getChannelTypeLabel(item.type),
        value: String(item.type),
        count: item.count,
        iconNode: <ChannelTypeLogo type={item.type} size={16} />,
      })),
    ]
  }, [t, typeCounts, typeFilter])

  const groupFilterOptions = [
    { label: t('All Groups'), value: 'all' },
    ...groupOptions.map((option) => ({
      ...option,
      label: sensitiveVisible ? option.label : '••••',
    })),
  ]

  if (query.isError && data === undefined) {
    // A failed read is an error, never an empty list.
    return (
      <ErrorState
        title={t('Could not load channels')}
        description={toApiError(query.error).message}
        onRetry={() => void refetch()}
      />
    )
  }

  const selectType = (value: string) =>
    handleColumnFiltersChange((previous) => {
      const rest = previous.filter((f) => f.id !== 'type')
      return value === 'all' ? rest : [...rest, { id: 'type', value: [value] }]
    })

  return (
    <ChannelOpsInfoContext.Provider value={overview.data ?? EMPTY_OPS_INFO}>
      <div className='flex h-full min-h-0 flex-col gap-2.5 sm:gap-3'>
        <ChannelTypeTabs
          tabs={typeFilterOptions}
          value={typeFilter ?? 'all'}
          onChange={selectType}
        />
        <DataTablePage
          className='min-h-0 flex-1'
          table={table}
          columns={columns}
          isLoading={isLoading}
          isFetching={isFetching}
          emptyTitle={t('No Channels Found')}
          emptyDescription={t(
            'No channels available. Create your first channel to get started.'
          )}
          skeletonKeyPrefix='channel-skeleton'
          enableCardView
          defaultViewMode='table'
          viewModeStorageKey={VIEW_MODE_STORAGE_KEY}
          renderCard={(row, { isSelected }) => (
            <ChannelCard row={row} isSelected={isSelected} />
          )}
          cardGridClassName='grid grid-cols-1 gap-3 sm:gap-4 lg:grid-cols-3'
          applyHeaderSize
          toolbarProps={{
            collapsibleOnMobile: true,
            searchPlaceholder: t('Filter by name, ID, or key...'),
            searchDebounceMs: 500,
            onReset: () => {
              resetModelFilterInput()
            },
            additionalSearch: (
              <Input
                placeholder={t('Filter by model...')}
                aria-label={t('Filter by model...')}
                value={modelFilterInput}
                onChange={onModelFilterInputChange}
                onCompositionStart={onModelFilterCompositionStart}
                onCompositionEnd={onModelFilterCompositionEnd}
                className='w-full sm:w-[150px] lg:w-[180px]'
              />
            ),
            filters: [
              {
                columnId: 'status',
                title: t('Status'),
                options: [...CHANNEL_STATUS_OPTIONS],
                singleSelect: true,
              },
              {
                columnId: 'type',
                title: t('Type'),
                options: typeFilterOptions,
                singleSelect: true,
              },
              {
                columnId: 'group',
                title: t('Group'),
                options: groupFilterOptions,
                singleSelect: true,
              },
            ],
            preActions: (
              <>
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <Button
                        variant='ghost'
                        size='icon'
                        onClick={() => setSensitiveVisible(!sensitiveVisible)}
                        aria-label={sensitiveVisible ? t('Hide') : t('Show')}
                        className='text-muted-foreground hover:text-foreground size-8'
                      />
                    }
                  >
                    {sensitiveVisible ? <Eye /> : <EyeOff />}
                  </TooltipTrigger>
                  <TooltipContent>
                    {sensitiveVisible ? t('Hide') : t('Show')}
                  </TooltipContent>
                </Tooltip>
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <Button
                        variant='ghost'
                        size='icon'
                        onClick={() => void refetch()}
                        aria-label={t('Refresh')}
                        aria-busy={isFetching}
                        className='text-muted-foreground hover:text-foreground size-8'
                      />
                    }
                  >
                    <RefreshCw className={cn(isFetching && 'animate-spin')} />
                  </TooltipTrigger>
                  <TooltipContent>{t('Refresh')}</TooltipContent>
                </Tooltip>
              </>
            ),
          }}
          getRowClassName={(row, { isMobile: mobile }) => {
            if (!isDisabledChannelRow(row.original)) return undefined
            return mobile ? DISABLED_ROW_MOBILE : DISABLED_ROW_DESKTOP
          }}
          bulkActions={batchMode ? <DataTableBulkActions table={table} /> : null}
        />
      </div>
    </ChannelOpsInfoContext.Provider>
  )
}
