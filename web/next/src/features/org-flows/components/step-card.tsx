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
import { useTranslation } from 'react-i18next'

import { Alert } from '@/components/ui/alert'
import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'

interface StepCardProps {
  step: number
  total: number
  title: string
  description?: string
  children: ReactNode
  /** Buttons, laid out on one row at the bottom. */
  footer?: ReactNode
  className?: string
  'data-testid'?: string
}

/** One wizard step: "Step n of m" eyebrow, title, body and an action row. */
export function StepCard(props: StepCardProps) {
  const { t } = useTranslation()
  return (
    <section
      className={cn('flex flex-col gap-5', props.className)}
      data-testid={props['data-testid']}
    >
      <header className='space-y-1'>
        <p className='text-muted-foreground text-xs font-medium tracking-wide uppercase'>
          {t('Step {{step}} of {{total}}', {
            step: props.step,
            total: props.total,
          })}
        </p>
        <h2 className='text-xl font-semibold tracking-tight'>{props.title}</h2>
        {props.description != null && (
          <p className='text-muted-foreground text-sm'>{props.description}</p>
        )}
      </header>
      {props.children}
      {props.footer != null && (
        <footer className='flex flex-wrap items-center justify-end gap-2 border-t pt-4'>
          {props.footer}
        </footer>
      )}
    </section>
  )
}

/** Labelled form field with an optional hint line. */
export function Field(props: {
  label: string
  htmlFor?: string
  hint?: string
  children: ReactNode
}) {
  return (
    <div className='grid gap-1.5'>
      <Label htmlFor={props.htmlFor}>{props.label}</Label>
      {props.children}
      {props.hint != null && (
        <p className='text-muted-foreground text-xs'>{props.hint}</p>
      )}
    </div>
  )
}

/** Label / value rows of a review step. */
export function ReviewList(props: {
  rows: Array<{ label: string; value: ReactNode }>
  'data-testid'?: string
}) {
  return (
    <dl
      className='divide-y rounded-lg border'
      data-testid={props['data-testid']}
    >
      {props.rows.map((row) => (
        <div
          key={row.label}
          className='grid grid-cols-1 gap-1 px-3 py-2.5 sm:grid-cols-[11rem_1fr] sm:gap-4'
        >
          <dt className='text-muted-foreground text-sm'>{row.label}</dt>
          <dd className='min-w-0 text-sm font-medium break-words'>
            {row.value}
          </dd>
        </div>
      ))}
    </dl>
  )
}

/** Inline error under a step. */
export function StepError(props: {
  children: ReactNode
  'data-testid'?: string
}) {
  return (
    <Alert variant='destructive' data-testid={props['data-testid']}>
      <div data-slot='alert-description'>{props.children}</div>
    </Alert>
  )
}
