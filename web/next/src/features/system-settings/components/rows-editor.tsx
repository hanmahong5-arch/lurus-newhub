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
import { Pencil, Plus, Search, Trash2 } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

export type RowsEditorField = {
  id: string
  label: string
  kind: 'text' | 'number'
  required?: boolean
  integer?: boolean
  min?: number
  max?: number
  positive?: boolean
  placeholder?: string
  description?: string
  /** Part of the row identity: read-only while editing an existing row. */
  identity?: boolean
}

export type RowsEditorColumn<Row> = {
  id: string
  header: string
  className?: string
  cellClassName?: string
  cell: (row: Row) => ReactNode
}

export type RowsEditorLabels = {
  add: string
  empty: string
  emptySearch: string
  dialogAdd: string
  dialogEdit: string
  dialogDescription: string
  searchPlaceholder: string
}

type Draft = Record<string, string>

type RowsEditorProps<Row> = {
  rows: Row[]
  rowKey: (row: Row) => string
  columns: RowsEditorColumn<Row>[]
  fields: RowsEditorField[]
  toDraft: (row: Row) => Draft
  fromDraft: (draft: Draft) => Row
  /** Called with the complete new row list. */
  onChange: (rows: Row[]) => void
  searchText: (row: Row) => string
  labels: RowsEditorLabels
  disabled?: boolean
  /** Return an English message to refuse the draft, or null. */
  extraValidate?: (draft: Draft) => string | null
  /** Extra toolbar content next to the add button. */
  toolbar?: ReactNode
}

function numberStep(field: RowsEditorField): number | 'any' | undefined {
  if (field.kind !== 'number') return undefined
  return field.integer ? 1 : 'any'
}

function validateDraftField(field: RowsEditorField, value: string) {
  const text = value.trim()
  if (field.kind === 'text') {
    if (field.required && text === '') return 'This field is required'
    return null
  }
  if (text === '') return field.required === false ? null : 'Enter a number'
  const n = Number(text)
  if (!Number.isFinite(n)) return 'Please enter a valid number'
  if (field.integer && !Number.isInteger(n)) return 'Enter a whole number'
  if (field.positive && n <= 0) return 'Must be greater than 0'
  if (field.min !== undefined && n < field.min) {
    return 'Value is below the minimum'
  }
  if (field.max !== undefined && n > field.max) {
    return 'Value is above the maximum'
  }
  return null
}

/**
 * Search + table + add/edit dialog over a list of keyed rows. The owner keeps
 * the rows in a JSON option; this component only edits the list and reports
 * the whole new list through onChange.
 */
