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
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import type { AuditFilters, AuditScope } from '../lib/audit'
import { CompactDateTimeRangePicker } from './compact-date-time-range-picker'
import {
  LogsFilterField,
  LogsFilterInput,
  LogsFilterToolbar,
} from './logs-filter-toolbar'

interface AuditFilterBarProps<TData> {
  table: Table<TData>
  scope: AuditScope
  draft: AuditFilters
  applied: AuditFilters
  /** Known action names (platform feed); suggestions only. */
  actions: string[]
  onChange: (patch: Partial<AuditFilters>) => void
  onSearch: () => void
  onReset: () => void
  searching: boolean
  actionStart?: React.ReactNode
}

function activeCount(f: AuditFilters): number {
  return [
    f.action.trim(),
    f.actorId.trim(),
    f.resource.trim(),
    f.start,
    f.end,
  ].filter(Boolean).length
}

export function AuditFilterBar<TData>(props: AuditFilterBarProps<TData>) {
  const { t } = useTranslation()
  const listId = useId()
  const { draft } = props
  const onEnter = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') props.onSearch()
  }

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
  const action = (
    <LogsFilterField>
      <LogsFilterInput
        aria-label={t('Action')}
        placeholder={t('Action')}
        list={props.actions.length > 0 ? listId : undefined}
        value={draft.action}
        className='font-mono'
        onChange={(e) => props.onChange({ action: e.target.value })}
        onKeyDown={onEnter}
      />
      {props.actions.length > 0 && (
        <datalist id={listId}>
          {props.actions.map((a) => (
            <option key={a} value={a} />
          ))}
        </datalist>
      )}
    </LogsFilterField>
  )
  const actor = (
    <LogsFilterField>
      <LogsFilterInput
        aria-label={t('Operator (user id)')}
        placeholder={t('Operator (user id)')}
        inputMode='numeric'
        value={draft.actorId}
        onChange={(e) => props.onChange({ actorId: e.target.value })}
        onKeyDown={onEnter}
      />
    </LogsFilterField>
  )
  const resource =
    props.scope === 'platform' ? (
      <LogsFilterField>
        <LogsFilterInput
          aria-label={t('Resource')}
          placeholder={t('Resource')}
          value={draft.resource}
          onChange={(e) => props.onChange({ resource: e.target.value })}
          onKeyDown={onEnter}
        />
      </LogsFilterField>
    ) : null

  return (
    <LogsFilterToolbar
      table={props.table}
      primaryFilters={
        <>
          {dateRange}
          {action}
          {actor}
          {resource}
        </>
      }
      mobilePinnedFilters={dateRange}
      mobileFilters={
        <>
          {action}
          {actor}
          {resource}
        </>
      }
      mobileFilterCount={
        [draft.action, draft.actorId, draft.resource].filter((v) => v.trim())
          .length
      }
      hasActiveFilters={activeCount(draft) + activeCount(props.applied) > 0}
      onSearch={props.onSearch}
      searchLoading={props.searching}
      onReset={props.onReset}
      actionStart={props.actionStart}
    />
  )
}
