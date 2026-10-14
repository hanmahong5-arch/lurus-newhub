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

import { LOG_TYPE_FILTERS } from '../lib/log-view'
import type { LogFilters } from '../lib/logs'
import { CompactDateTimeRangePicker } from './compact-date-time-range-picker'
import {
  LogsFilterField,
  LogsFilterInput,
  LogsFilterToolbar,
} from './logs-filter-toolbar'

const ALL_TYPES = '0'

interface CommonLogsFilterBarProps<TData> {
  table: Table<TData>
  /** What the inputs show. */
  draft: LogFilters
  /** What the queries last ran with (drives the "can reset" state). */
  applied: LogFilters
  onChange: (patch: Partial<LogFilters>) => void
  onSearch: () => void
  onReset: () => void
  searching: boolean
  /** Extra controls placed before Reset (export, ...). */
  actionStart?: React.ReactNode
}

function countActive(f: LogFilters): { primary: number; advanced: number } {
  const advanced = [f.token, f.requestId, f.sessionId, f.username].filter(
    (v) => v.trim() !== ''
  ).length
  const primary = [
    f.start !== '' || f.end !== '',
    f.model.trim() !== '',
    f.logType > 0,
  ].filter(Boolean).length
  return { primary, advanced }
}

export function CommonLogsFilterBar<TData>(
  props: CommonLogsFilterBarProps<TData>
) {
  const { t } = useTranslation()
  const { draft } = props
  const onEnter = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') props.onSearch()
  }

  const typeItems = [
    { value: ALL_TYPES, label: t('All types') },
    ...LOG_TYPE_FILTERS.map((x) => ({
      value: String(x.value),
      label: t(x.label),
    })),
  ]
  const selectedType = typeItems.find(
    (x) => x.value === String(draft.logType)
  )

  const dateRange = (
    <LogsFilterField wide>
      <CompactDateTimeRangePicker
        start={draft.start}
        end={draft.end}
        placeholder={t('Last 7 days')}
        onChange={({ start, end }) => props.onChange({ start, end })}
      />
    </LogsFilterField>
  )
  const model = (
    <LogsFilterField>
      <LogsFilterInput
        aria-label={t('Model')}
        placeholder={t('Model')}
        value={draft.model}
        onChange={(e) => props.onChange({ model: e.target.value })}
        onKeyDown={onEnter}
      />
    </LogsFilterField>
  )
  const type = (
    <LogsFilterField>
      <Select
        items={typeItems}
        value={String(draft.logType)}
        onValueChange={(v) =>
          props.onChange({ logType: v === null ? 0 : Number(v) || 0 })
        }
      >
        <SelectTrigger aria-label={t('Type')}>
          <SelectValue className='min-w-0'>
            <span className='truncate'>
              {selectedType?.label ?? t('All types')}
            </span>
          </SelectValue>
        </SelectTrigger>
        <SelectContent
          alignItemWithTrigger={false}
          className='max-w-[calc(100vw-2rem)] min-w-40'
        >
          <SelectGroup>
            {typeItems.map((x) => (
              <SelectItem key={x.value} value={x.value}>
                {x.label}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
    </LogsFilterField>
  )
  const advanced = (
    <>
      <LogsFilterField>
        <LogsFilterInput
          aria-label={t('Token')}
          placeholder={t('Token name')}
          value={draft.token}
          onChange={(e) => props.onChange({ token: e.target.value })}
          onKeyDown={onEnter}
        />
      </LogsFilterField>
      {draft.tenantWide && (
        <LogsFilterField>
          <LogsFilterInput
            aria-label={t('Username')}
            placeholder={t('Username')}
            value={draft.username}
            onChange={(e) => props.onChange({ username: e.target.value })}
            onKeyDown={onEnter}
          />
        </LogsFilterField>
      )}
      <LogsFilterField>
        <LogsFilterInput
          aria-label={t('Request ID')}
          placeholder={t('Request ID')}
          value={draft.requestId}
          onChange={(e) => props.onChange({ requestId: e.target.value })}
          onKeyDown={onEnter}
        />
      </LogsFilterField>
      <LogsFilterField>
        <LogsFilterInput
          aria-label={t('Session ID')}
          placeholder={t('Session ID')}
          value={draft.sessionId}
          onChange={(e) => props.onChange({ sessionId: e.target.value })}
          onKeyDown={onEnter}
        />
      </LogsFilterField>
    </>
  )

  const counts = countActive(draft)
  const dateSet = draft.start !== '' || draft.end !== ''
  const appliedCounts = countActive(props.applied)
  return (
    <LogsFilterToolbar
      table={props.table}
      primaryFilters={
        <>
          {dateRange}
          {model}
          {type}
        </>
      }
      advancedFilters={advanced}
      mobilePinnedFilters={dateRange}
      mobileFilters={
        <>
          {model}
          {type}
          {advanced}
        </>
      }
      mobileFilterCount={counts.primary - (dateSet ? 1 : 0) + counts.advanced}
      hasAdvancedActiveFilters={counts.advanced > 0}
      advancedFilterCount={counts.advanced}
      hasActiveFilters={
        counts.primary + counts.advanced > 0 ||
        appliedCounts.primary + appliedCounts.advanced > 0
      }
      onSearch={props.onSearch}
      searchLoading={props.searching}
      onReset={props.onReset}
      actionStart={props.actionStart}
    />
  )
}
