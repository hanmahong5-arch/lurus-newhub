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
import type { ColumnDef, Row } from '@tanstack/react-table';
import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';

import {
  DataTablePagination,
  DataTableRow,
  DataTableView,
  DataTableViewOptions,
  useDataTable,
} from '@/components/data-table';
import { Badge } from '@/components/ui/badge';

import {
  capabilityLabelKey,
  formatCompact,
  hasPrice,
  perUnit,
  type ModelEntry,
} from '../lib/catalog';
import { formatLatency } from '../lib/perf';
import { SuccessRate } from './perf-badge';
import { perfWindowLabelKey, usePerfWindow } from './perf-window';
import {
  BillingModeBadge,
  CallableBadge,
  ModalityLabel,
  OutputPriceValue,
  PriceValue,
  SearchUnitPriceValue,
} from './price';
import { usePriceFormatter } from './price-format';
import { usePriceUnit } from './price-unit';
import { VendorIcon } from './vendor-icon';

const PAGE_SIZE = 20;

const HIDDEN_BY_DEFAULT = {
  cache: false,
  groups: false,
  p95: false,
};

function useModelColumns(): ColumnDef<ModelEntry>[] {
  const { t } = useTranslation();
  const fmt = usePriceFormatter();
  const unit = usePriceUnit();
  const windowLabel = t(perfWindowLabelKey(usePerfWindow()));
  const unitLabel = unit === 'K' ? '1K' : '1M';

  return useMemo<ColumnDef<ModelEntry>[]>(
    () => [
      {
        id: 'model',
        accessorFn: (e) => e.id,
        header: t('Model'),
        meta: { label: t('Model') },
        enableHiding: false,
        enableSorting: false,
        minSize: 220,
        cell: ({ row: { original: e } }) => (
          <span className='inline-flex min-w-0 items-center gap-2'>
            <VendorIcon vendor={e.vendor} icon={e.icon} size={20} />
            <span className='truncate font-mono text-sm font-medium'>
              {e.id}
            </span>
          </span>
        ),
      },
      {
        id: 'type',
        accessorFn: (e) => e.usageUnit,
        header: t('Billing unit'),
        meta: { label: t('Billing unit') },
        enableSorting: false,
        cell: ({ row: { original: e } }) => (
          <span data-testid={`model-unit-${e.id}`}>
            <BillingModeBadge entry={e} />
          </span>
        ),
      },
      {
        id: 'input',
        accessorFn: (e) => e.inputPerM,
        header: t('Input / {{unit}}', { unit: unitLabel }),
        meta: { label: t('Input') },
        enableSorting: false,
        cell: ({ row: { original: e } }) => {
          if (e.usageUnit === 'search_unit') {
            return <SearchUnitPriceValue value={e.searchUnitPrice} />;
          }
          if (e.quotaType === 1) {
            return hasPrice(e) ? (
              `${fmt(e.perCall)} ${t('per call')}`
            ) : (
              <PriceValue value={e.perCall} />
            );
          }
          return <PriceValue value={perUnit(e.inputPerM, unit)} />;
        },
      },
      {
        id: 'output',
        accessorFn: (e) => e.outputPerM,
        header: t('Output / {{unit}}', { unit: unitLabel }),
        meta: { label: t('Output') },
        enableSorting: false,
        cell: ({ row: { original: e } }) =>
          e.quotaType === 1 || e.usageUnit === 'search_unit' ? (
            '--'
          ) : (
            <OutputPriceValue entry={e} />
          ),
      },
      {
        id: 'cache',
        accessorFn: (e) => e.cacheReadPerM,
        header: t('Cache read / {{unit}}', { unit: unitLabel }),
        meta: { label: t('Cached') },
        enableSorting: false,
        cell: ({ row: { original: e } }) => (
          <span className='text-muted-foreground'>
            {fmt(perUnit(e.cacheReadPerM, unit)) ?? '--'}
          </span>
        ),
      },
      {
        id: 'vendor',
        accessorFn: (e) => e.vendor,
        header: t('Vendor'),
        meta: { label: t('Vendor') },
        enableSorting: false,
        cell: ({ row: { original: e } }) => (
          <span className='text-muted-foreground'>{e.vendor || '--'}</span>
        ),
      },
      {
        id: 'modality',
        accessorFn: (e) => e.modality,
        header: t('Modality'),
        meta: { label: t('Modality') },
        enableSorting: false,
        cell: ({ row: { original: e } }) => (
          <span data-testid={`model-modality-${e.id}`}>
            <ModalityLabel modality={e.modality} />
          </span>
        ),
      },
      {
        id: 'endpoints',
        accessorFn: (e) => e.capabilities.join(','),
        header: t('Endpoints'),
        meta: { label: t('Endpoints') },
        enableSorting: false,
        cell: ({ row: { original: e } }) =>
          e.capabilities.length === 0 ? (
            '--'
          ) : (
            <span className='flex flex-wrap gap-1'>
              {e.capabilities.slice(0, 2).map((c) => (
                <Badge key={c} variant='outline'>
                  {t(capabilityLabelKey(c))}
                </Badge>
              ))}
              {e.capabilities.length > 2 && (
                <Badge variant='outline'>+{e.capabilities.length - 2}</Badge>
              )}
            </span>
          ),
      },
      {
        id: 'groups',
        accessorFn: (e) => e.groups.join(','),
        header: t('Groups'),
        meta: { label: t('Groups') },
        enableSorting: false,
        cell: ({ row: { original: e } }) =>
          e.groups.length === 0 ? (
            '--'
          ) : (
            <span className='flex flex-wrap gap-1'>
              {e.groups.slice(0, 2).map((g) => (
                <Badge key={g} variant='outline'>
                  {g}
                </Badge>
              ))}
              {e.groups.length > 2 && (
                <Badge variant='outline'>+{e.groups.length - 2}</Badge>
              )}
            </span>
          ),
      },
      {
        id: 'tokens',
        accessorFn: (e) => e.tokens,
        header: t('tokens · 7d'),
        meta: { label: t('tokens · 7d') },
        enableSorting: false,
        cell: ({ row: { original: e } }) => (
          <span className='font-mono'>
            {e.tokens === null ? '--' : formatCompact(e.tokens)}
          </span>
        ),
      },
      {
        id: 'success',
        accessorFn: (e) => e.errorRate,
        header: t('Success'),
        meta: { label: `${t('Success')} (${windowLabel})` },
        enableSorting: false,
        cell: ({ row: { original: e } }) => <SuccessRate entry={e} />,
      },
      {
        id: 'p50',
        accessorFn: (e) => e.p50Ms,
        header: 'p50',
        meta: { label: `p50 (${windowLabel})` },
        enableSorting: false,
        cell: ({ row: { original: e } }) => (
          <span className='font-mono'>
            {e.enoughSamples ? formatLatency(e.p50Ms) : '--'}
          </span>
        ),
      },
      {
        id: 'p95',
        accessorFn: (e) => e.p95Ms,
        header: 'p95',
        meta: { label: `p95 (${windowLabel})` },
        enableSorting: false,
        cell: ({ row: { original: e } }) => (
          <span className='font-mono'>
            {e.enoughSamples ? formatLatency(e.p95Ms) : '--'}
          </span>
        ),
      },
      {
        id: 'access',
        accessorFn: (e) => e.routable,
        header: t('Access'),
        meta: { label: t('Access') },
        enableSorting: false,
        cell: ({ row: { original: e } }) => <CallableBadge entry={e} />,
      },
    ],
    [t, fmt, unit, unitLabel, windowLabel],
  );
}

