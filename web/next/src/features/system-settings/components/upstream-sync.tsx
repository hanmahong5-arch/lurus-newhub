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
import { useMutation, useQuery } from '@tanstack/react-query'
import { AlertTriangle, Check, RefreshCcw, Search } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
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
import { getServerErrorMessage } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import {
  fetchUpstreamRatios,
  getSyncableChannels,
  type DifferencesMap,
  type RatioType,
  type SyncableChannel,
  type UpstreamTestResult,
} from '../api'
import { useOptionDraft } from '../hooks/use-option-draft'
import {
  applyRow,
  buildRows,
  changedKeys,
  parsePricingMaps,
  serializeNumberMap,
  type ModelPriceRow,
} from '../lib/pricing'


/** The server's built-in preset channel (handler.GetSyncableChannels). */
const OFFICIAL_PRESET_ID = -100

const RATIO_TYPES: RatioType[] = [
  'model_ratio',
  'completion_ratio',
  'cache_ratio',
  'model_price',
]

const TYPE_LABEL: Record<RatioType, string> = {
  model_ratio: 'Model ratio',
  completion_ratio: 'Completion ratio',
  cache_ratio: 'Cache ratio',
  model_price: 'Fixed price',
}

const TYPE_FIELD: Record<RatioType, keyof ModelPriceRow> = {
  model_ratio: 'ratio',
  completion_ratio: 'completion',
  cache_ratio: 'cache',
  model_price: 'price',
}

const PAGE = 100

type DiffRow = {
  id: string
  model: string
  type: RatioType
  current: number | null
  upstreams: Record<string, number | 'same' | null>
  confidence: Record<string, boolean>
}

type Choice = { source: string; value: number }

function channelLabel(channel: SyncableChannel, t: (key: string) => string) {
  return channel.id === OFFICIAL_PRESET_ID
    ? t('Official ratio preset')
    : channel.name
}

function flatten(differences: DifferencesMap | null): DiffRow[] {
  const rows: DiffRow[] = []
  for (const [model, byType] of Object.entries(differences ?? {})) {
    for (const type of RATIO_TYPES) {
      const item = byType[type]
      if (!item) continue
      rows.push({
        id: `${model}\u0000${type}`,
        model,
        type,
        current: typeof item.current === 'number' ? item.current : null,
        upstreams: item.upstreams ?? {},
        confidence: item.confidence ?? {},
      })
    }
  }
  return rows.sort(
    (a, b) => a.model.localeCompare(b.model) || a.type.localeCompare(b.type)
  )
}

/**
 * Pull ratios from other gateways (or the built-in preset), compare them with
 * the local ones, and write the chosen values into the form's draft. Nothing is
 * saved here: the person reviews the draft in the model table and saves with
 * the page's Save button, so one click can never reprice the catalogue.
 */
