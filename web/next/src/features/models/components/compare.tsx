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
import { Check, Plus, X } from 'lucide-react';
import {
  useContext,
  type KeyboardEvent,
  type MouseEvent,
  type ReactNode,
} from 'react';
import { useTranslation } from 'react-i18next';

import {
  sideDrawerContentClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { cn } from '@/lib/utils';

import {
  capabilityLabelKey,
  formatCompact,
  hasPrice,
  perUnit,
  type ModelEntry,
} from '../lib/catalog';
import {
  cheapestIndexes,
  MAX_COMPARE,
  rankableInput,
  rankableOutput,
} from '../lib/compare';
import { formatLatency } from '../lib/perf';
import { SuccessRate } from './perf-badge';
import {
  perfWindowLabelKey,
  useNoMeasurement,
  usePerfWindow,
} from './perf-window';
import {
  BillingModeBadge,
  CallableBadge,
  OutputPriceValue,
  PriceValue,
  SearchUnitPriceValue,
} from './price';
import { usePriceFormatter } from './price-format';
import { usePriceUnit } from './price-unit';
import { CompareContext } from './compare-context';
import { VendorIcon } from './vendor-icon';

/** Add/remove button on a card or table row. Never opens the detail drawer. */
export function CompareToggle(props: { id: string; className?: string }) {
  const { t } = useTranslation();
  const state = useContext(CompareContext);
  if (!state) return null;
  const on = state.ids.includes(props.id);
  const full = !on && state.ids.length >= MAX_COMPARE;
  let label = t('Add {{model}} to the comparison', { model: props.id });
  if (on) {
    label = t('Remove {{model}} from the comparison', { model: props.id });
  } else if (full) {
    label = t('You can compare up to {{n}} models', { n: MAX_COMPARE });
  }
  const stop = (ev: MouseEvent | KeyboardEvent) => ev.stopPropagation();
  return (
    <span onClick={stop} onKeyDown={stop} className='inline-flex'>
      <Button
        type='button'
        size='sm'
        variant={on ? 'default' : 'outline'}
        aria-pressed={on}
        aria-label={label}
        title={label}
        disabled={full}
        data-testid={`model-compare-${props.id}`}
        className={cn('gap-1', props.className)}
        onClick={() => state.toggle(props.id)}
      >
        {on ? (
          <Check aria-hidden className='size-3.5' />
        ) : (
          <Plus aria-hidden className='size-3.5' />
        )}
        {on ? t('Comparing') : t('Compare')}
      </Button>
    </span>
  );
}

/** Floating selection summary; absent until something is picked. */
export function CompareBar(props: {
  count: number;
  onOpen: () => void;
  onClear: () => void;
}) {
  const { t } = useTranslation();
  if (props.count === 0) return null;
  return (
    <div
      data-testid='compare-bar'
      role='region'
      aria-label={t('Model comparison')}
      className='bg-card fixed inset-x-3 bottom-4 z-40 mx-auto flex max-w-md items-center gap-3 rounded-xl border px-4 py-2.5 shadow-lg'
    >
      <span className='min-w-0 flex-1 text-sm'>
        {t('{{n}} selected', { n: props.count })}
        <span className='text-muted-foreground ml-2 text-xs'>
          {t('up to {{n}}', { n: MAX_COMPARE })}
        </span>
      </span>
      <Button
        type='button'
        size='sm'
        variant='ghost'
        onClick={props.onClear}
        data-testid='compare-clear'
      >
        {t('Clear')}
      </Button>
      <Button
        type='button'
        size='sm'
        disabled={props.count < 2}
        onClick={props.onOpen}
        data-testid='compare-open'
      >
        {t('Compare')}
      </Button>
    </div>
  );
}

function Cell(props: { best?: boolean; children: ReactNode }) {
  const { t } = useTranslation();
  return (
    <div className='flex flex-wrap items-center gap-1.5'>
      {props.children}
      {props.best && (
        <Badge variant='secondary' data-testid='compare-cheapest'>
          {t('Cheapest')}
        </Badge>
      )}
    </div>
  );
}

function PerfCell(props: {
  entry: ModelEntry;
  kind: 'success' | 'p50' | 'p95';
}) {
  const e = props.entry;
  const reason = useNoMeasurement(e);
  if (reason) {
    return props.kind === 'success' ? (
      <span className='text-muted-foreground font-sans text-xs'>{reason}</span>
    ) : (
      <span className='text-muted-foreground'>--</span>
    );
  }
  if (props.kind === 'success') return <SuccessRate entry={e} />;
  return <span>{formatLatency(props.kind === 'p50' ? e.p50Ms : e.p95Ms)}</span>;
}

function InputCell(props: { entry: ModelEntry }) {
  const { t } = useTranslation();
  const fmt = usePriceFormatter();
  const unit = usePriceUnit();
  const e = props.entry;
  if (e.usageUnit === 'search_unit') {
    return (
      <span>
        <SearchUnitPriceValue value={e.searchUnitPrice} /> / {t('search unit')}
      </span>
    );
  }
  if (e.quotaType === 1) {
    return hasPrice(e) ? (
      <span>
        {fmt(e.perCall)} {t('per call')}
      </span>
    ) : (
      <PriceValue value={e.perCall} />
    );
  }
  return <PriceValue value={perUnit(e.inputPerM, unit)} />;
}

function OutputCell(props: { entry: ModelEntry }) {
  const e = props.entry;
  if (e.quotaType === 1 || e.usageUnit === 'search_unit') {
    return <span className='text-muted-foreground'>--</span>;
  }
  return <OutputPriceValue entry={e} />;
}

interface Row {
  key: string;
  label: string;
  mono?: boolean;
  cell: (e: ModelEntry, i: number) => ReactNode;
}

/** Side-by-side table of the picked models. */
export function CompareSheet(props: {
  /** Picked models in pick order; ids missing from the catalogue are dropped by the caller. */
  entries: ModelEntry[];
  open: boolean;
  onClose: () => void;
  onRemove: (id: string) => void;
  onOpenDetails: (id: string) => void;
  /** Rendered inside the sheet so a dialog opened from it stacks on top. */
  children?: ReactNode;
}) {
  const { t } = useTranslation();
  const fmt = usePriceFormatter();
  const unit = usePriceUnit();
  const windowLabel = t(perfWindowLabelKey(usePerfWindow()));
  const unitLabel = unit === 'K' ? '1K' : '1M';
  // The display unit scales every price alike, so ranking on it is the same
  // as ranking on USD/1M; it is applied only so the numbers match the cells.
  const inBest = cheapestIndexes(
    props.entries.map((e) => perUnit(rankableInput(e), unit)),
  );
  const outBest = cheapestIndexes(
    props.entries.map((e) => perUnit(rankableOutput(e), unit)),
  );

  const rows: Row[] = [
    { key: 'vendor', label: t('Vendor'), cell: (e) => e.vendor || '--' },
    {
      key: 'access',
      label: t('Access'),
      cell: (e) => <CallableBadge entry={e} />,
    },
    {
      key: 'billing',
      label: t('Billing unit'),
      cell: (e) => <BillingModeBadge entry={e} />,
    },
    {
      key: 'input',
      label: t('Input / {{unit}}', { unit: unitLabel }),
      mono: true,
      cell: (e, i) => (
        <Cell best={inBest.has(i)}>
          <InputCell entry={e} />
        </Cell>
      ),
    },
    {
      key: 'output',
      label: t('Output / {{unit}}', { unit: unitLabel }),
      mono: true,
      cell: (e, i) => (
        <Cell best={outBest.has(i)}>
          <OutputCell entry={e} />
        </Cell>
      ),
    },
    {
      key: 'cache',
      label: t('Cache read / {{unit}}', { unit: unitLabel }),
      mono: true,
      cell: (e) => fmt(perUnit(e.cacheReadPerM, unit)) ?? '--',
    },
    {
      key: 'caps',
      label: t('Endpoints'),
      cell: (e) =>
        e.capabilities.length === 0 ? (
          '--'
        ) : (
          <span className='flex flex-wrap gap-1'>
            {e.capabilities.map((c) => (
              <Badge key={c} variant='outline'>
                {t(capabilityLabelKey(c))}
              </Badge>
            ))}
          </span>
        ),
    },
    {
      key: 'success',
      label: `${t('Success')} (${windowLabel})`,
      mono: true,
      cell: (e) => <PerfCell entry={e} kind='success' />,
    },
    {
      key: 'p50',
      label: `p50 (${windowLabel})`,
      mono: true,
      cell: (e) => <PerfCell entry={e} kind='p50' />,
    },
    {
      key: 'p95',
      label: `p95 (${windowLabel})`,
      mono: true,
      cell: (e) => <PerfCell entry={e} kind='p95' />,
    },
    {
      key: 'tokens',
      label: t('tokens · 7d'),
      mono: true,
      cell: (e) => (e.tokens === null ? '--' : formatCompact(e.tokens)),
    },
    {
      key: 'desc',
      label: t('Description'),
      cell: (e) => e.description || '--',
    },
  ];

  return (
    <Sheet
      open={props.open && props.entries.length > 0}
      onOpenChange={(open) => {
        if (!open) props.onClose();
      }}
    >
      <SheetContent
        side='right'
        className={sideDrawerContentClassName('sm:max-w-4xl')}
        data-testid='compare-drawer'
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('Compare models')}</SheetTitle>
          <SheetDescription>
            {t('Side by side: price, access, endpoints and performance.')}
          </SheetDescription>
        </SheetHeader>
        <div className='min-h-0 flex-1 overflow-auto px-4 py-4 sm:px-6'>
          <table
            className='w-full min-w-max text-sm'
            data-testid='compare-table'
          >
            <thead>
              <tr className='border-b'>
                <th className='bg-background sticky left-0 z-10 w-36 min-w-36' />
                {props.entries.map((e) => (
                  <th
                    key={e.id}
                    className='min-w-48 px-3 py-2 text-left align-top font-normal'
                  >
                    <span className='flex items-center gap-2'>
                      <VendorIcon vendor={e.vendor} icon={e.icon} size={20} />
                      <button
                        type='button'
                        title={t('Details')}
                        data-testid={`compare-details-${e.id}`}
                        className='min-w-0 flex-1 truncate text-left font-mono text-sm font-semibold hover:underline'
                        onClick={() => props.onOpenDetails(e.id)}
                      >
                        {e.id}
                      </button>
                      <Button
                        type='button'
                        size='icon-sm'
                        variant='ghost'
                        aria-label={t('Remove {{model}} from the comparison', {
                          model: e.id,
                        })}
                        data-testid={`compare-remove-${e.id}`}
                        onClick={() => props.onRemove(e.id)}
                      >
                        <X aria-hidden className='size-4' />
                      </Button>
                    </span>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr
                  key={row.key}
                  data-testid={`compare-row-${row.key}`}
                  className='border-b last:border-b-0'
                >
                  <th
                    scope='row'
                    className='bg-background text-muted-foreground sticky left-0 z-10 px-0 py-2.5 pr-3 text-left align-top text-xs font-medium'
                  >
                    {row.label}
                  </th>
                  {props.entries.map((e, i) => (
                    <td
                      key={e.id}
                      className={cn(
                        'px-3 py-2.5 align-top',
                        row.mono && 'font-mono tabular-nums',
                        row.key === 'desc' && 'max-w-64 whitespace-normal',
                      )}
                    >
                      {row.cell(e, i)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {props.children}
      </SheetContent>
    </Sheet>
  );
}
