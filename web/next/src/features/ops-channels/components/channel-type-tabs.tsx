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
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

export type ChannelTypeTab = {
  label: string
  value: string
  count: number
  iconNode?: React.ReactNode
}

/**
 * Horizontal tabs of channel types with their counts ("All" first). The server
 * reports the counts for the current status/group/search filters, so a tab
 * never promises rows the filters would hide.
 */
export function ChannelTypeTabs(props: {
  tabs: ChannelTypeTab[]
  /** `all` or a type id as string. */
  value: string
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  if (props.tabs.length <= 1) return null

  // Roving focus: arrow keys move between tabs and select as they go.
  const handleKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    const keys = ['ArrowRight', 'ArrowLeft', 'Home', 'End']
    if (!keys.includes(event.key)) return
    const nodes = [
      ...event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]'),
    ]
    const current = nodes.indexOf(document.activeElement as HTMLButtonElement)
    if (current < 0) return
    event.preventDefault()
    let next = current
    if (event.key === 'ArrowRight') next = (current + 1) % nodes.length
    else if (event.key === 'ArrowLeft') {
      next = (current - 1 + nodes.length) % nodes.length
    } else if (event.key === 'Home') next = 0
    else next = nodes.length - 1
    nodes[next]?.focus()
    const target = props.tabs[next]
    if (target) props.onChange(target.value)
  }

  return (
    <div
      role='tablist'
      aria-label={t('Channel types')}
      onKeyDown={handleKeyDown}
      className='-mx-1 flex shrink-0 gap-1.5 overflow-x-auto px-1 pb-0.5'
    >
      {props.tabs.map((tab) => {
        const selected = tab.value === props.value
        return (
          <button
            key={tab.value}
            type='button'
            role='tab'
            aria-selected={selected}
            tabIndex={selected ? 0 : -1}
            onClick={() => props.onChange(tab.value)}
            className={cn(
              'focus-visible:ring-ring/50 inline-flex h-8 shrink-0 items-center gap-1.5 rounded-full border px-3 text-sm whitespace-nowrap transition-colors outline-none focus-visible:ring-3',
              selected
                ? 'border-primary bg-primary text-primary-foreground'
                : 'bg-background text-muted-foreground hover:bg-muted hover:text-foreground'
            )}
          >
            {tab.iconNode}
            <span>{t(tab.label)}</span>
            <span
              className={cn(
                'font-mono text-xs tabular-nums',
                selected ? 'text-primary-foreground/80' : 'text-muted-foreground'
              )}
            >
              {tab.count}
            </span>
          </button>
        )
      })}
    </div>
  )
}