export function UpstreamSync() {
  const { t } = useTranslation()
  const draft = useOptionDraft()
  const [pickerOpen, setPickerOpen] = useState(false)
  const [chosenIds, setChosenIds] = useState<number[]>([])
  const [differences, setDifferences] = useState<DifferencesMap | null>(null)
  const [results, setResults] = useState<UpstreamTestResult[]>([])
  const [selected, setSelected] = useState<Record<string, Choice>>({})
  const [query, setQuery] = useState('')
  const [typeFilter, setTypeFilter] = useState<'all' | RatioType>('all')
  const [limit, setLimit] = useState(PAGE)

  const channels = useQuery({
    queryKey: ['system-settings', 'ratio-sync-channels'],
    queryFn: getSyncableChannels,
    enabled: pickerOpen,
  })

  const fetchMutation = useMutation({
    mutationFn: (ids: number[]) =>
      fetchUpstreamRatios({ channel_ids: ids, timeout: 10 }),
    onMutate: () => {
      setDifferences(null)
      setResults([])
      setSelected({})
    },
    onSuccess: (data) => {
      const tests = data.test_results ?? []
      setResults(tests)
      setDifferences(data.differences ?? {})
      if (tests.length > 0 && tests.every((r) => r.status === 'error')) {
        toast.error(t('No source could be reached.'))
      } else if (Object.keys(data.differences ?? {}).length === 0) {
        toast.success(t('No price differences found'))
      }
    },
    onError: (error) =>
      toast.error(getServerErrorMessage(error, t('Failed to fetch upstream prices'))),
  })

  const rows = useMemo(() => flatten(differences), [differences])
  const sources = useMemo(() => {
    const names = new Set<string>()
    for (const row of rows) for (const name of Object.keys(row.upstreams)) names.add(name)
    return [...names]
  }, [rows])

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return rows.filter(
      (row) =>
        (typeFilter === 'all' || row.type === typeFilter) &&
        (needle === '' || row.model.toLowerCase().includes(needle))
    )
  }, [rows, query, typeFilter])

  const toggle = (row: DiffRow, source: string, value: number) =>
    setSelected((current) => {
      const next = { ...current }
      if (next[row.id]?.source === source) delete next[row.id]
      else next[row.id] = { source, value }
      return next
    })

  const selectAllFrom = (source: string) =>
    setSelected((current) => {
      const next = { ...current }
      for (const row of filtered) {
        const value = row.upstreams[source]
        if (typeof value === 'number' && row.confidence[source] !== false) {
          next[row.id] = { source, value }
        }
      }
      return next
    })

  const applyToDraft = () => {
    const maps = parsePricingMaps((key) => draft.value(key))
    const rowsByName = new Map(buildRows(maps).map((row) => [row.name, row]))
    let nextMaps = maps
    for (const [id, pick] of Object.entries(selected)) {
      const [model, type] = id.split('\u0000') as [string, RatioType]
      const base: ModelPriceRow = rowsByName.get(model) ?? {
        name: model,
        mode: type === 'model_price' ? 'per-request' : 'per-token',
      }
      const next: ModelPriceRow = { ...base, [TYPE_FIELD[type]]: pick.value }
      if (type === 'model_price') next.mode = 'per-request'
      else if (base.mode === 'per-request' && base.price !== undefined) {
        // A fixed price wins over ratios; leave that model's mode alone.
        next.mode = 'per-request'
      }
      nextMaps = applyRow(nextMaps, next, model)
      rowsByName.set(model, next)
    }
    for (const key of changedKeys(maps, nextMaps)) {
      draft.setValue(key, serializeNumberMap(nextMaps[key]))
    }
    toast.success(
      t('{{count}} values copied into the draft. Review them in Models, then save.', {
        count: Object.keys(selected).length,
      })
    )
    setSelected({})
  }

  let pickerBody: ReactNode
  if (channels.isLoading) {
    pickerBody = (
      <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
    )
  } else if (channels.isError) {
    pickerBody = (
      <ErrorState
        title={t('Failed to load channels')}
        description={getServerErrorMessage(channels.error)}
        onRetry={() => void channels.refetch()}
      />
    )
  } else {
    pickerBody = (
      <ul className='flex max-h-72 flex-col gap-1 overflow-y-auto'>
        {(channels.data ?? []).map((channel) => {
          const id = `sync-channel-${channel.id}`
          return (
            <li
              key={channel.id}
              className='hover:bg-accent/40 flex items-center gap-3 rounded-md px-2 py-1.5'
            >
              <Checkbox
                id={id}
                checked={chosenIds.includes(channel.id)}
                onCheckedChange={(checked) =>
                  setChosenIds((current) =>
                    checked
                      ? [...current, channel.id]
                      : current.filter((value) => value !== channel.id)
                  )
                }
              />
              <Label htmlFor={id} className='flex min-w-0 flex-col items-start'>
                <span className='truncate'>{channelLabel(channel, t)}</span>
                <span className='text-muted-foreground truncate text-xs'>
                  {channel.base_url}
                </span>
              </Label>
            </li>
          )
        })}
        {(channels.data ?? []).length === 0 ? (
          <li className='text-muted-foreground text-sm'>
            {t('No channels with a base URL were found.')}
          </li>
        ) : null}
      </ul>
    )
  }

  const errored = results.filter((r) => r.status === 'error')
  const selectedCount = Object.keys(selected).length

  return (
    <div className='flex min-w-0 flex-col gap-3' data-settings-form-span='full'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Compare your ratios with other gateways or the built-in preset. Chosen values go into the draft; nothing is saved until you press Save Changes.'
        )}
      </p>
      <div className='flex flex-wrap items-center gap-2'>
        <Button
          type='button'
          variant='outline'
          disabled={fetchMutation.isPending || draft.disabled}
          onClick={() => setPickerOpen(true)}
        >
          <RefreshCcw
            data-icon='inline-start'
            className={cn(fetchMutation.isPending && 'animate-spin')}
          />
          {t('Select price sources')}
        </Button>
        <span className='text-muted-foreground text-sm' role='status'>
          {t('{{count}} selected', { count: selectedCount })}
        </span>
        <Button
          type='button'
          disabled={selectedCount === 0}
          onClick={applyToDraft}
        >
          <Check data-icon='inline-start' />
          {t('Copy selected into draft')}
        </Button>
      </div>

      {errored.length > 0 ? (
        <div
          role='alert'
          className='border-warning/40 bg-warning/10 rounded-lg border px-3 py-2 text-sm'
        >
          <div className='font-medium'>{t('Some sources failed')}</div>
          <ul className='mt-1 list-disc pl-5'>
            {errored.map((result) => (
              <li key={result.name}>
                {result.name}: {result.error}
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {fetchMutation.isError ? (
        <ErrorState
          title={t('Failed to fetch upstream prices')}
          description={getServerErrorMessage(fetchMutation.error)}
          onRetry={() => fetchMutation.mutate(chosenIds)}
        />
      ) : null}

      {differences !== null && !fetchMutation.isError ? (
        <>
          <div className='flex flex-wrap items-center gap-2'>
            <div className='relative min-w-48 flex-1'>
              <Search className='text-muted-foreground pointer-events-none absolute top-2 left-2.5 size-4' />
              <Input
                value={query}
                onChange={(event) => {
                  setQuery(event.target.value)
                  setLimit(PAGE)
                }}
                placeholder={t('Search models...')}
                aria-label={t('Search models...')}
                className='pl-8'
              />
            </div>
            <Select
              items={[
                { value: 'all', label: t('All fields') },
                ...RATIO_TYPES.map((type) => ({
                  value: type,
                  label: t(TYPE_LABEL[type]),
                })),
              ]}
              value={typeFilter}
              onValueChange={(next) => {
                setTypeFilter((next as 'all' | RatioType) ?? 'all')
                setLimit(PAGE)
              }}
            >
              <SelectTrigger className='w-48' aria-label={t('Field')}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  <SelectItem value='all'>{t('All fields')}</SelectItem>
                  {RATIO_TYPES.map((type) => (
                    <SelectItem key={type} value={type}>
                      {t(TYPE_LABEL[type])}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>

          <StaticDataTable
            data={filtered.slice(0, limit)}
            getRowKey={(row) => row.id}
            emptyContent={t(
              rows.length === 0
                ? 'No price differences found'
                : 'No models match your search'
            )}
            columns={[
              {
                id: 'model',
                header: t('Model'),
                cellClassName: 'font-medium',
                cell: (row: DiffRow) => row.model,
              },
              {
                id: 'field',
                header: t('Field'),
                cell: (row: DiffRow) => t(TYPE_LABEL[row.type]),
              },
              {
                id: 'current',
                header: t('Current'),
                className: 'text-right',
                cellClassName: 'text-right font-mono',
                cell: (row: DiffRow) => row.current ?? '—',
              },
              ...sources.map((source) => ({
                id: `source-${source}`,
                header: (
                  <div className='flex flex-col items-end gap-1'>
                    <span className='max-w-40 truncate'>{source}</span>
                    <Button
                      type='button'
                      variant='link'
                      size='sm'
                      className='h-auto p-0 text-xs'
                      onClick={() => selectAllFrom(source)}
                    >
                      {t('Select all')}
                    </Button>
                  </div>
                ),
                className: 'text-right',
                cellClassName: 'text-right',
                cell: (row: DiffRow) => {
                  const value = row.upstreams[source]
                  if (value === 'same') {
                    return (
                      <span className='text-muted-foreground text-xs'>
                        {t('Same')}
                      </span>
                    )
                  }
                  if (typeof value !== 'number') {
                    return <span className='text-muted-foreground'>—</span>
                  }
                  const active = selected[row.id]?.source === source
                  const doubtful = row.confidence[source] === false
                  return (
                    <Button
                      type='button'
                      size='sm'
                      variant={active ? 'default' : 'outline'}
                      aria-pressed={active}
                      aria-label={t('Use {{value}} from {{source}} for {{model}}', {
                        value,
                        source,
                        model: row.model,
                      })}
                      onClick={() => toggle(row, source, value)}
                    >
                      {doubtful ? (
                        <AlertTriangle
                          data-icon='inline-start'
                          aria-label={t('Low confidence')}
                        />
                      ) : null}
                      <span className='font-mono'>{value}</span>
                    </Button>
                  )
                },
              })),
            ]}
          />
          {filtered.length > limit ? (
            <div className='flex justify-center'>
              <Button
                type='button'
                variant='outline'
                onClick={() => setLimit((current) => current + PAGE)}
              >
                {t('Show more ({{count}} left)', {
                  count: filtered.length - limit,
                })}
              </Button>
            </div>
          ) : null}
        </>
      ) : null}

      <Dialog
        open={pickerOpen}
        onOpenChange={setPickerOpen}
        title={t('Select price sources')}
        description={t(
          'Each source is asked for its ratio list. Sources are fetched by the server, never by your browser.'
        )}
        contentClassName='sm:max-w-[520px]'
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              onClick={() => setPickerOpen(false)}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              disabled={chosenIds.length === 0}
              onClick={() => {
                setPickerOpen(false)
                fetchMutation.mutate(chosenIds)
              }}
            >
              {t('Fetch prices')}
            </Button>
          </>
        }
      >
        {pickerBody}
      </Dialog>
    </div>
  )
}
