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

type EditorProps = {
  value: string
  onChange: (value: string) => void
  disabled: boolean
}

function parseObject(value: string): Record<string, unknown> {
  const parsed = safeJsonParse<Record<string, unknown>>(value, {
    fallback: {},
    silent: true,
  })
  return parsed && typeof parsed === 'object' && !Array.isArray(parsed)
    ? parsed
    : {}
}

type RatioEntry = { name: string; ratio: number }

/** GroupRatio: {group: multiplier applied to that group's usage}. */
export function GroupRatioEditor({ value, onChange, disabled }: EditorProps) {
  const rows = useMemo<RatioEntry[]>(
    () =>
      Object.entries(parseObject(value))
        .filter(([, ratio]) => typeof ratio === 'number')
        .map(([name, ratio]) => ({ name, ratio: ratio as number })),
    [value]
  )
  return (
    <RowsEditor<RatioEntry>
      rows={rows}
      rowKey={(row) => row.name}
      disabled={disabled}
      onChange={(next) =>
        onChange(
          JSON.stringify(
            Object.fromEntries(next.map((row) => [row.name, row.ratio])),
            null,
            2
          )
        )
      }
      searchText={(row) => row.name}
      columns={[
        {
          id: 'group',
          header: 'Group Name',
          cellClassName: 'font-medium',
          cell: (row) => row.name,
        },
        {
          id: 'ratio',
          header: 'Ratio',
          className: 'text-right',
          cellClassName: 'text-right font-mono',
          cell: (row) => `×${row.ratio}`,
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
        },
        {
          id: 'ratio',
          label: 'Ratio',
          kind: 'number',
          min: 0,
          description:
            'Multiplier applied to everything this group uses. 1 = list price.',
        },
      ]}
      toDraft={(row) => ({ name: row.name, ratio: String(row.ratio) })}
      fromDraft={(draft) => ({
        name: (draft.name ?? '').trim(),
        ratio: Number(draft.ratio),
      })}
      labels={{
        add: 'Add group',
        empty: 'No groups configured. Use Add group to get started.',
        emptySearch: 'No groups match your search',
        dialogAdd: 'Add group ratio',
        dialogEdit: 'Edit group ratio',
        dialogDescription: 'Set the price multiplier for one user group.',
        searchPlaceholder: 'Search group names...',
      }}
    />
  )
}

type UsableEntry = { name: string; description: string }

/** UserUsableGroups: {group: description} people may pick for a key. */
export function UsableGroupsEditor({ value, onChange, disabled }: EditorProps) {
  const rows = useMemo<UsableEntry[]>(
    () =>
      Object.entries(parseObject(value))
        .filter(([, description]) => typeof description === 'string')
        .map(([name, description]) => ({
          name,
          description: description as string,
        })),
    [value]
  )
  return (
    <RowsEditor<UsableEntry>
      rows={rows}
      rowKey={(row) => row.name}
      disabled={disabled}
      onChange={(next) =>
        onChange(
          JSON.stringify(
            Object.fromEntries(next.map((row) => [row.name, row.description])),
            null,
            2
          )
        )
      }
      searchText={(row) => `${row.name} ${row.description}`}
      columns={[
        {
          id: 'group',
          header: 'Group Name',
          cellClassName: 'font-medium',
          cell: (row) => row.name,
        },
        {
          id: 'description',
          header: 'Description',
          cell: (row) => row.description,
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
        },
        {
          id: 'description',
          label: 'Description',
          kind: 'text',
          required: true,
          description: 'Shown to people when they choose a group.',
        },
      ]}
      toDraft={(row) => ({ name: row.name, description: row.description })}
      fromDraft={(draft) => ({
        name: (draft.name ?? '').trim(),
        description: (draft.description ?? '').trim(),
      })}
      labels={{
        add: 'Add group',
        empty: 'No selectable groups configured. Use Add group to get started.',
        emptySearch: 'No groups match your search',
        dialogAdd: 'Add selectable group',
        dialogEdit: 'Edit selectable group',
        dialogDescription: 'Choose which groups a person can attach to a key.',
        searchPlaceholder: 'Search group names...',
      }}
    />
  )
}

type PairEntry = { userGroup: string; usingGroup: string; ratio: number }

function pairKey(row: PairEntry): string {
  return `${row.userGroup} / ${row.usingGroup}`
}

/** GroupGroupRatio: {userGroup: {usingGroup: multiplier}} overrides. */
export function GroupGroupRatioEditor({
  value,
  onChange,
  disabled,
}: EditorProps) {
  const rows = useMemo<PairEntry[]>(() => {
    const out: PairEntry[] = []
    for (const [userGroup, inner] of Object.entries(parseObject(value))) {
      if (typeof inner !== 'object' || inner === null) continue
      for (const [usingGroup, ratio] of Object.entries(inner)) {
        if (typeof ratio === 'number') out.push({ userGroup, usingGroup, ratio })
      }
    }
    return out
  }, [value])
  return (
    <RowsEditor<PairEntry>
      rows={rows}
      rowKey={pairKey}
      disabled={disabled}
      onChange={(next) => {
        const body: Record<string, Record<string, number>> = {}
        for (const row of next) {
          body[row.userGroup] = {
            ...body[row.userGroup],
            [row.usingGroup]: row.ratio,
          }
        }
        onChange(JSON.stringify(body, null, 2))
      }}
      searchText={(row) => `${row.userGroup} ${row.usingGroup}`}
      columns={[
        {
          id: 'user-group',
          header: 'User group',
          cellClassName: 'font-medium',
          cell: (row) => row.userGroup,
        },
        {
          id: 'using-group',
          header: 'Using group',
          cell: (row) => row.usingGroup,
        },
        {
          id: 'ratio',
          header: 'Ratio',
          className: 'text-right',
          cellClassName: 'text-right font-mono',
          cell: (row) => `×${row.ratio}`,
        },
      ]}
      fields={[
        {
          id: 'userGroup',
          label: 'User group',
          kind: 'text',
          required: true,
          identity: true,
          placeholder: 'vip',
        },
        {
          id: 'usingGroup',
          label: 'Using group',
          kind: 'text',
          required: true,
          identity: true,
          placeholder: 'default',
          description: 'The group of the key or channel being used.',
        },
        {
          id: 'ratio',
          label: 'Ratio',
          kind: 'number',
          min: 0,
          description:
            'Replaces the using group ratio for people in the user group.',
        },
      ]}
      toDraft={(row) => ({
        userGroup: row.userGroup,
        usingGroup: row.usingGroup,
        ratio: String(row.ratio),
      })}
      fromDraft={(draft) => ({
        userGroup: (draft.userGroup ?? '').trim(),
        usingGroup: (draft.usingGroup ?? '').trim(),
        ratio: Number(draft.ratio),
      })}
      labels={{
        add: 'Add override',
        empty: 'No group overrides configured. Use Add override to get started.',
        emptySearch: 'No overrides match your search',
        dialogAdd: 'Add group override',
        dialogEdit: 'Edit group override',
        dialogDescription:
          'Give people in one group a different multiplier for another group.',
        searchPlaceholder: 'Search groups...',
      }}
    />
  )
}
