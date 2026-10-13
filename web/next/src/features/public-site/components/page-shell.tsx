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
import type { ReactNode } from 'react'

import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

/** Padded column for a public page; the top padding clears the fixed header. */
export function PageShell(props: {
  children: ReactNode
  className?: string
  /** Narrow reading column instead of the wide one. */
  narrow?: boolean
}) {
  return (
    <div
      className={cn(
        'mx-auto w-full px-4 pt-24 pb-12 sm:px-6',
        props.narrow ? 'max-w-3xl' : 'max-w-6xl',
        props.className
      )}
    >
      {props.children}
    </div>
  )
}

/** Placeholder while a document loads. */
export function DocumentSkeleton() {
  return (
    <PageShell narrow>
      <div className='flex flex-col gap-4' aria-busy='true'>
        <Skeleton className='h-8 w-[45%]' />
        <Skeleton className='h-4 w-full' />
        <Skeleton className='h-4 w-[90%]' />
        <Skeleton className='h-4 w-[80%]' />
      </div>
    </PageShell>
  )
}