/** Table in the toolbar's sort order, with column visibility and paging. */
export function ModelTable(props: {
  entries: ModelEntry[];
  onOpen: (id: string) => void;
}) {
  const { t } = useTranslation();
  const columns = useModelColumns();
  const { table } = useDataTable<ModelEntry>({
    data: props.entries,
    columns: columns as ColumnDef<ModelEntry, unknown>[],
    getRowId: (e) => e.id,
    initialPagination: { pageIndex: 0, pageSize: PAGE_SIZE },
    initialColumnVisibility: HIDDEN_BY_DEFAULT,
    columnVisibilityStorageKey: 'models-table-columns',
    withFilteredRowModel: false,
    withSortedRowModel: false,
    withFacetedRowModel: false,
  });

  return (
    <div className='space-y-3' data-testid='models-table'>
      <div className='flex justify-end'>
        <DataTableViewOptions table={table} />
      </div>
      <DataTableView
        table={table}
        emptyTitle={t('No model matches these filters')}
        skeletonKeyPrefix='models-skeleton'
        containerClassName='rounded-xl border'
        renderRow={(row: Row<ModelEntry>) => (
          <DataTableRow
            key={row.id}
            row={row}
            data-testid={`model-row-${row.original.id}`}
            tabIndex={0}
            className='hover:bg-muted/30 focus-visible:bg-muted/40 cursor-pointer outline-none'
            onClick={() => props.onOpen(row.original.id)}
            onKeyDown={(ev) => {
              if (ev.key === 'Enter' || ev.key === ' ') {
                ev.preventDefault();
                props.onOpen(row.original.id);
              }
            }}
          />
        )}
      />
      {props.entries.length > 0 && <DataTablePagination table={table} />}
    </div>
  );
}
