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
import { Link } from '@tanstack/react-router'
import type { ColumnDef, PaginationState, Row } from '@tanstack/react-table'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  BadgeCell,
  BadgeListCell,
  DataTablePagination,
  DataTableRow,
  DataTableView,
  DataTableViewOptions,
  useDataTable,
} from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import { StatusBadge } from '@/components/status-badge'

import type { ModelHealth } from '../../api'
import { PAGE_SIZE, type PricingSearch, type TokenUnit } from '../constants'
import {
  groupMultiplier,
  pricingTypeOf,
  priceView,
  type PublicModel,
} from '../lib/model'
import {
  endpointLabel,
  modalityLabel,
  unitLabel,
  useAmountFormatter,
} from '../lib/labels'
import { HealthBadge, ModelAvatar, TypeBadge } from './price-cells'

interface PricingTableProps {
  models: PublicModel[]
  unit: TokenUnit
  search: PricingSearch
  health: Map<string, ModelHealth>
  onOpen: (model: PublicModel) => void
}

function Dash() {
  return <span className='text-muted-foreground/60'>--</span>
}

/** Input, output or cached price of one row, in the table's current unit. */
function PriceCell(props: {
  model: PublicModel
  column: 'input' | 'output' | 'cached'
  unit: TokenUnit
  group: string | undefined
}) {
  const { t } = useTranslation()
  const fmt = useAmountFormatter()
  const view = priceView(
    props.model,
    groupMultiplier(props.model, props.group),
    props.unit
  )
  const mono = 'font-mono text-sm tabular-nums'

  if (view.kind === 'unpriced') {
    return props.column === 'input' ? (
      <span className='text-muted-foreground text-sm'>{t('Unpriced')}</span>
    ) : (
      <Dash />
    )
  }
  if (view.kind === 'unavailable') {
    return props.column === 'input' ? (
      <span className='text-muted-foreground text-sm'>
        {t('Price unavailable')}
      </span>
    ) : (
      <Dash />
    )
  }
  if (view.kind === 'call' || view.kind === 'search') {
    if (props.column !== 'input') return <Dash />
    const text = fmt(view.price)
    const suffix = view.kind === 'call' ? t('request') : t('search unit')
    return text ? (
      <span className={mono}>
        {text}
        <span className='text-muted-foreground text-xs'> / {suffix}</span>
      </span>
    ) : (
      <span className='text-muted-foreground text-sm'>{t('Not configured')}</span>
    )
  }
  if (props.column === 'output' && !view.outputBilled) {
    return <span className='text-muted-foreground text-sm'>{t('Not billed')}</span>
  }
  const values = {
    input: view.input,
    output: view.output,
    cached: view.cache,
  }
  const text = fmt(values[props.column])
  if (!text) return <Dash />
  return <span className={mono}>{text}</span>
}

