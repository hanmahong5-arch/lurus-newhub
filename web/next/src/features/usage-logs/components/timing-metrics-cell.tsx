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

import { dotColorMap, textColorMap, type StatusVariant } from '@/components/status-badge'
import { cn } from '@/lib/utils'

import {
  durationVariant,
  firstResponseVariant,
  formatSeconds,
} from '../lib/log-view'

/**
 * Softened fills for the full-height timing bar: the saturated dot tones read
 * as too loud beside dense numeric text.
 */
const barColor: Partial<Record<StatusVariant, string>> = {
  ...dotColorMap,
  success: 'bg-success/90',
  warning: 'bg-warning/80',
  danger: 'bg-destructive/80',
  neutral: 'bg-neutral/80',
}

interface TimingMetricsCellProps {
  /** Total request time in seconds; null when the relay recorded none. */
  durationSec: number | null
  completionTokens: number
  /** Time to first token in ms (streamed calls only). */
  firstTokenMs?: number | null
  isStream: boolean
  /** `bar`: a coloured segment beside the labels. `dot`: inline dots. */
  indicator?: 'bar' | 'dot'
  compact?: boolean
  className?: string
}

export function TimingMetricsCell(props: TimingMetricsCellProps) {
  const { t } = useTranslation()
  const indicator = props.indicator ?? 'bar'
  const firstSec =
    props.firstTokenMs != null && props.firstTokenMs > 0
      ? props.firstTokenMs / 1000
      : null
  const showFirst = props.isStream
  const firstVariant: StatusVariant =
    firstSec === null ? 'neutral' : firstResponseVariant(firstSec)
  const totalVariant: StatusVariant =
    props.durationSec === null
      ? 'neutral'
      : durationVariant(props.durationSec, props.completionTokens)

  const dot = (variant: StatusVariant) =>
    indicator === 'dot' ? (
      <span
        aria-hidden
        className={cn('size-1.5 shrink-0 rounded-full', dotColorMap[variant])}
      />
    ) : null

  const labels = (
    <div
      className={cn(
        'flex min-h-8 min-w-0 flex-col justify-center gap-0.5 text-xs leading-tight',
        props.compact &&
          'min-h-0 flex-row flex-wrap items-center gap-x-2.5 gap-y-1'
      )}
    >
      {showFirst && (
        <div className='flex items-baseline gap-1.5'>
          {dot(firstVariant)}
          <span className='text-muted-foreground shrink-0'>
            {t('First token')}
          </span>
          <span className={cn('tabular-nums', textColorMap[firstVariant])}>
            {firstSec === null ? t('N/A') : formatSeconds(firstSec)}
          </span>
        </div>
      )}
      <div className='flex items-baseline gap-1.5'>
        {dot(totalVariant)}
        <span className='text-muted-foreground shrink-0'>{t('Duration')}</span>
        <span className={cn('tabular-nums', textColorMap[totalVariant])}>
          {props.durationSec === null ? '--' : formatSeconds(props.durationSec)}
        </span>
      </div>
    </div>
  )

  if (indicator === 'dot') {
    return (
      <div className={cn('flex items-stretch', props.className)}>{labels}</div>
    )
  }
  return (
    <div className={cn('flex items-stretch gap-2', props.className)}>
      <span
        aria-hidden
        className={cn(
          'flex w-1 shrink-0 flex-col overflow-hidden rounded-full',
          !showFirst && barColor[totalVariant]
        )}
      >
        {showFirst && (
          <>
            <span className={cn('flex-1', barColor[firstVariant])} />
            <span className={cn('flex-1', barColor[totalVariant])} />
          </>
        )}
      </span>
      {labels}
    </div>
  )
}

interface StreamCellProps {
  isStream: boolean
  tokensPerSecond?: number | null
  compact?: boolean
  className?: string
}

/** "Stream" / "Non-stream" plus generation speed when it can be measured. */
export function StreamTpsCell(props: StreamCellProps) {
  const { t } = useTranslation()
  const tps =
    props.tokensPerSecond != null
      ? `${Math.round(props.tokensPerSecond)} t/s`
      : '—'
  return (
    <div
      className={cn(
        'flex shrink-0 flex-col items-start justify-center gap-0.5 text-xs leading-tight',
        props.compact && 'flex-row flex-wrap items-center gap-1.5',
        props.className
      )}
    >
      <span
        className={cn(
          'font-medium',
          props.isStream ? 'text-info' : 'text-muted-foreground'
        )}
      >
        {props.isStream ? t('Stream') : t('Non-stream')}
      </span>
      {(!props.compact || (props.isStream && props.tokensPerSecond != null)) && (
        <span className='text-muted-foreground/60 px-0.5 tabular-nums'>
          {tps}
        </span>
      )}
    </div>
  )
}
