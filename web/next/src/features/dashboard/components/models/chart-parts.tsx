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
import type { SeriesDef } from '../../lib/series'
import { seriesColor } from '../../lib/series-style'

/** Wrapping legend; recharts' own legend row cannot wrap 13 long names. */
export function SeriesLegend(props: {
  series: SeriesDef[]
  nameOf: (s: SeriesDef) => string
}) {
  return (
    <ul
      className='mt-3 flex flex-wrap justify-center gap-x-4 gap-y-1.5 px-2 text-xs'
      data-testid='series-legend'
    >
      {props.series.map((s) => (
        <li key={s.key} className='flex min-w-0 items-center gap-1.5'>
          <span
            className='size-2 shrink-0 rounded-sm'
            style={{ backgroundColor: seriesColor(props.series, s.key) }}
            aria-hidden='true'
          />
          <span className='max-w-40 truncate font-mono'>
            {props.nameOf(s)}
          </span>
        </li>
      ))}
    </ul>
  )
}

interface TooltipEntry {
  dataKey?: string | number
  value?: number | string
}

/**
 * Multi-series tooltip: every non-zero series at the hovered bucket, largest
 * first, plus the bucket total. Values are raw (quota or counts); `format`
 * turns them into display text.
 */
export function UsageTooltip(props: {
  active?: boolean
  payload?: TooltipEntry[]
  label?: string | number
  series: SeriesDef[]
  nameOf: (s: SeriesDef) => string
  format: (value: number) => string
  totalLabel: string
  maxRows?: number
}) {
  if (!props.active || !props.payload?.length) return null
  const rows = props.payload
    .map((p) => ({
      key: String(p.dataKey ?? ''),
      value: Number(p.value) || 0,
    }))
    .filter((r) => r.value > 0)
    .sort((a, b) => b.value - a.value)
  if (rows.length === 0) return null
  const total = rows.reduce((s, r) => s + r.value, 0)
  const max = props.maxRows ?? 12
  const shown = rows.slice(0, max)
  const hidden = rows.length - shown.length
  return (
    <div className='bg-background border-border/60 grid min-w-40 gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs shadow-xl'>
      <div className='font-medium'>{props.label}</div>
      <div className='grid gap-1'>
        {shown.map((r) => {
          const def = props.series.find((s) => s.key === r.key)
          return (
            <div key={r.key} className='flex items-center gap-2'>
              <span
                className='size-2 shrink-0 rounded-[2px]'
                style={{ backgroundColor: seriesColor(props.series, r.key) }}
                aria-hidden='true'
              />
              <span className='max-w-40 truncate font-mono'>
                {def ? props.nameOf(def) : r.key}
              </span>
              <span className='ms-auto font-mono font-medium tabular-nums'>
                {props.format(r.value)}
              </span>
            </div>
          )
        })}
        {hidden > 0 && (
          <div className='text-muted-foreground'>+{hidden}</div>
        )}
      </div>
      <div className='border-border/60 flex items-center justify-between gap-4 border-t pt-1'>
        <span className='text-muted-foreground'>{props.totalLabel}</span>
        <span className='font-mono font-semibold tabular-nums'>
          {props.format(total)}
        </span>
      </div>
    </div>
  )
}
