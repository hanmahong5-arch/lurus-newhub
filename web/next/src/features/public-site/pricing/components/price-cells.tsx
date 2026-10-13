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

import { getLobeIcon } from '@/components/lobe-icon'
import { StatusBadge } from '@/components/status-badge'
import { cn } from '@/lib/utils'

import type { ModelHealth } from '../../api'
import type { TokenUnit } from '../constants'
import {
  healthLabelKey,
  typeLabelKey,
  unitLabel,
  useAmountFormatter,
} from '../lib/labels'
import { pricingTypeOf, type PriceView, type PublicModel } from '../lib/model'

const TYPE_VARIANT = {
  token: 'info',
  call: 'warning',
  search: 'purple',
} as const

export function ModelAvatar(props: {
  model: PublicModel
  size?: number
  className?: string
}) {
  const size = props.size ?? 20
  const iconKey = props.model.icon || props.model.vendorIcon
  return (
    <span
      aria-hidden
      className={cn('flex shrink-0 items-center justify-center', props.className)}
    >
      {iconKey ? (
        getLobeIcon(iconKey, size)
      ) : (
        <span className='text-muted-foreground text-sm font-bold'>
          {props.model.id.charAt(0).toUpperCase() || '?'}
        </span>
      )}
    </span>
  )
}

export function TypeBadge(props: { model: PublicModel; className?: string }) {
  const { t } = useTranslation()
  const type = pricingTypeOf(props.model.entry)
  return (
    <StatusBadge
      label={t(typeLabelKey(type))}
      variant={TYPE_VARIANT[type]}
      size='sm'
      copyable={false}
      className={props.className}
    />
  )
}

const HEALTH_DOT: Record<ModelHealth, string> = {
  operational: 'bg-success',
  degraded: 'bg-warning',
  down: 'bg-destructive',
}

/** Status dot + word; renders nothing when the model has no health data. */
export function HealthBadge(props: {
  health: ModelHealth | undefined
  className?: string
}) {
  const { t } = useTranslation()
  if (!props.health) return null
  return (
    <span
      className={cn(
        'text-muted-foreground inline-flex items-center gap-1.5 text-xs',
        props.className
      )}
      data-testid='model-health'
    >
      <span
        aria-hidden
        className={cn('size-2 rounded-full', HEALTH_DOT[props.health])}
      />
      {t(healthLabelKey(props.health))}
    </span>
  )
}

/** One labelled price: caption above, figure and unit below. */
export function PriceItem(props: {
  label?: string
  value: string | null
  unit?: string
  className?: string
}) {
  const { t } = useTranslation()
  return (
    <div className={cn('flex min-w-0 flex-col gap-1', props.className)}>
      {props.label && (
        <span className='text-muted-foreground text-xs'>{props.label}</span>
      )}
      <span className='flex flex-wrap items-baseline gap-x-1 font-mono text-sm font-semibold tabular-nums'>
        {props.value ?? (
          <span className='text-muted-foreground font-sans font-normal'>
            {t('Not configured')}
          </span>
        )}
        {props.value !== null && props.unit && (
          <span className='text-muted-foreground text-xs font-normal whitespace-nowrap'>
            / {props.unit}
          </span>
        )}
      </span>
    </div>
  )
}

/**
 * The price block of a card or the detail page. `view` comes from `priceView`;
 * unpriced and unavailable are stated, never shown as zero.
 */
export function PriceSummary(props: {
  view: PriceView
  unit: TokenUnit
  className?: string
}) {
  const { t } = useTranslation()
  const fmt = useAmountFormatter()
  const { view } = props
  const label = unitLabel(props.unit)

  if (view.kind === 'unpriced') {
    return (
      <span className={cn('text-muted-foreground text-sm', props.className)}>
        {t('Unpriced')}
      </span>
    )
  }
  if (view.kind === 'unavailable') {
    return (
      <span className={cn('text-muted-foreground text-sm', props.className)}>
        {t('Price unavailable')}
      </span>
    )
  }
  if (view.kind === 'call') {
    return (
      <div className={props.className}>
        <PriceItem value={fmt(view.price)} unit={t('request')} />
      </div>
    )
  }
  if (view.kind === 'search') {
    return (
      <div className={props.className}>
        <PriceItem value={fmt(view.price)} unit={t('search unit')} />
      </div>
    )
  }
  return (
    <div
      className={cn(
        'grid grid-cols-[repeat(auto-fit,minmax(88px,1fr))] gap-x-3 gap-y-2',
        props.className
      )}
    >
      <PriceItem label={t('Input')} value={fmt(view.input)} unit={label} />
      {view.outputBilled ? (
        <PriceItem label={t('Output')} value={fmt(view.output)} unit={label} />
      ) : (
        <div className='flex min-w-0 flex-col gap-1'>
          <span className='text-muted-foreground text-xs'>{t('Output')}</span>
          <span className='text-muted-foreground text-sm'>
            {t('Not billed')}
          </span>
        </div>
      )}
      {view.cache !== null && (
        <PriceItem label={t('Cached')} value={fmt(view.cache)} unit={label} />
      )}
    </div>
  )
}
