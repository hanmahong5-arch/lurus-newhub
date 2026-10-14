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
import { Route } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { cn } from '@/lib/utils'

interface ModelBadgeProps {
  modelName: string
  /** Upstream model when the request model was remapped. */
  upstreamModel?: string | null
  wrapText?: boolean
  /** Click copies the name (off inside other clickable surfaces). */
  copyable?: boolean
  className?: string
}

/** The model as a copyable badge; a mapped request also names its target. */
export function ModelBadge(props: ModelBadgeProps) {
  const { t } = useTranslation()
  return (
    <span className='inline-flex min-w-0 max-w-full items-center gap-1'>
      <StatusBadge
        copyText={props.modelName}
        copyable={props.copyable ?? true}
        size='sm'
        autoColor={props.modelName}
        className={cn(
          'border-border/60 bg-muted/30 h-6 max-w-none gap-1.5 rounded-md border px-2',
          props.wrapText && 'h-auto min-h-6 max-w-full py-px whitespace-normal',
          props.className
        )}
      >
        <span
          className={
            props.wrapText
              ? 'line-clamp-2 leading-5 [overflow-wrap:anywhere]'
              : 'whitespace-nowrap'
          }
        >
          {props.modelName}
        </span>
      </StatusBadge>
      {props.upstreamModel && (
        <span
          title={`${t('Routed to')} ${props.upstreamModel}`}
          aria-label={`${t('Routed to')} ${props.upstreamModel}`}
        >
          <Route
            className='text-muted-foreground size-3 shrink-0'
            aria-hidden='true'
          />
        </span>
      )}
    </span>
  )
}
