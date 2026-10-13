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
import { Search } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardFooter, CardHeader } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

import type { ViewMode } from '../constants'

/** No model passes the current search and filters. */
export function NoMatches(props: {
  query: string
  hasFilters: boolean
  onClear: () => void
}) {
  const { t } = useTranslation()
  const hasQuery = props.query.trim() !== ''
  return (
    <div className='flex min-h-[320px] flex-col items-center justify-center rounded-lg border border-dashed px-6 py-12 text-center'>
      <Search aria-hidden className='text-muted-foreground/40 mb-3 size-10' />
      <h3 className='text-foreground mb-1 text-base font-semibold'>
        {t('No models found')}
      </h3>
      <p className='text-muted-foreground mb-5 max-w-xs text-sm'>
        {hasQuery
          ? t(
              'No results for "{{query}}". Try adjusting your search or filters.',
              { query: props.query }
            )
          : t('No models match your current filters.')}
      </p>
      {(props.hasFilters || hasQuery) && (
        <Button variant='outline' size='sm' onClick={props.onClear}>
          {t('Clear all filters')}
        </Button>
      )}
    </div>
  )
}

/** The operator has published no priced model at all. */
export function EmptyCatalogue() {
  const { t } = useTranslation()
  return (
    <div className='flex min-h-[320px] flex-col items-center justify-center rounded-lg border border-dashed px-6 py-12 text-center'>
      <h3 className='text-foreground mb-1 text-base font-semibold'>
        {t('No models yet')}
      </h3>
      <p className='text-muted-foreground max-w-sm text-sm'>
        {t('No priced model has been published on this site yet.')}
      </p>
    </div>
  )
}

/** Page-shaped placeholder while the list loads. */
export function PricingSkeleton(props: { view: ViewMode }) {
  return (
    <div aria-busy='true' role='status'>
      <div className='mx-auto mb-5 flex max-w-3xl flex-col items-center pt-5 sm:mb-10 sm:pt-10'>
        <Skeleton className='h-12 w-48 max-w-full sm:w-64' />
        <Skeleton className='mt-3 h-5 w-56 max-w-full sm:mt-4 sm:h-6' />
        <Skeleton className='mt-4 h-10 w-full max-w-2xl sm:mt-6' />
      </div>
      <div className='grid gap-4 xl:grid-cols-[280px_minmax(0,1fr)]'>
        <div className='hidden self-start rounded-xl border p-3 xl:block'>
          <Skeleton className='mb-4 h-5 w-24' />
          {Array.from({ length: 5 }, (_, i) => (
            <div key={i} className='flex flex-col gap-3 border-b py-4 last:border-0'>
              <Skeleton className='h-4 w-28' />
              <div className='flex flex-wrap gap-2'>
                <Skeleton className='h-7 w-24' />
                <Skeleton className='h-7 w-20' />
                <Skeleton className='h-7 w-28' />
              </div>
            </div>
          ))}
        </div>
        <div className='flex min-w-0 flex-col gap-4'>
          <Skeleton className='h-14 w-full rounded-xl' />
          {props.view === 'table' ? (
            <div className='overflow-hidden rounded-xl border'>
              {Array.from({ length: 10 }, (_, i) => (
                <div key={i} className='flex gap-4 border-b p-4 last:border-0'>
                  <Skeleton className='h-5 w-40 max-w-full' />
                  <Skeleton className='h-5 flex-1' />
                  <Skeleton className='h-5 w-20' />
                </div>
              ))}
            </div>
          ) : (
            <div className='grid grid-cols-1 gap-3 sm:gap-4 md:grid-cols-2 xl:grid-cols-3'>
              {Array.from({ length: 6 }, (_, i) => (
                <Card key={i} className='gap-3'>
                  <CardHeader className='flex flex-row gap-3'>
                    <Skeleton className='size-10 shrink-0' />
                    <div className='flex min-w-0 flex-1 flex-col gap-2'>
                      <Skeleton className='h-5 w-40 max-w-full' />
                      <Skeleton className='h-3 w-20' />
                    </div>
                  </CardHeader>
                  <CardContent className='flex flex-col gap-3'>
                    <Skeleton className='h-3.5 w-full' />
                    <Skeleton className='h-3.5 w-4/5' />
                    <div className='grid grid-cols-3 gap-3'>
                      <Skeleton className='h-10' />
                      <Skeleton className='h-10' />
                      <Skeleton className='h-10' />
                    </div>
                  </CardContent>
                  <CardFooter className='border-0 bg-transparent pt-0'>
                    <Skeleton className='ms-auto h-7 w-16' />
                  </CardFooter>
                </Card>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
