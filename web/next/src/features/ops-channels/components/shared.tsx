import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Label } from '@/components/ui/label'

import { formatCountdown, formatPercent, REASON_LABELS } from '../lib/map'
import { formatTs, useNowSec } from '../lib/use-labels'
import type { PlanWindow } from '../types'

export function Field(props: {
  label: string
  hint?: string
  children: ReactNode
}) {
  return (
    <div className='grid gap-1.5'>
      <Label>{props.label}</Label>
      {props.children}
      {props.hint != null && (
        <p className='text-muted-foreground text-xs'>{props.hint}</p>
      )}
    </div>
  )
}
export function ReasonBadges(props: { reasons: string[] }) {
  const { t } = useTranslation()
  return (
    <div className='flex flex-wrap gap-1'>
      {props.reasons.map((r) => (
        <Badge
          key={r}
          variant={r === 'cooling_429' ? 'warning' : 'destructive'}
          data-testid={`reason-${r}`}
        >
          {t(REASON_LABELS[r] ?? r)}
        </Badge>
      ))}
    </div>
  )
}

/** Live "time left" for a cooldown deadline (Unix seconds). */
export function Countdown(props: { until: number }) {
  const now = useNowSec()
  if (props.until <= now) return null
  return (
    <span className='tabular-nums' data-testid='cooldown-countdown'>
      {formatCountdown(props.until, now)}
    </span>
  )
}

/** A horizontal bar for a 0..1+ ratio; the fill is capped, the label is not. */
export function RatioBar(props: {
  ratio: number
  label: string
  warnAt?: number
}) {
  const pct = Math.max(0, Math.min(1, props.ratio)) * 100
  const hot = props.ratio >= (props.warnAt ?? 0.9)
  return (
    <div
      className='bg-muted h-1.5 w-full overflow-hidden rounded-full'
      role='progressbar'
      aria-label={props.label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(pct)}
    >
      <div
        className={hot ? 'bg-destructive h-1.5' : 'bg-primary h-1.5'}
        style={{ width: `${pct}%` }}
      />
    </div>
  )
}

export function WindowBars(props: { windows: PlanWindow[] }) {
  const { t } = useTranslation()
  if (props.windows.length === 0) return null
  const names: Record<string, string> = {
    '5h': t('5-hour window'),
    weekly: t('Weekly window'),
  }
  return (
    <div className='grid gap-2' data-testid='window-bars'>
      {props.windows.map((w) => {
        const name = names[w.name] ?? w.name
        return (
          <div key={w.name} className='grid gap-1'>
            <div className='flex items-center justify-between text-xs'>
              <span>{name}</span>
              <span className='text-muted-foreground tabular-nums'>
                {formatPercent(w.usedPct / 100)}
                {w.resetAt > 0 &&
                  ` · ${t('resets {{time}}', { time: formatTs(w.resetAt) })}`}
              </span>
            </div>
            <RatioBar ratio={w.usedPct / 100} label={name} />
          </div>
        )
      })}
    </div>
  )
}
