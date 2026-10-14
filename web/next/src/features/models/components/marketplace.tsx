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
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
  DATA_TABLE_VIEW_MODES,
  useDataTableViewMode,
} from '@/components/data-table';
import { EmptyState } from '@/components/empty-state';
import { Button } from '@/components/ui/button';

import {
  filterCatalog,
  sortCatalog,
  type ModelEntry,
  type SortKey,
  type TokenUnit,
} from '../lib/catalog';
import {
  activeFilterCount,
  EMPTY_FILTERS,
  toCatalogFilter,
  type Filters,
} from '../lib/filter-state';
import type { PerfWindow } from '../lib/perf';
import { ModelCardGrid } from './card-grid';
import { FilterSidebar, type GroupOption } from './filter-sidebar';
import { ModelDrawer } from './model-drawer';
import { ModelTable } from './model-table';
import { PerfWindowContext } from './perf-window';
import { PriceUnitContext } from './price-unit';
import { SearchBar } from './search-bar';
import { Toolbar } from './toolbar';

export interface MarketplaceProps {
  entries: ModelEntry[];
  /** Groups the caller may price under. */
  groups: GroupOption[];
  /** Multiplier per group the caller may use. */
  groupRatios: Record<string, number>;
  /** Group the shown prices use; "" = the caller's own. */
  priceGroup: string;
  onPriceGroupChange: (g: string) => void;
  /** Multiplier currently applied to every shown price. */
  activeRatio: number;
  perfWindow: PerfWindow;
  onPerfWindowChange: (w: PerfWindow) => void;
}

/** Search, facet rail, toolbar, card/table results and the detail drawer. */
export function Marketplace(props: MarketplaceProps) {
  const { t } = useTranslation();
  const { entries } = props;
  const [q, setQ] = useState('');
  const [filters, setFilters] = useState<Filters>(EMPTY_FILTERS);
  const [unit, setUnit] = useState<TokenUnit>('M');
  // Without usage data "most used" would rank on nothing: hide it.
  const usageKnown = entries.some((e) => e.tokens !== null);
  const [sort, setSort] = useState<SortKey>(usageKnown ? 'popular' : 'name');
  const effectiveSort: SortKey = usageKnown || sort !== 'popular' ? sort : 'name';
  const [view, setView] = useDataTableViewMode({
    storageKey: 'models-view-mode',
    defaultMode: DATA_TABLE_VIEW_MODES.CARD,
  });
  const [openId, setOpenId] = useState<string | null>(null);

  const shown = useMemo(
    () =>
      sortCatalog(
        filterCatalog(entries, toCatalogFilter(q, filters, props.priceGroup)),
        effectiveSort,
      ),
    [entries, q, filters, props.priceGroup, effectiveSort],
  );
  const open = openId ? (entries.find((e) => e.id === openId) ?? null) : null;
  const facetCount = activeFilterCount(filters, props.priceGroup);
  const filtered = Boolean(q.trim()) || facetCount > 0;

  const reset = () => {
    setQ('');
    setFilters(EMPTY_FILTERS);
    props.onPriceGroupChange('');
  };

  const rail = (className?: string) => (
    <FilterSidebar
      entries={entries}
      filters={filters}
      onFiltersChange={setFilters}
      groups={props.groups}
      group={props.priceGroup}
      onGroupChange={props.onPriceGroupChange}
      onReset={reset}
      activeCount={facetCount}
      className={className}
    />
  );

  let results;
  if (shown.length === 0) {
    results = (
      <div data-testid='models-empty'>
        <EmptyState
          bordered
          title={
            entries.length === 0
              ? t('No models yet')
              : t('No model matches these filters')
          }
          description={
            entries.length === 0
              ? t('Add an upstream channel and its models appear here.')
              : undefined
          }
          action={
            filtered ? (
              <Button variant='outline' size='sm' onClick={reset}>
                {t('Clear all filters')}
              </Button>
            ) : undefined
          }
        />
      </div>
    );
  } else if (view === DATA_TABLE_VIEW_MODES.TABLE) {
    results = <ModelTable entries={shown} onOpen={setOpenId} />;
  } else {
    results = <ModelCardGrid entries={shown} onOpen={setOpenId} />;
  }

  return (
    <PriceUnitContext.Provider value={unit}>
      <PerfWindowContext.Provider value={props.perfWindow}>
        <div className='space-y-4'>
          <SearchBar value={q} onChange={setQ} className='max-w-2xl' />
          <div className='grid gap-4 xl:grid-cols-[280px_minmax(0,1fr)]'>
            {rail(
              'hidden xl:sticky xl:top-4 xl:block xl:max-h-[calc(100dvh-2rem)] xl:self-start xl:overflow-y-auto',
            )}
            <section className='min-w-0 space-y-4'>
              <Toolbar
                shown={shown.length}
                total={entries.length}
                filtered={filtered}
                sort={effectiveSort}
                usageKnown={usageKnown}
                onSortChange={setSort}
                unit={unit}
                onUnitChange={setUnit}
                perfWindow={props.perfWindow}
                onPerfWindowChange={props.onPerfWindowChange}
                view={view}
                onViewChange={setView}
                activeFilterCount={facetCount}
                filterPanel={rail('border-0 bg-transparent p-0 shadow-none')}
              />
              {results}
            </section>
          </div>
          <ModelDrawer
            entry={open}
            onClose={() => setOpenId(null)}
            groupRatios={props.groupRatios}
            activeRatio={props.activeRatio}
            onPerfWindowChange={props.onPerfWindowChange}
          />
        </div>
      </PerfWindowContext.Provider>
    </PriceUnitContext.Provider>
  );
}
