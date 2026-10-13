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
import type { Table } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'

import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'
import {
  LogsFilterField,
  LogsFilterInput,
  LogsFilterToolbar,
} from '@/features/usage-logs/components/logs-filter-toolbar'

import {
  TASK_STATUS_FILTERS,
  taskStatus,
  type MjFilters,
  type TaskFilters,
} from '../lib/tasks'

const ALL = '__all__'

interface BarProps<TData, F> {
  table: Table<TData>
  draft: F
  applied: F
  onChange: (patch: Partial<F>) => void
  onSearch: () => void
  onReset: () => void
  searching: boolean
}

function anySet(values: string[]): number {
  return values.filter((v) => v.trim() !== '').length
}

export function TaskFilterBar<TData>(props: BarProps<TData, TaskFilters>) {
  const { t } = useTranslation()
  const { draft } = props
  const onEnter = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') props.onSearch()
  }
  const statusItems = [
    { value: ALL, label: t('All statuses') },
    ...TASK_STATUS_FILTERS.map((s) => ({
      value: s,
      label: t(taskStatus(s).label),
    })),
  ]
  const selectedStatus = statusItems.find(
    (s) => s.value === (draft.status || ALL)
  )

  const dateRange = (
    <LogsFilterField wide>
      <CompactDateTimeRangePicker
        start={draft.start}
        end={draft.end}
        placeholder={t('All time')}
        onChange={({ start, end }) => props.onChange({ start, end })}
      />
    </LogsFilterField>
  )
  const taskId = (
    <LogsFilterField>
      <LogsFilterInput
        aria-label={t('Task ID')}
        placeholder={t('Filter by task ID')}
        value={draft.taskId}
        onChange={(e) => props.onChange({ taskId: e.target.value })}
        onKeyDown={onEnter}
      />
    </LogsFilterField>
  )
  const status = (
    <LogsFilterField>
      <Select
        items={statusItems}
        value={draft.status || ALL}
        onValueChange={(v) =>
          props.onChange({ status: v === null || v === ALL ? '' : v })
        }
      >
        <SelectTrigger aria-label={t('Status')}>
          <SelectValue className='min-w-0'>
            <span className='truncate'>
              {selectedStatus?.label ?? t('All statuses')}
            </span>
          </SelectValue>
        </SelectTrigger>
        <SelectContent
          alignItemWithTrigger={false}
          className='max-w-[calc(100vw-2rem)] min-w-40'
        >
          <SelectGroup>
            {statusItems.map((s) => (
              <SelectItem key={s.value} value={s.value}>
                {s.label}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
    </LogsFilterField>
  )
  const platform = (
    <LogsFilterField>
      <LogsFilterInput
        aria-label={t('Platform')}
        placeholder={t('Platform')}
        value={draft.platform}
        onChange={(e) => props.onChange({ platform: e.target.value })}
        onKeyDown={onEnter}
      />
    </LogsFilterField>
  )
  const action = (
    <LogsFilterField>
      <LogsFilterInput
        aria-label={t('Action')}
        placeholder={t('Action')}
        value={draft.action}
        onChange={(e) => props.onChange({ action: e.target.value })}
        onKeyDown={onEnter}
      />
    </LogsFilterField>
  )

  const advanced = anySet([draft.platform, draft.action])
  const total =
    advanced + anySet([draft.taskId, draft.status, draft.start, draft.end])
  const appliedTotal = anySet([
    props.applied.taskId,
    props.applied.status,
    props.applied.start,
    props.applied.end,
    props.applied.platform,
    props.applied.action,
  ])
  return (
    <LogsFilterToolbar
      table={props.table}
      primaryFilters={
        <>
          {dateRange}
          {taskId}
          {status}
        </>
      }
      advancedFilters={
        <>
          {platform}
          {action}
        </>
      }
      mobilePinnedFilters={dateRange}
      mobileFilters={
        <>
          {taskId}
          {status}
          {platform}
          {action}
        </>
      }
      mobileFilterCount={
        anySet([draft.taskId, draft.status]) + advanced
      }
      hasAdvancedActiveFilters={advanced > 0}
      advancedFilterCount={advanced}
      hasActiveFilters={total + appliedTotal > 0}
      onSearch={props.onSearch}
      searchLoading={props.searching}
      onReset={props.onReset}
    />
  )
}

export function MjFilterBar<TData>(props: BarProps<TData, MjFilters>) {
  const { t } = useTranslation()
  const { draft } = props
  const dateRange = (
    <LogsFilterField wide>
      <CompactDateTimeRangePicker
        start={draft.start}
        end={draft.end}
        placeholder={t('All time')}
        onChange={({ start, end }) => props.onChange({ start, end })}
      />
    </LogsFilterField>
  )
  const mjId = (
    <LogsFilterField>
      <LogsFilterInput
        aria-label={t('Task ID')}
        placeholder={t('Filter by task ID')}
        value={draft.mjId}
        onChange={(e) => props.onChange({ mjId: e.target.value })}
        onKeyDown={(e) => {
          if (e.key === 'Enter') props.onSearch()
        }}
      />
    </LogsFilterField>
  )
  return (
    <LogsFilterToolbar
      table={props.table}
      primaryFilters={
        <>
          {dateRange}
          {mjId}
        </>
      }
      mobilePinnedFilters={dateRange}
      mobileFilters={mjId}
      mobileFilterCount={anySet([draft.mjId])}
      hasActiveFilters={
        anySet([draft.mjId, draft.start, draft.end]) +
          anySet([props.applied.mjId, props.applied.start, props.applied.end]) >
        0
      }
      onSearch={props.onSearch}
      searchLoading={props.searching}
      onReset={props.onReset}
    />
  )
}