export function RowsEditor<Row>({
  rows,
  rowKey,
  columns,
  fields,
  toDraft,
  fromDraft,
  onChange,
  searchText,
  labels,
  disabled,
  extraValidate,
  toolbar,
}: RowsEditorProps<Row>) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Row | null>(null)
  const [draft, setDraft] = useState<Draft>({})
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [formError, setFormError] = useState<string | null>(null)

  const visibleRows = useMemo(() => {
    const needle = query.trim().toLowerCase()
    if (!needle) return rows
    return rows.filter((row) => searchText(row).toLowerCase().includes(needle))
  }, [rows, query, searchText])

  const openAdd = () => {
    const blank: Draft = {}
    for (const field of fields) blank[field.id] = ''
    setEditing(null)
    setDraft(blank)
    setErrors({})
    setFormError(null)
    setOpen(true)
  }

  const openEdit = (row: Row) => {
    setEditing(row)
    setDraft(toDraft(row))
    setErrors({})
    setFormError(null)
    setOpen(true)
  }

  const submit = () => {
    const found: Record<string, string> = {}
    for (const field of fields) {
      const problem = validateDraftField(field, draft[field.id] ?? '')
      if (problem) found[field.id] = problem
    }
    if (Object.keys(found).length > 0) {
      setErrors(found)
      setFormError(null)
      return
    }
    const next = fromDraft(draft)
    const nextKey = rowKey(next)
    const editingKey = editing ? rowKey(editing) : null
    if (nextKey !== editingKey && rows.some((row) => rowKey(row) === nextKey)) {
      setErrors({})
      setFormError('An entry with this name already exists')
      return
    }
    const extra = extraValidate?.(draft)
    if (extra) {
      setErrors({})
      setFormError(extra)
      return
    }
    onChange(
      editingKey === null
        ? [...rows, next]
        : rows.map((row) => (rowKey(row) === editingKey ? next : row))
    )
    setOpen(false)
  }

  const remove = (row: Row) => {
    const key = rowKey(row)
    onChange(rows.filter((candidate) => rowKey(candidate) !== key))
  }

  const formId = 'rows-editor-form'

  return (
    <div className='flex flex-col gap-3'>
      <div className='flex flex-wrap items-center gap-2'>
        <div className='relative min-w-48 flex-1'>
          <Search className='text-muted-foreground pointer-events-none absolute top-2 left-2.5 size-4' />
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t(labels.searchPlaceholder)}
            aria-label={t(labels.searchPlaceholder)}
            className='pl-8'
          />
        </div>
        {toolbar}
        <Button type='button' onClick={openAdd} disabled={disabled}>
          <Plus data-icon='inline-start' />
          {t(labels.add)}
        </Button>
      </div>

      <StaticDataTable
        data={visibleRows}
        getRowKey={(row) => rowKey(row)}
        emptyContent={t(query.trim() ? labels.emptySearch : labels.empty)}
        columns={[
          ...columns.map((column) => ({
            id: column.id,
            header: t(column.header),
            className: column.className,
            cellClassName: column.cellClassName,
            cell: (row: Row) => column.cell(row),
          })),
          {
            id: 'actions',
            header: <span className='sr-only'>{t('Actions')}</span>,
            className: 'w-24 text-right',
            cellClassName: 'text-right',
            cell: (row: Row) => (
              <div className='flex justify-end gap-1'>
                <Button
                  type='button'
                  variant='ghost'
                  size='icon-sm'
                  disabled={disabled}
                  aria-label={t('Edit {{name}}', { name: rowKey(row) })}
                  onClick={() => openEdit(row)}
                >
                  <Pencil />
                </Button>
                <Button
                  type='button'
                  variant='ghost'
                  size='icon-sm'
                  disabled={disabled}
                  aria-label={t('Delete {{name}}', { name: rowKey(row) })}
                  className='text-destructive hover:text-destructive'
                  onClick={() => remove(row)}
                >
                  <Trash2 />
                </Button>
              </div>
            ),
          },
        ]}
      />

      <Dialog
        open={open}
        onOpenChange={setOpen}
        title={t(editing ? labels.dialogEdit : labels.dialogAdd)}
        description={t(labels.dialogDescription)}
        contentClassName='sm:max-w-[480px]'
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              onClick={() => setOpen(false)}
            >
              {t('Cancel')}
            </Button>
            <Button type='submit' form={formId}>
              {editing ? t('Update') : t('Add')}
            </Button>
          </>
        }
      >
        <form
          id={formId}
          className='flex flex-col gap-4'
          onSubmit={(event) => {
            event.preventDefault()
            event.stopPropagation()
            submit()
          }}
        >
          {fields.map((field) => {
            const id = `${formId}-${field.id}`
            const error = errors[field.id]
            return (
              <div key={field.id} className='flex flex-col gap-1.5'>
                <Label htmlFor={id}>{t(field.label)}</Label>
                <Input
                  id={id}
                  type={field.kind === 'number' ? 'number' : 'text'}
                  inputMode={field.kind === 'number' ? 'decimal' : undefined}
                  step={numberStep(field)}
                  value={draft[field.id] ?? ''}
                  disabled={Boolean(editing) && field.identity}
                  placeholder={
                    field.placeholder ? t(field.placeholder) : undefined
                  }
                  aria-invalid={error ? true : undefined}
                  onChange={(event) =>
                    setDraft((current) => ({
                      ...current,
                      [field.id]: event.target.value,
                    }))
                  }
                />
                {field.description ? (
                  <p className='text-muted-foreground text-xs'>
                    {t(field.description)}
                  </p>
                ) : null}
                {error ? (
                  <p role='alert' className='text-destructive text-sm'>
                    {t(error)}
                  </p>
                ) : null}
              </div>
            )
          })}
          {formError ? (
            <p role='alert' className='text-destructive text-sm'>
              {t(formError)}
            </p>
          ) : null}
        </form>
      </Dialog>
    </div>
  )
}