function useColumns(props: PricingTableProps): ColumnDef<PublicModel>[] {
  const { t } = useTranslation()
  const { unit, search, health } = props
  const hasCache = props.models.some((m) => m.entry.cacheReadPerM !== null)
  const hasTokenPrices = props.models.some(
    (m) => pricingTypeOf(m.entry) === 'token'
  )
  const per = unitLabel(unit)

  return useMemo<ColumnDef<PublicModel>[]>(() => {
    const columns: ColumnDef<PublicModel>[] = [
      {
        id: 'model',
        accessorFn: (m) => m.id,
        meta: { label: t('Model') },
        header: t('Model'),
        minSize: 220,
        enableHiding: false,
        cell: ({ row }) => (
          <div className='flex max-w-full min-w-0 items-center gap-2'>
            <ModelAvatar model={row.original} size={16} />
            <Link
              to='/pricing/$modelId'
              params={{ modelId: row.original.id }}
              search={search}
              onClick={(e) => e.stopPropagation()}
              className='truncate font-mono text-sm font-medium underline-offset-4 hover:underline'
            >
              {row.original.id}
            </Link>
          </div>
        ),
      },
      {
        id: 'type',
        accessorFn: (m) => pricingTypeOf(m.entry),
        meta: { label: t('Type') },
        header: t('Type'),
        size: 120,
        cell: ({ row }) => <TypeBadge model={row.original} />,
      },
      {
        id: 'input',
        accessorFn: (m) => m.entry.inputPerM,
        meta: { label: t('Input') },
        header: hasTokenPrices ? `${t('Input')} / ${per}` : t('Price'),
        size: 170,
        cell: ({ row }) => (
          <PriceCell
            model={row.original}
            column='input'
            unit={unit}
            group={search.group}
          />
        ),
      },
      {
        id: 'output',
        accessorFn: (m) => m.entry.outputPerM,
        meta: { label: t('Output') },
        header: `${t('Output')} / ${per}`,
        size: 130,
        cell: ({ row }) => (
          <PriceCell
            model={row.original}
            column='output'
            unit={unit}
            group={search.group}
          />
        ),
      },
    ]
    if (hasCache) {
      columns.push({
        id: 'cached',
        accessorFn: (m) => m.entry.cacheReadPerM,
        meta: { label: t('Cached') },
        header: `${t('Cached')} / ${per}`,
        size: 130,
        cell: ({ row }) => (
          <PriceCell
            model={row.original}
            column='cached'
            unit={unit}
            group={search.group}
          />
        ),
      })
    }
    columns.push(
      {
        id: 'vendor',
        accessorFn: (m) => m.vendor,
        meta: { label: t('Vendor') },
        header: t('Vendor'),
        size: 130,
        cell: ({ row }) =>
          row.original.vendor ? (
            <BadgeCell className='gap-1.5'>
              <StatusBadge
                label={row.original.vendor}
                autoColor={row.original.vendor}
                size='sm'
                copyable={false}
              />
            </BadgeCell>
          ) : (
            <Dash />
          ),
      },
      {
        id: 'modality',
        accessorFn: (m) => m.modality,
        meta: { label: t('Modality') },
        header: t('Modality'),
        size: 120,
        cell: ({ row }) =>
          row.original.modality ? (
            <span className='text-sm'>
              {modalityLabel(t, row.original.modality)}
            </span>
          ) : (
            <Dash />
          ),
      },
      {
        id: 'endpoints',
        accessorFn: (m) => m.endpoints.join(','),
        meta: { label: t('Endpoints') },
        header: t('Endpoints'),
        size: 160,
        cell: ({ row }) => (
          <BadgeListCell
            items={row.original.endpoints.map((e) => (
              <StatusBadge
                key={e}
                label={endpointLabel(t, e)}
                autoColor={e}
                size='sm'
                copyable={false}
              />
            ))}
          />
        ),
      },
      {
        id: 'groups',
        accessorFn: (m) => m.groups.join(','),
        meta: { label: t('Groups') },
        header: t('Groups'),
        size: 130,
        cell: ({ row }) => (
          <BadgeListCell
            items={row.original.groups.map((g) => (
              <GroupBadge key={g} group={g} size='sm' />
            ))}
          />
        ),
      },
      {
        id: 'tags',
        accessorFn: (m) => m.tags.join(','),
        meta: { label: t('Tags') },
        header: t('Tags'),
        size: 140,
        cell: ({ row }) => (
          <BadgeListCell
            items={row.original.tags.map((tag) => (
              <StatusBadge
                key={tag}
                label={tag}
                autoColor={tag}
                size='sm'
                copyable={false}
              />
            ))}
          />
        ),
      }
    )
    if (health.size > 0) {
      columns.push({
        id: 'status',
        accessorFn: (m) => health.get(m.id) ?? '',
        meta: { label: t('Status') },
        header: t('Status'),
        size: 120,
        cell: ({ row }) => (
          <HealthBadge health={health.get(row.original.id)} />
        ),
      })
    }
    return columns
  }, [t, unit, search, health, hasCache, hasTokenPrices, per])
}

/** Table view: sortable-looking columns, column picker and paging. */
export function PricingTable(props: PricingTableProps) {
  const { t } = useTranslation()
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: PAGE_SIZE,
  })
  const columns = useColumns(props)

  const { table } = useDataTable({
    data: props.models,
    columns,
    pagination,
    onPaginationChange: setPagination,
    columnVisibilityStorageKey: 'public-pricing-columns',
    // Ordering is the toolbar's job and the rows arrive filtered.
    withFilteredRowModel: false,
    withSortedRowModel: false,
    withFacetedRowModel: false,
    enableSorting: false,
    // A filter change must not strand the person on a page that no longer
    // exists; the table clamps it.
    autoResetPageIndex: true,
  })

  return (
    <div className='space-y-3'>
      <div className='flex justify-end'>
        <DataTableViewOptions table={table} />
      </div>
      <DataTableView
        table={table}
        emptyTitle={t('No Models Found')}
        emptyDescription={t('No models match your current filters.')}
        skeletonKeyPrefix='public-pricing'
        applyHeaderSize
        getColumnClassName={(_id, kind) =>
          kind === 'header' ? 'text-muted-foreground font-medium' : undefined
        }
        renderRow={(row: Row<PublicModel>) => (
          <DataTableRow
            key={row.id}
            row={row}
            className='hover:bg-muted/30 cursor-pointer transition-colors'
            onClick={() => props.onOpen(row.original)}
          />
        )}
      />
      {props.models.length > 0 && <DataTablePagination table={table} />}
    </div>
  )
}
