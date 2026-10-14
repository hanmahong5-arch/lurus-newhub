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
import type {
  ColumnDef,
  ColumnFiltersState,
  Row,
} from '@tanstack/react-table'
import { Pencil, Plus, Trash2 } from 'lucide-react'
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTableColumnHeader,
  DataTablePagination,
  DataTableRow,
  DataTableToolbar,
  DataTableView,
  useDataTable,
} from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useQuota } from '@/hooks/use-quota'

import { useOptionDraft } from '../hooks/use-option-draft'
import {
  applyRow,
  buildRows,
  changedKeys,
  parsePricingMaps,
  removeModel,
  rowSignature,
  serializeNumberMap,
  type ModelPriceRow,
  type PricingMaps,
  type PricingMode,
} from '../lib/pricing'


type TableRowData = ModelPriceRow & { changed: boolean }

const TOKENS_PER_MILLION = 1_000_000

/** Hidden by default: rarely set, and they widen the table past a laptop. */
const INITIAL_VISIBILITY = {
  cache: false,
  image: false,
  audio: false,
  audioCompletion: false,
}

type DraftFormValues = {
  name: string
  mode: PricingMode
  ratio: string
  completion: string
  cache: string
  price: string
  image: string
  audio: string
  audioCompletion: string
}

const BLANK_FORM: DraftFormValues = {
  name: '',
  mode: 'per-token',
  ratio: '',
  completion: '',
  cache: '',
  price: '',
  image: '',
  audio: '',
  audioCompletion: '',
}

const NUMERIC_FORM_FIELDS = [
  'ratio',
  'completion',
  'cache',
  'price',
  'image',
  'audio',
  'audioCompletion',
] as const

function formFromRow(row: ModelPriceRow): DraftFormValues {
  const out: DraftFormValues = { ...BLANK_FORM, name: row.name, mode: row.mode }
  for (const field of NUMERIC_FORM_FIELDS) {
    const value = row[field]
    out[field] = value === undefined ? '' : String(value)
  }
  return out
}

/**
 * Validate the dialog and turn it into a row. Returns per-field English
 * messages; a blank optional field means "not set" (the entry is removed).
 */
function rowFromForm(
  form: DraftFormValues
): { row: ModelPriceRow } | { errors: Record<string, string> } {
  const errors: Record<string, string> = {}
  const name = form.name.trim()
  if (name === '') errors.name = 'This field is required'
  const row: ModelPriceRow = { name, mode: form.mode }
  const active =
    form.mode === 'per-request'
      ? (['price'] as const)
      : ([
          'ratio',
          'completion',
          'cache',
          'image',
          'audio',
          'audioCompletion',
        ] as const)
  for (const field of active) {
    const text = form[field].trim()
    const required =
      (form.mode === 'per-request' && field === 'price') ||
      (form.mode === 'per-token' && field === 'ratio')
    if (text === '') {
      if (required) errors[field] = 'Enter a number'
      continue
    }
    const n = Number(text)
    if (!Number.isFinite(n)) errors[field] = 'Please enter a valid number'
    else if (n < 0) errors[field] = 'Values cannot be negative'
    else row[field] = n
  }
  return Object.keys(errors).length > 0 ? { errors } : { row }
}

