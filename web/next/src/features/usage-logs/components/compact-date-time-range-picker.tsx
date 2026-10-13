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
import { CalendarDays } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import dayjs from '@/lib/dayjs'
import { cn } from '@/lib/utils'

/** `<input type="datetime-local">` value format; '' = unset. */
const INPUT_FORMAT = 'YYYY-MM-DDTHH:mm'

export interface DateTimeRange {
  start: string
  end: string
}

interface CompactDateTimeRangePickerProps {
  start: string
  end: string
  onChange: (range: DateTimeRange) => void
  /** Shown while no bound is set (e.g. the lookback the query falls back to). */
  placeholder?: string
  className?: string
}

type Preset = 'today' | '7d' | 'week' | '30d' | 'month'

function presetRange(kind: Preset): DateTimeRange {
  const now = dayjs()
  const ranges: Record<Preset, [dayjs.Dayjs, dayjs.Dayjs]> = {
    today: [now.startOf('day'), now.endOf('day')],
    '7d': [now.subtract(6, 'day').startOf('day'), now.endOf('day')],
    week: [now.startOf('week'), now.endOf('week')],
    '30d': [now.subtract(29, 'day').startOf('day'), now.endOf('day')],
    month: [now.startOf('month'), now.endOf('month')],
  }
  const [a, b] = ranges[kind]
  return { start: a.format(INPUT_FORMAT), end: b.format(INPUT_FORMAT) }
}

function show(value: string): string {
  const d = dayjs(value)
  return value && d.isValid() ? d.format('YYYY-MM-DD HH:mm') : '-'
}

/**
 * One button that opens a popover with a start/end pair and quick presets.
 * Values are plain `datetime-local` strings so callers can store and compare
 * them without a Date round-trip.
 */
export function CompactDateTimeRangePicker(
  props: CompactDateTimeRangePickerProps
) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState<DateTimeRange>({
    start: props.start,
    end: props.end,
  })
  const hasValue = props.start !== '' || props.end !== ''

  const label = useMemo(() => {
    if (!hasValue) return props.placeholder ?? t('Date range')
    return `${show(props.start)} ~ ${show(props.end)}`
  }, [hasValue, props.end, props.placeholder, props.start, t])

  const handleOpenChange = (next: boolean) => {
    if (next) setDraft({ start: props.start, end: props.end })
    setOpen(next)
  }

  const apply = (range: DateTimeRange) => {
    props.onChange(range)
    setOpen(false)
  }

  const presets: Array<{ id: Preset; label: string }> = [
    { id: 'today', label: t('Today') },
    { id: '7d', label: t('7 days') },
    { id: 'week', label: t('This week') },
    { id: '30d', label: t('30 days') },
    { id: 'month', label: t('This month') },
  ]

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger
        render={
          <Button
            type='button'
            variant='outline'
            aria-label={label}
            className={cn(
              'w-full justify-start gap-2 px-2.5 text-sm leading-5 font-normal tabular-nums',
              !hasValue && 'text-muted-foreground',
              props.className
            )}
          />
        }
      >
        <CalendarDays className='text-muted-foreground size-4 shrink-0' />
        <span className='min-w-0 truncate'>{label}</span>
      </PopoverTrigger>
      <PopoverContent
        align='start'
        className='w-[min(520px,calc(100vw-2rem))] p-3'
      >
        <div className='space-y-3'>
          <div className='grid gap-2 sm:grid-cols-[1fr_auto_1fr] sm:items-end'>
            <div className='space-y-1.5'>
              <div className='text-muted-foreground text-xs'>
                {t('Start time')}
              </div>
              <Input
                type='datetime-local'
                value={draft.start}
                aria-label={t('Start time')}
                onChange={(e) =>
                  setDraft((d) => ({ ...d, start: e.target.value }))
                }
                className='h-8 text-sm leading-5 tabular-nums'
              />
            </div>
            <span className='text-muted-foreground hidden pb-2 text-xs sm:block'>
              ~
            </span>
            <div className='space-y-1.5'>
              <div className='text-muted-foreground text-xs'>
                {t('End time')}
              </div>
              <Input
                type='datetime-local'
                value={draft.end}
                aria-label={t('End time')}
                onChange={(e) =>
                  setDraft((d) => ({ ...d, end: e.target.value }))
                }
                className='h-8 text-sm leading-5 tabular-nums'
              />
            </div>
          </div>

          <div className='flex flex-wrap gap-1.5'>
            {presets.map((p) => (
              <Button
                key={p.id}
                type='button'
                variant='secondary'
                size='sm'
                className='h-7 flex-1 px-2 text-xs'
                onClick={() => apply(presetRange(p.id))}
              >
                {p.label}
              </Button>
            ))}
          </div>

          <div className='flex justify-between gap-2'>
            <Button
              type='button'
              variant='ghost'
              size='sm'
              className='h-8'
              onClick={() => apply({ start: '', end: '' })}
            >
              {t('Clear')}
            </Button>
            <Button
              type='button'
              size='sm'
              className='h-8'
              onClick={() => apply(draft)}
            >
              {t('Confirm')}
            </Button>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  )
}
