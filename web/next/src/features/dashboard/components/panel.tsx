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

import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

interface PanelProps {
  title: ReactNode
  description?: ReactNode
  icon?: ReactNode
  tone?: IconBadgeTone
  /** Right-aligned controls in the header (toggles, selects). */
  actions?: ReactNode
  testId?: string
  className?: string
  bodyClassName?: string
  /** Show a skeleton of this height instead of the children. */
  loadingHeight?: string
  children?: ReactNode
}

/**
 * Donor PanelWrapper look: a rounded bordered frame with a titled header.
 * Loading is a skeleton in the body; the caller decides empty / error
 * content, so a failed read can never be drawn as an empty one.
 */
export function Panel(props: PanelProps) {
  return (
    <section
      data-testid={props.testId}
      className={cn(
        'bg-card min-w-0 overflow-hidden rounded-2xl border shadow-xs',
        props.className
      )}
    >
      <div className='flex flex-wrap items-center justify-between gap-x-3 gap-y-2 border-b px-4 py-3 sm:px-5'>
        <div className='flex min-w-0 items-center gap-2'>
          {props.icon != null && (
            <IconBadge tone={props.tone ?? 'primary'} size='sm'>
              {props.icon}
            </IconBadge>
          )}
          <div className='flex min-w-0 flex-col'>
            <h3 className='truncate text-sm font-semibold'>{props.title}</h3>
            {props.description != null && (
              <p className='text-muted-foreground truncate text-xs'>
                {props.description}
              </p>
            )}
          </div>
        </div>
        {props.actions != null && (
          <div className='flex shrink-0 flex-wrap items-center gap-1.5'>
            {props.actions}
          </div>
        )}
      </div>
      <div className={cn('p-4 sm:p-5', props.bodyClassName)}>
        {props.loadingHeight != null ? (
          <Skeleton className={cn('w-full', props.loadingHeight)} />
        ) : (
          props.children
        )}
      </div>
    </section>
  )
}

interface SegmentedProps<T extends string> {
  value: T
  onChange: (value: T) => void
  options: ReadonlyArray<{ value: T; label: string; icon?: ReactNode }>
  label: string
  testIdPrefix?: string
}

/** Donor pill switcher (chart type / metric). Radio semantics for a11y. */
export function Segmented<T extends string>(props: SegmentedProps<T>) {
  return (
    <div
      role='radiogroup'
      aria-label={props.label}
      className='bg-muted/60 inline-flex h-8 max-w-full overflow-x-auto rounded-lg border p-0.5'
    >
      {props.options.map((o) => {
        const active = props.value === o.value
        return (
          <button
            key={o.value}
            type='button'
            role='radio'
            aria-checked={active}
            data-testid={
              props.testIdPrefix ? `${props.testIdPrefix}-${o.value}` : undefined
            }
            onClick={() => props.onChange(o.value)}
            className={cn(
              'focus-visible:ring-ring/50 inline-flex shrink-0 items-center gap-1.5 rounded-md px-3 text-xs font-medium transition-colors outline-none focus-visible:ring-[3px]',
              active
                ? 'bg-background text-foreground shadow-sm'
                : 'text-muted-foreground hover:text-foreground'
            )}
          >
            {o.icon}
            {o.label}
          </button>
        )
      })}
    </div>
  )
}
