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
import { Filter } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import {
  DataTableViewModeToggle,
  type DataTableViewMode,
} from '@/components/data-table';
import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';

import type { SortKey, TokenUnit } from '../lib/catalog';
import { PERF_WINDOWS, type PerfWindow } from '../lib/perf';

const WINDOW_LABEL: Record<PerfWindow, string> = {
  1: '1h',
  24: '24h',
  168: '7d',
};

export interface ToolbarProps {
  shown: number;
  total: number;
  filtered: boolean;
  sort: SortKey;
  /** Sorting by usage needs usage data; without it that option is hidden. */
  usageKnown: boolean;
  onSortChange: (s: SortKey) => void;
  unit: TokenUnit;
  onUnitChange: (u: TokenUnit) => void;
  perfWindow: PerfWindow;
  onPerfWindowChange: (w: PerfWindow) => void;
  view: DataTableViewMode;
  onViewChange: (v: DataTableViewMode) => void;
  activeFilterCount: number;
  /** The filter rail, rendered inside the mobile sheet. */
  filterPanel: ReactNode;
}

/** Count, price unit, latency window, sort and view mode over the results. */
export function Toolbar(props: ToolbarProps) {
  const { t } = useTranslation();
  const [filtersOpen, setFiltersOpen] = useState(false);

  return (
    <div className='bg-card rounded-xl border p-3'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='flex items-center gap-2'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => setFiltersOpen(true)}
            className='gap-1.5 xl:hidden'
            data-testid='models-open-filters'
          >
            <Filter className='size-4' />
            {t('Filter')}
            {props.activeFilterCount > 0 && (
              <Badge className='ml-0.5 size-5 justify-center p-0 text-[10px]'>
                {props.activeFilterCount}
              </Badge>
            )}
          </Button>
          <span
            className='text-muted-foreground text-sm'
            data-testid='models-count'
          >
            {props.filtered
              ? t('{{n}} of {{total}}', {
                  n: props.shown,
                  total: props.total,
                })
              : t('{{n}} models', { n: props.total })}
          </span>
        </div>

        <div className='flex min-w-0 flex-wrap items-center gap-2'>
          <ToggleGroup
            value={[props.unit]}
            onValueChange={(values) => {
              if (values[0] === 'M' || values[0] === 'K') {
                props.onUnitChange(values[0]);
              }
            }}
            variant='outline'
            size='sm'
            aria-label={t('Token unit')}
          >
            <ToggleGroupItem value='M' data-testid='models-unit-M'>
              /1M
            </ToggleGroupItem>
            <ToggleGroupItem value='K' data-testid='models-unit-K'>
              /1K
            </ToggleGroupItem>
          </ToggleGroup>

          <ToggleGroup
            value={[String(props.perfWindow)]}
            onValueChange={(values) => {
              const w = Number(values[0]);
              if ((PERF_WINDOWS as readonly number[]).includes(w)) {
                props.onPerfWindowChange(w as PerfWindow);
              }
            }}
            variant='outline'
            size='sm'
            aria-label={t('Latency window')}
          >
            {PERF_WINDOWS.map((w) => (
              <ToggleGroupItem
                key={w}
                value={String(w)}
                data-testid={`models-window-${w}`}
              >
                {WINDOW_LABEL[w]}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>

          <NativeSelect
            size='sm'
            value={props.sort}
            onChange={(ev) => props.onSortChange(ev.target.value as SortKey)}
            data-testid='models-sort'
            aria-label={t('Sort')}
          >
            {props.usageKnown && (
              <NativeSelectOption value='popular'>
                {t('Most used')}
              </NativeSelectOption>
            )}
            <NativeSelectOption value='name'>{t('Name')}</NativeSelectOption>
            <NativeSelectOption value='input_asc'>
              {t('Input price ↑')}
            </NativeSelectOption>
            <NativeSelectOption value='input_desc'>
              {t('Input price ↓')}
            </NativeSelectOption>
            <NativeSelectOption value='output_asc'>
              {t('Output price ↑')}
            </NativeSelectOption>
            <NativeSelectOption value='fastest'>
              {t('Fastest (p50)')}
            </NativeSelectOption>
            <NativeSelectOption value='reliable'>
              {t('Most reliable')}
            </NativeSelectOption>
          </NativeSelect>

          <DataTableViewModeToggle
            value={props.view}
            onChange={props.onViewChange}
          />
        </div>
      </div>

      <Sheet open={filtersOpen} onOpenChange={setFiltersOpen}>
        <SheetContent
          side='left'
          className={sideDrawerContentClassName('sm:max-w-md')}
        >
          <SheetHeader className={sideDrawerHeaderClassName()}>
            <SheetTitle>{t('Filter')}</SheetTitle>
            <SheetDescription>
              {t('Refine models by vendor, group, type, and tags.')}
            </SheetDescription>
          </SheetHeader>
          <div className={sideDrawerFormClassName('gap-0')}>
            {props.filterPanel}
          </div>
        </SheetContent>
      </Sheet>
    </div>
  );
}
