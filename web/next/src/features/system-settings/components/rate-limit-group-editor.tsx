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
import { useMemo } from 'react'

import { safeJsonParse } from '../utils/json-parser'
import { RowsEditor } from './rows-editor'

type Entry = { name: string; maxRequests: number; maxSuccess: number }

function parseEntries(value: string): Entry[] {
  const parsed = safeJsonParse<Record<string, unknown>>(value, {
    fallback: {},
    silent: true,
  })
  const out: Entry[] = []
  for (const [name, limits] of Object.entries(parsed ?? {})) {
    if (
      Array.isArray(limits) &&
      limits.length === 2 &&
      typeof limits[0] === 'number' &&
      typeof limits[1] === 'number'
    ) {
      out.push({ name, maxRequests: limits[0], maxSuccess: limits[1] })
    }
  }
  return out
}

function serialize(entries: Entry[]): string {
  const body: Record<string, [number, number]> = {}
  for (const entry of entries) {
    body[entry.name] = [entry.maxRequests, entry.maxSuccess]
  }
  return JSON.stringify(body, null, 2)
}

type Props = {
  value: string
  onChange: (value: string) => void
  disabled: boolean
}

/** Visual editor for ModelRequestRateLimitGroup ({group: [requests, success]}). */
export function RateLimitGroupEditor({ value, onChange, disabled }: Props) {
  const rows = useMemo(() => parseEntries(value), [value])
  return (
    <RowsEditor<Entry>
      rows={rows}
      rowKey={(row) => row.name}
      disabled={disabled}
      onChange={(next) => onChange(serialize(next))}
      searchText={(row) => row.name}
      columns={[
        {
          id: 'group',
          header: 'Group Name',
          cellClassName: 'font-medium',
          cell: (row) => row.name,
        },
        {
          id: 'max-requests',
          header: 'Max Requests (incl. failures)',
          className: 'text-right',
          cellClassName: 'text-right font-mono',
          cell: (row) =>
            row.maxRequests === 0 ? '∞' : row.maxRequests.toLocaleString(),
        },
        {
          id: 'max-success',
          header: 'Max Success',
          className: 'text-right',
          cellClassName: 'text-right font-mono',
          cell: (row) => row.maxSuccess.toLocaleString(),
        },
      ]}
      fields={[
        {
          id: 'name',
          label: 'Group Name',
          kind: 'text',
          required: true,
          identity: true,
          placeholder: 'default',
          description: 'Unique identifier for this group.',
        },
        {
          id: 'maxRequests',
          label: 'Max Requests (including failures)',
          kind: 'number',
          integer: true,
          min: 0,
          max: 2147483647,
          description: 'Total requests allowed per period. 0 = unlimited.',
        },
        {
          id: 'maxSuccess',
          label: 'Max Successful Requests',
          kind: 'number',
          integer: true,
          min: 1,
          max: 2147483647,
          description: 'Only successful requests count toward this limit.',
        },
      ]}
      toDraft={(row) => ({
        name: row.name,
        maxRequests: String(row.maxRequests),
        maxSuccess: String(row.maxSuccess),
      })}
      fromDraft={(draft) => ({
        name: (draft.name ?? '').trim(),
        maxRequests: Number(draft.maxRequests),
        maxSuccess: Number(draft.maxSuccess),
      })}
      labels={{
        add: 'Add group',
        empty:
          'No group-based rate limits configured. Use Add group to get started.',
        emptySearch: 'No groups match your search',
        dialogAdd: 'Add group rate limit',
        dialogEdit: 'Edit group rate limit',
        dialogDescription:
          'Configure rate limiting rules for a specific user group.',
        searchPlaceholder: 'Search group names...',
      }}
    />
  )
}
