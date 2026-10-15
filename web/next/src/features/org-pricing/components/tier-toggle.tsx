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
import { ChevronDown, ChevronRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

/**
 * The context-tiers toggle of one row. A model with zero tiers is not
 * "disabled": it is the entry point for adding the first one, so it stays a
 * real button, only visually quieter than a model that already has tiers.
 */
export function TierToggle(props: {
  count: number
  expanded: boolean
  onClick: () => void
  testId?: string
  /** Read-only viewers have nothing to add, so zero tiers says "none". */
  readOnly?: boolean
}) {
  const { t } = useTranslation()
  const tiered = props.count > 0
  const Chevron = props.expanded ? ChevronDown : ChevronRight
  let label: string
  if (tiered) {
    label = props.count === 1 ? t('1 tier') : t('{{n}} tiers', { n: props.count })
  } else {
    label = props.readOnly ? t('No tiers') : t('Not tiered')
  }
  return (
    <Button
      type='button'
      variant='outline'
      size='sm'
      aria-expanded={props.expanded}
      data-testid={props.testId}
      data-tiered={tiered ? 'true' : 'false'}
      className={cn(
        'h-7 gap-1 px-2 text-xs',
        tiered ? 'font-semibold' : 'text-muted-foreground font-normal'
      )}
      onClick={props.onClick}
    >
      <Chevron className='size-3.5' aria-hidden />
      {label}
      {!tiered && !props.readOnly && <span aria-hidden>+</span>}
    </Button>
  )
}
