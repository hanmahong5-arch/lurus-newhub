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
import { TriangleAlert } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { useDebounce } from '@/hooks/use-debounce'
import { toApiError } from '@/lib/api'

import { PageShell } from '../components/page-shell'
import { SORTS, type PricingSearch, type SortOption } from './constants'
import { PricingGate } from './gate'
import {
  activeFilterCount,
  applyFilters,
  buildFacets,
  FILTER_KEYS,
} from './lib/filters'
import type { PublicModel } from './lib/model'
import { ModelGrid } from './components/model-grid'
import { PricingSidebar } from './components/pricing-sidebar'
import { PricingTable } from './components/pricing-table'
import { PricingToolbar } from './components/pricing-toolbar'
import { SearchBar } from './components/search-bar'
import { EmptyCatalogue, NoMatches, PricingSkeleton } from './components/states'
import { useModelHealth, usePublicModels } from './use-public-models'

export interface PublicPricingPageProps {
  /** URL state, from the route's validated search. */
  search: PricingSearch
  /** Replace the URL state (no new history entry per keystroke). */
  onSearchChange: (next: PricingSearch) => void
  onOpenModel: (model: PublicModel) => void
}

const SEARCH_DEBOUNCE_MS = 300

/** Drop keys the patch sets to undefined, so the URL stays minimal. */
function merge(search: PricingSearch, patch: Partial<PricingSearch>) {
  const next: Record<string, unknown> = { ...search, ...patch }
  for (const key of Object.keys(next)) {
    if (next[key] === undefined || next[key] === '') delete next[key]
  }
  return next as PricingSearch
}

function PricingContent(props: PublicPricingPageProps) {
  const { t } = useTranslation()
  const { search, onSearchChange } = props
  const data = usePublicModels()
  const { health } = useModelHealth()

  // Typing filters at once; the URL catches up after a pause.
  const [query, setQuery] = useState(search.search ?? '')
  const debouncedQuery = useDebounce(query, SEARCH_DEBOUNCE_MS)
  useEffect(() => {
    if ((search.search ?? '') !== debouncedQuery) {
      onSearchChange(merge(search, { search: debouncedQuery || undefined }))
    }
    // Only a settled keystroke pushes to the URL.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debouncedQuery])

  const unit = search.unit ?? 'M'
  const view = search.view ?? 'card'
  const sort: SortOption = SORTS.includes(search.sort as SortOption)
    ? (search.sort as SortOption)
    : 'name'

  const update = useCallback(
    (patch: Partial<PricingSearch>) => onSearchChange(merge(search, patch)),
    [onSearchChange, search]
  )
  const clearFilters = useCallback(() => {
    setQuery('')
    const cleared: Partial<PricingSearch> = { search: undefined }
    for (const key of FILTER_KEYS) cleared[key] = undefined
    onSearchChange(merge(search, cleared))
  }, [onSearchChange, search])

  const facets = useMemo(() => buildFacets(data.models), [data.models])
  const filterCount = activeFilterCount(search)
  const visible = useMemo(
    () => applyFilters(data.models, { ...search, search: query, sort }, unit),
    [data.models, search, query, sort, unit]
  )
  // New filters start from page one.
  const resetKey = JSON.stringify([
    query,
    ...FILTER_KEYS.map((k) => search[k]),
    sort,
  ])

  if (data.isLoading) return <PricingSkeleton view={view} />
  if (data.isError) {
    return (
      <ErrorState
        title={t('Failed to load the price list')}
        description={toApiError(data.error).message}
        onRetry={() => void data.refetch()}
      />
    )
  }

  const sidebar = (
    <PricingSidebar
      search={search}
      facets={facets}
      total={data.models.length}
      vendors={data.payload?.vendors ?? []}
      groupRatios={data.payload?.groupRatio ?? {}}
      hasActiveFilters={filterCount > 0}
      onChange={update}
      onReset={clearFilters}
    />
  )

  let body: ReactNode
  if (data.models.length === 0) {
    body = <EmptyCatalogue />
  } else if (visible.length === 0) {
    body = (
      <NoMatches
        query={query}
        hasFilters={filterCount > 0}
        onClear={clearFilters}
      />
    )
  } else if (view === 'table') {
    body = (
      <PricingTable
        key={resetKey}
        models={visible}
        unit={unit}
        search={search}
        health={health}
        onOpen={props.onOpenModel}
      />
    )
  } else {
    body = (
      <ModelGrid
        key={resetKey}
        models={visible}
        unit={unit}
        search={search}
        health={health}
      />
    )
  }

  return (
    <>
      <header className='mx-auto mb-5 max-w-3xl pt-2 text-center sm:mb-10 sm:pt-6'>
        <h1 className='text-[clamp(2rem,5.5vw,3.25rem)] leading-[1.15] font-bold tracking-tight'>
          {t('Models and pricing')}
        </h1>
        <p className='text-muted-foreground/80 mt-3 text-sm sm:mt-4 sm:text-base'>
          {t('{{count}} models have a published price', {
            count: data.models.filter((m) => m.entry.priced).length,
          })}
        </p>
        <p className='text-muted-foreground/60 mx-auto mt-2 max-w-2xl text-xs leading-relaxed sm:text-sm'>
          {t(
            'Prices are per token, per request or per search unit, before any tenant discount. Choose a group to see its price.'
          )}
        </p>
        <SearchBar
          value={query}
          onChange={setQuery}
          onClear={() => setQuery('')}
          placeholder={t('Search model name, vendor, endpoint or tag...')}
          className='mx-auto mt-4 max-w-2xl sm:mt-6'
        />
      </header>

      {data.moneyMissing && (
        <Alert variant='destructive' className='mb-4'>
          <TriangleAlert aria-hidden />
          <AlertTitle>{t('Token prices are unavailable')}</AlertTitle>
          <AlertDescription>
            {t(
              'The site settings needed to convert prices could not be loaded. Reload to try again.'
            )}
          </AlertDescription>
        </Alert>
      )}

      <div className='grid gap-4 xl:grid-cols-[280px_minmax(0,1fr)]'>
        <div className='hover-scrollbar sticky top-20 hidden max-h-[calc(100dvh-6rem)] self-start overflow-y-auto xl:block'>
          {sidebar}
        </div>
        <div className='min-w-0 space-y-4'>
          <PricingToolbar
            filteredCount={visible.length}
            totalCount={data.models.length}
            sort={sort}
            onSortChange={(value) => update({ sort: value })}
            unit={unit}
            onUnitChange={(value) => update({ unit: value })}
            view={view}
            onViewChange={(value) => update({ view: value })}
            activeFilterCount={filterCount}
            filters={sidebar}
          />
          {body}
        </div>
      </div>
    </>
  )
}

/** Public price list: filter sidebar, search, sort, card or table view. */
export function PublicPricingPage(props: PublicPricingPageProps) {
  return (
    <PricingGate returnTo='/pricing' fallback={<PricingPageSkeleton />}>
      <PageShell className='max-w-[1800px] xl:px-8'>
        <PricingContent {...props} />
      </PageShell>
    </PricingGate>
  )
}

function PricingPageSkeleton() {
  return (
    <PageShell className='max-w-[1800px] xl:px-8'>
      <PricingSkeleton view='card' />
    </PageShell>
  )
}
