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
import { Check } from 'lucide-react'
import { Fragment } from 'react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

interface StepperProps {
  steps: string[]
  /** 1-based index of the active step; beyond the last = all done. */
  current: number
  'data-testid'?: string
}

function stateOf(n: number, current: number): 'done' | 'active' | 'todo' {
  if (n < current) return 'done'
  if (n === current) return 'active'
  return 'todo'
}

/**
 * Progress rail of a wizard. Completed steps show a check, the active step is
 * announced with aria-current, and on narrow screens only the active label is
 * spelled out so four steps still fit one line.
 */
export function Stepper(props: StepperProps) {
  const { t } = useTranslation()
  const { steps, current } = props
  return (
    <ol
      className='flex w-full items-center gap-2'
      aria-label={t('Setup progress')}
      data-testid={props['data-testid']}
    >
      {steps.map((label, i) => {
        const n = i + 1
        const done = n < current
        const active = n === current
        return (
          <Fragment key={label}>
            <li
              className='flex shrink-0 items-center gap-2'
              aria-current={active ? 'step' : undefined}
              data-state={stateOf(n, current)}
            >
              <span
                className={cn(
                  'flex size-6 items-center justify-center rounded-full border text-xs font-semibold',
                  done && 'border-transparent bg-emerald-600 text-white',
                  active &&
                    'border-transparent bg-primary text-primary-foreground',
                  !done && !active && 'bg-muted text-muted-foreground'
                )}
              >
                {done ? <Check className='size-3.5' aria-hidden='true' /> : n}
              </span>
              <span
                className={cn(
                  'text-sm font-medium',
                  active ? 'text-foreground' : 'text-muted-foreground',
                  !active && 'max-sm:sr-only'
                )}
              >
                {label}
              </span>
            </li>
            {n < steps.length && (
              <li
                aria-hidden='true'
                className={cn(
                  'h-px min-w-3 flex-1',
                  done ? 'bg-emerald-600' : 'bg-border'
                )}
              />
            )}
          </Fragment>
        )
      })}
    </ol>
  )
}
