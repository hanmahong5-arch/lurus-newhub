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
import { Loader2, type LucideIcon } from 'lucide-react'
import type { ReactNode } from 'react'

import { Card, CardContent } from '@/components/ui/card'
import { cn } from '@/lib/utils'

/**
 * The single card every status screen of the sign-in flow is drawn on: an
 * icon (or spinner), a headline, a line of explanation and optional actions.
 */
export function AuthStatusCard(props: {
  icon?: LucideIcon
  busy?: boolean
  tone?: 'default' | 'danger' | 'warning'
  title: ReactNode
  children?: ReactNode
  actions?: ReactNode
  testId?: string
}) {
  const Icon = props.icon
  const tone = props.tone ?? 'default'
  return (
    <Card data-testid={props.testId} className='shadow-sm'>
      <CardContent className='flex flex-col items-center gap-3 px-6 py-6 text-center'>
        {props.busy ? (
          <Loader2
            aria-hidden='true'
            className='text-muted-foreground size-8 animate-spin'
          />
        ) : (
          Icon && (
            <span
              className={cn(
                'flex size-11 items-center justify-center rounded-full',
                tone === 'danger' && 'bg-destructive/10 text-destructive',
                tone === 'warning' && 'bg-warning/10 text-warning',
                tone === 'default' && 'bg-muted text-muted-foreground'
              )}
            >
              <Icon aria-hidden='true' className='size-5' />
            </span>
          )
        )}
        <h1 className='text-lg leading-snug font-semibold'>{props.title}</h1>
        {props.children && (
          <div className='text-muted-foreground max-w-prose text-sm leading-relaxed'>
            {props.children}
          </div>
        )}
        {props.actions && (
          <div className='mt-2 flex flex-wrap items-center justify-center gap-2'>
            {props.actions}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