export function ModelPriceTable() {
  const { t } = useTranslation()
  const draft = useOptionDraft()
  const { formatQuota, ready, config } = useQuota()

  const maps = useMemo(
    () => parsePricingMaps((key) => draft.value(key)),
    // draft.value changes identity whenever any edit changes
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [draft.value]
  )
  const savedMaps = useMemo(
    () => parsePricingMaps((key) => draft.saved(key)),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [draft.saved]
  )

  const savedSignatures = useMemo(() => {
    const out = new Map<string, string>()
    for (const row of buildRows(savedMaps)) out.set(row.name, rowSignature(row))
    return out
  }, [savedMaps])

  const rows: TableRowData[] = useMemo(
    () =>
      buildRows(maps).map((row) => ({
        ...row,
        changed: savedSignatures.get(row.name) !== rowSignature(row),
      })),
    [maps, savedSignatures]
  )

  const removedCount = useMemo(() => {
    const present = new Set(rows.map((row) => row.name))
    let n = 0
    for (const name of savedSignatures.keys()) if (!present.has(name)) n += 1
    return n
  }, [rows, savedSignatures])

  const commit = useCallback(
    (next: PricingMaps) => {
      for (const key of changedKeys(maps, next)) {
        draft.setValue(key, serializeNumberMap(next[key]))
      }
    },
    [maps, draft]
  )

  const [editing, setEditing] = useState<ModelPriceRow | null>(null)
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState<DraftFormValues>(BLANK_FORM)
  const [errors, setErrors] = useState<Record<string, string>>({})

  const openAdd = useCallback(() => {
    setEditing(null)
    setForm(BLANK_FORM)
    setErrors({})
    setOpen(true)
  }, [])

  const openEdit = useCallback((row: ModelPriceRow) => {
    setEditing(row)
    setForm(formFromRow(row))
    setErrors({})
    setOpen(true)
  }, [])

  const handleDelete = useCallback(
    (name: string) => commit(removeModel(maps, name)),
    [commit, maps]
  )

  const submit = () => {
    const result = rowFromForm(form)
    if ('errors' in result) {
      setErrors(result.errors)
      return
    }
    if (
      result.row.name !== editing?.name &&
      rows.some((row) => row.name === result.row.name)
    ) {
      setErrors({ name: 'A model with this name is already priced' })
      return
    }
    commit(applyRow(maps, result.row, editing?.name))
    setOpen(false)
  }

  const money = useCallback(
    (quota: number | undefined) =>
      quota === undefined || !ready ? null : formatQuota(quota, 4),
    [formatQuota, ready]
  )

  const columns = useMemo<ColumnDef<TableRowData>[]>(() => {
    const dash = <span className='text-muted-foreground'>—</span>
    return [
      {
        accessorKey: 'name',
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Model')} />
        ),
        cell: ({ row }) => (
          <div className='flex min-w-0 items-center gap-2'>
            <span className='truncate font-medium'>{row.original.name}</span>
            {row.original.changed ? (
              <StatusBadge variant='warning' label={t('Unsaved')} />
            ) : null}
          </div>
        ),
        meta: { label: t('Model') },
      },
      {
        accessorKey: 'mode',
        header: t('Billing'),
        cell: ({ row }) => (
          <StatusBadge
            variant={row.original.mode === 'per-request' ? 'info' : 'neutral'}
            label={
              row.original.mode === 'per-request'
                ? t('Per request')
                : t('Per token')
            }
          />
        ),
        filterFn: (row, id, value: string[]) =>
          value.length === 0 || value.includes(row.getValue<string>(id)),
        meta: { label: t('Billing') },
      },
      {
        id: 'ratio',
        accessorFn: (row) => row.ratio ?? row.price ?? -1,
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('Input / price')} />
        ),
        cell: ({ row }) => {
          const r = row.original
          if (r.mode === 'per-request') {
            if (r.price === undefined) return dash
            const amount = money(
              config ? r.price * config.quotaPerUnit : undefined
            )
            return (
              <div className='flex flex-col leading-tight'>
                <span className='font-mono'>{r.price}</span>
                <span className='text-muted-foreground text-xs'>
                  {amount ? t('{{amount}} per request', { amount }) : '--'}
                </span>
              </div>
            )
          }
          if (r.ratio === undefined) return dash
          const amount = money(r.ratio * TOKENS_PER_MILLION)
          return (
            <div className='flex flex-col leading-tight'>
              <span className='font-mono'>{r.ratio}</span>
              <span className='text-muted-foreground text-xs'>
                {amount ? t('{{amount}} / 1M tokens', { amount }) : '--'}
              </span>
            </div>
          )
        },
        meta: { label: t('Input / price') },
      },
      {
        id: 'completion',
        accessorFn: (row) => row.completion ?? -1,
        header: t('Completion'),
        cell: ({ row }) => {
          const r = row.original
          if (r.mode === 'per-request') return dash
          if (r.completion === undefined) {
            return (
              <span className='text-muted-foreground text-xs'>
                {t('Default')}
              </span>
            )
          }
          const amount = money(
            r.ratio === undefined
              ? undefined
              : r.ratio * r.completion * TOKENS_PER_MILLION
          )
          return (
            <div className='flex flex-col leading-tight'>
              <span className='font-mono'>×{r.completion}</span>
              <span className='text-muted-foreground text-xs'>
                {amount ? t('{{amount}} / 1M tokens', { amount }) : '--'}
              </span>
            </div>
          )
        },
        meta: { label: t('Completion') },
      },
      {
        id: 'cache',
        header: t('Cache'),
        cell: ({ row }) =>
          row.original.cache === undefined ? (
            dash
          ) : (
            <span className='font-mono'>×{row.original.cache}</span>
          ),
        meta: { label: t('Cache') },
      },
      {
        id: 'image',
        header: t('Image'),
        cell: ({ row }) =>
          row.original.image === undefined ? (
            dash
          ) : (
            <span className='font-mono'>×{row.original.image}</span>
          ),
        meta: { label: t('Image') },
      },
      {
        id: 'audio',
        header: t('Audio'),
        cell: ({ row }) =>
          row.original.audio === undefined ? (
            dash
          ) : (
            <span className='font-mono'>×{row.original.audio}</span>
          ),
        meta: { label: t('Audio') },
      },
      {
        id: 'audioCompletion',
        header: t('Audio completion'),
        cell: ({ row }) =>
          row.original.audioCompletion === undefined ? (
            dash
          ) : (
            <span className='font-mono'>×{row.original.audioCompletion}</span>
          ),
        meta: { label: t('Audio completion') },
      },
      {
        id: 'actions',
        enableHiding: false,
        header: () => <span className='sr-only'>{t('Actions')}</span>,
        cell: ({ row }) => (
          <div className='flex justify-end gap-1'>
            <Button
              type='button'
              variant='ghost'
              size='icon-sm'
              disabled={draft.disabled}
              aria-label={t('Edit {{name}}', { name: row.original.name })}
              onClick={() => openEdit(row.original)}
            >
              <Pencil />
            </Button>
            <Button
              type='button'
              variant='ghost'
              size='icon-sm'
              disabled={draft.disabled}
              aria-label={t('Delete {{name}}', { name: row.original.name })}
              className='text-destructive hover:text-destructive'
              onClick={() => handleDelete(row.original.name)}
            >
              <Trash2 />
            </Button>
          </div>
        ),
      },
    ]
  }, [t, money, config, draft.disabled, openEdit, handleDelete])

  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([])
  const [globalFilter, setGlobalFilter] = useState('')

  const { table } = useDataTable<TableRowData>({
    data: rows,
    columns,
    columnFilters,
    onColumnFiltersChange: setColumnFilters,
    globalFilter,
    onGlobalFilterChange: setGlobalFilter,
    getRowId: (row) => row.name,
    initialColumnVisibility: INITIAL_VISIBILITY,
    columnVisibilityStorageKey: 'system-settings-model-price-columns',
    globalFilterFn: (row, _columnId, filterValue) =>
      row.original.name.toLowerCase().includes(String(filterValue).toLowerCase()),
  })

  const modeCounts = useMemo(() => {
    let request = 0
    for (const row of rows) if (row.mode === 'per-request') request += 1
    return { request, token: rows.length - request }
  }, [rows])

  const previewInput =
    ready && form.mode === 'per-token' && form.ratio.trim() !== ''
      ? money(Number(form.ratio) * TOKENS_PER_MILLION)
      : null
  const previewPrice =
    ready && config && form.mode === 'per-request' && form.price.trim() !== ''
      ? money(Number(form.price) * config.quotaPerUnit)
      : null

  const setField = (field: keyof DraftFormValues, value: string) => {
    setForm((current) => ({ ...current, [field]: value }))
    setErrors((current) => {
      if (!(field in current)) return current
      const next = { ...current }
      delete next[field]
      return next
    })
  }

  const numberField = (
    field: (typeof NUMERIC_FORM_FIELDS)[number],
    label: string,
    description?: string,
    preview?: string | null
  ) => (
    <div key={field} className='flex flex-col gap-1.5'>
      <Label htmlFor={`price-${field}`}>{t(label)}</Label>
      <Input
        id={`price-${field}`}
        type='number'
        inputMode='decimal'
        step='any'
        min={0}
        value={form[field]}
        aria-invalid={errors[field] ? true : undefined}
        onChange={(event) => setField(field, event.target.value)}
      />
      {preview ? (
        <p className='text-muted-foreground text-xs'>{preview}</p>
      ) : null}
      {description ? (
        <p className='text-muted-foreground text-xs'>{t(description)}</p>
      ) : null}
      {errors[field] ? (
        <p role='alert' className='text-destructive text-sm'>
          {t(errors[field])}
        </p>
      ) : null}
    </div>
  )

  return (
    <div className='flex min-w-0 flex-col gap-3' data-settings-form-span='full'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Ratios multiply the base token price; amounts shown here follow the display currency. A model with no entry here is not priced.'
        )}
      </p>
      <DataTableToolbar
        table={table}
        searchPlaceholder={t('Search models...')}
        filters={[
          {
            columnId: 'mode',
            title: t('Billing'),
            options: [
              {
                label: t('Per token'),
                value: 'per-token',
                count: modeCounts.token,
              },
              {
                label: t('Per request'),
                value: 'per-request',
                count: modeCounts.request,
              },
            ],
          },
        ]}
        preActions={
          <Button type='button' onClick={openAdd} disabled={draft.disabled}>
            <Plus data-icon='inline-start' />
            {t('Add model')}
          </Button>
        }
      />
      {removedCount > 0 ? (
        <p className='text-muted-foreground text-xs' role='status'>
          {t('{{count}} priced model(s) removed. Save to apply.', {
            count: removedCount,
          })}
        </p>
      ) : null}
      <DataTableView
        table={table}
        emptyTitle={t('No models are priced yet')}
        emptyDescription={t('Use Add model to give a model a price.')}
        tableClassName='min-w-[640px]'
        renderRow={(row: Row<TableRowData>, { getCellClassName }) => (
          <DataTableRow
            key={row.id}
            row={row}
            getColumnClassName={(columnId) => getCellClassName(columnId)}
          />
        )}
      />
      <DataTablePagination table={table} />

      <Dialog
        open={open}
        onOpenChange={setOpen}
        title={editing ? t('Edit model price') : t('Add model price')}
        description={t(
          'Leave an optional field empty to remove it. Saving here only changes the draft; use Save Changes to apply.'
        )}
        contentClassName='sm:max-w-[520px]'
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              onClick={() => setOpen(false)}
            >
              {t('Cancel')}
            </Button>
            <Button type='submit' form='model-price-form'>
              {editing ? t('Update') : t('Add')}
            </Button>
          </>
        }
      >
        <form
          id='model-price-form'
          className='flex flex-col gap-4'
          onSubmit={(event) => {
            event.preventDefault()
            event.stopPropagation()
            submit()
          }}
        >
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='price-name'>{t('Model name')}</Label>
            <Input
              id='price-name'
              value={form.name}
              disabled={Boolean(editing)}
              placeholder='model-a'
              aria-invalid={errors.name ? true : undefined}
              onChange={(event) => setField('name', event.target.value)}
            />
            {errors.name ? (
              <p role='alert' className='text-destructive text-sm'>
                {t(errors.name)}
              </p>
            ) : null}
          </div>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='price-mode'>{t('Billing')}</Label>
            <Select
              items={[
                { value: 'per-token', label: t('Per token') },
                { value: 'per-request', label: t('Per request') },
              ]}
              value={form.mode}
              onValueChange={(next) =>
                setField('mode', next === 'per-request' ? 'per-request' : 'per-token')
              }
            >
              <SelectTrigger id='price-mode' className='w-full'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  <SelectItem value='per-token'>{t('Per token')}</SelectItem>
                  <SelectItem value='per-request'>
                    {t('Per request')}
                  </SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
          {form.mode === 'per-request'
            ? numberField(
                'price',
                'Fixed price per request (USD)',
                'Takes precedence over token ratios. Stored in USD and shown in the display currency.',
                previewPrice
                  ? t('Shown as {{amount}} per request', { amount: previewPrice })
                  : null
              )
            : (
              <div className='grid gap-4 sm:grid-cols-2'>
                {numberField(
                  'ratio',
                  'Model ratio',
                  'Multiplier applied to input tokens.',
                  previewInput
                    ? t('Input price {{amount}} / 1M tokens', {
                        amount: previewInput,
                      })
                    : null
                )}
                {numberField(
                  'completion',
                  'Completion ratio',
                  'Output tokens cost this many times the input price.'
                )}
                {numberField(
                  'cache',
                  'Cache ratio',
                  'Cached input tokens cost this fraction of the input price.'
                )}
                {numberField('image', 'Image ratio')}
                {numberField('audio', 'Audio ratio')}
                {numberField('audioCompletion', 'Audio completion ratio')}
              </div>
            )}
        </form>
      </Dialog>
    </div>
  )
}
