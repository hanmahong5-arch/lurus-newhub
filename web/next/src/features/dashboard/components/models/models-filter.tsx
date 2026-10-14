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
import { Calendar, Filter, RotateCcw, Search } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DateTimePicker } from '@/components/datetime-picker'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type { TimeGranularity } from '@/lib/time'
import { cn } from '@/lib/utils'

import { TIME_GRANULARITY_OPTIONS, TIME_RANGE_PRESETS } from '../../lib/constants'
import {
  buildDefaultFilters,
  detectPresetDays,
  granularityForRangeDays,
  rangeProblem,
  resolveRange,
  type RangeProblem,
} from '../../lib/filters'
import type {
  DashboardChartPreferences,
  DashboardFilters,
} from '../../lib/types'

function SectionDivider(props: { label: string }) {
  return (
    <div className='relative'>
      <div className='absolute inset-0 flex items-center'>
        <span className='w-full border-t' />
      </div>
      <div className='relative flex justify-center text-xs uppercase'>
        <span className='bg-popover text-muted-foreground px-2'>
          {props.label}
        </span>
      </div>
    </div>
  )
}

const PROBLEM_TEXT: Record<RangeProblem, string> = {
  incomplete: 'Pick both a start and an end time.',
  order: 'The end time must be after the start time.',
  'too-long': 'The range can be at most 30 days.',
}

export interface ModelsFilterProps {
  preferences: DashboardChartPreferences
  /** The filters currently applied; the dialog edits a copy of them. */
  currentFilters: DashboardFilters
  onApply: (filters: DashboardFilters) => void
  onReset: () => void
}

/** Time range + granularity dialog (donor ModelsFilter, minus admin fields). */
export function ModelsFilter(props: ModelsFilterProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState<DashboardFilters>(props.currentFilters)

  const handleOpenChange = (next: boolean) => {
    // Re-seed from the applied filters on every open so a manual range sticks.
    if (next) setDraft(props.currentFilters)
    setOpen(next)
  }

  const custom = draft.days == null
  const problem = custom ? rangeProblem(draft.start, draft.end) : null
  const selectedDays = detectPresetDays(draft)
  const [shownStart, shownEnd] = useMemo(() => {
    if (draft.days == null) return [draft.start, draft.end]
    const r = resolveRange(draft)
    return [new Date(r.start * 1000), new Date(r.end * 1000)]
  }, [draft])

  const pickPreset = (days: number) =>
    setDraft({
      days,
      granularity: granularityForRangeDays(days),
    })

  const pickDate = (field: 'start' | 'end', value: Date | undefined) =>
    setDraft((prev) => {
      // Leaving a preset keeps its concrete span as the custom starting point.
      const r = prev.days != null ? resolveRange(prev) : null
      const start = r ? new Date(r.start * 1000) : prev.start
      const end = r ? new Date(r.end * 1000) : prev.end
      return {
        days: null,
        granularity: prev.granularity,
        start: field === 'start' ? value : start,
        end: field === 'end' ? value : end,
      }
    })

  const apply = () => {
    if (problem) return
    props.onApply(draft)
    setOpen(false)
  }

  const reset = () => {
    props.onReset()
    setDraft(buildDefaultFilters(props.preferences))
    setOpen(false)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={handleOpenChange}
      trigger={
        <Button variant='outline' size='sm'>
          <Filter className='me-2 size-4' />
          {t('Filter')}
        </Button>
      }
      title={t('Model Analytics Filters')}
      description={t('Filter the model analytics view by time range.')}
      contentClassName='max-sm:h-dvh max-sm:w-screen max-sm:max-w-none max-sm:rounded-none max-sm:p-4 sm:max-w-lg'
      contentHeight='min(48vh, 460px)'
      footerClassName='grid grid-cols-2 gap-2 sm:flex'
      footer={
        <>
          <Button onClick={reset} variant='outline' type='button'>
            <RotateCcw className='me-2 size-4' />
            {t('Reset')}
          </Button>
          <Button onClick={apply} type='button' disabled={problem != null}>
            <Search className='me-2 size-4' />
            {t('Apply Filters')}
          </Button>
        </>
      }
    >
      <ScrollArea className='h-full pe-3 sm:pe-4'>
        <div className='grid gap-2.5 py-2'>
          <div className='grid gap-2'>
            <Label className='flex items-center gap-2'>
              <Calendar className='size-4' />
              {t('Quick Range')}
            </Label>
            <div className='grid grid-cols-2 gap-2 sm:flex'>
              {TIME_RANGE_PRESETS.map((range) => (
                <Button
                  key={range.days}
                  type='button'
                  size='sm'
                  variant={selectedDays === range.days ? 'default' : 'outline'}
                  aria-pressed={selectedDays === range.days}
                  onClick={() => pickPreset(range.days)}
                  className={cn(
                    'flex-1',
                    selectedDays === range.days &&
                      'ring-ring ring-2 ring-offset-2'
                  )}
                >
                  {t(range.label)}
                </Button>
              ))}
            </div>
          </div>

          <SectionDivider label={t('Custom Time Range')} />

          <div className='grid gap-2.5'>
            <div className='grid gap-2'>
              <Label>{t('Start Time')}</Label>
              <DateTimePicker
                value={shownStart}
                onChange={(d) => pickDate('start', d)}
                placeholder={t('Select start time')}
              />
            </div>
            <div className='grid gap-2'>
              <Label>{t('End Time')}</Label>
              <DateTimePicker
                value={shownEnd}
                onChange={(d) => pickDate('end', d)}
                placeholder={t('Select end time')}
              />
            </div>
            {problem != null && custom && (draft.start || draft.end) && (
              <p
                role='alert'
                data-testid='filter-range-problem'
                className='text-destructive text-xs'
              >
                {t(PROBLEM_TEXT[problem])}
              </p>
            )}
          </div>

          <SectionDivider label={t('Chart Settings')} />

          <div className='grid gap-2'>
            <Label>{t('Time Granularity')}</Label>
            <Select
              items={TIME_GRANULARITY_OPTIONS.map((o) => ({
                value: o.value,
                label: t(o.label),
              }))}
              value={draft.granularity}
              onValueChange={(value) =>
                setDraft((prev) => ({
                  ...prev,
                  granularity: value as TimeGranularity,
                }))
              }
            >
              <SelectTrigger aria-label={t('Time Granularity')}>
                <SelectValue placeholder={t('Select time granularity')} />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {TIME_GRANULARITY_OPTIONS.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {t(o.label)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
        </div>
      </ScrollArea>
    </Dialog>
  )
}
