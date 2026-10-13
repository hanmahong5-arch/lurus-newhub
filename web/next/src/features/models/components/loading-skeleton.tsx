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
import { Card, CardContent, CardHeader } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';

/** Placeholder shaped like the marketplace: search, filter rail, card grid. */
export function MarketplaceSkeleton() {
  return (
    <div aria-busy='true' className='space-y-4' data-testid='models-loading'>
      <Skeleton className='h-10 w-full max-w-2xl' />
      <div className='grid gap-4 xl:grid-cols-[280px_minmax(0,1fr)]'>
        <div className='hidden self-start rounded-xl border p-3 xl:block'>
          <Skeleton className='mb-4 h-5 w-24' />
          {Array.from({ length: 5 }, (_, i) => (
            <div
              key={i}
              className='flex flex-col gap-3 border-b py-4 last:border-0'
            >
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
          <div className='flex flex-wrap items-center justify-between gap-3 rounded-xl border p-3'>
            <Skeleton className='h-7 w-20' />
            <div className='flex flex-wrap gap-2'>
              <Skeleton className='h-7 w-24' />
              <Skeleton className='h-7 w-28' />
              <Skeleton className='h-7 w-16' />
            </div>
          </div>
          <div className='grid grid-cols-1 gap-3 sm:gap-4 md:grid-cols-2 2xl:grid-cols-3'>
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
              </Card>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
