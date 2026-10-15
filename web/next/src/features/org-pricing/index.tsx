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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Eye, RefreshCw, Save, Search, TriangleAlert } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { PageHeader } from '@/components/layout/page-header'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toApiError } from '@/lib/api'
import { ROLE, hasMinRole } from '@/lib/auth'
import { currentUserQueryOptions } from '@/lib/user'

import {
  fetchPricing,
  isVersionConflict,
  previewPricing,
  pricingQueryKey,
  savePricing,
} from './api'
import { PreviewDialog } from './components/preview-dialog'
import { PricingTable } from './components/pricing-table'
import { draftKey, loadDraft, saveDraft } from './lib/draft'
import {
  buildBatch,
  clearModel,
  setScalar,
  setTiers,
  validateAll,
  type DiffEntry,
  type Edits,
  type PricingRow,
  type PricingSheet,
  type ScalarField,
  type TierDraft,
} from './lib/pricing'

const PAGE_SIZE = 50

type BillingFilter = 'all' | 'ratio' | 'per-call'

/** Group ratios are display-only here: the write routes do not take them. */
function GroupRatios(props: { ratios: Record<string, number> }) {
  const { t } = useTranslation()
  const entries = Object.entries(props.ratios)
  if (entries.length === 0) return null
  return (
    <section className='space-y-2' data-testid='pricing-group-ratios'>
      <h2 className='text-sm font-medium'>{t('Group ratios (read-only)')}</h2>
      <div className='flex flex-wrap gap-3'>
        {entries.map(([group, ratio]) => (
          <div
            key={group}
            className='flex min-w-20 flex-col items-center gap-1 rounded-md border px-3.5 py-2'
          >
            <Badge variant='secondary'>{group}</Badge>
            <span className='font-mono text-base'>x{ratio.toFixed(2)}</span>
          </div>
        ))}
      </div>
    </section>
  )
}

function PricingEditor(props: { userId: number | undefined; canWrite: boolean }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const storageKey = draftKey(props.userId)
  const readOnly = !props.canWrite

  const sheetQuery = useQuery({
    queryKey: pricingQueryKey,
    queryFn: fetchPricing,
    retry: false,
  })
  const sheet: PricingSheet | undefined = sheetQuery.data

  // Unsaved edits survive a refresh; only a writer ever has any.
  const [edits, setEdits] = useState<Edits>(() =>
    props.canWrite ? loadDraft(storageKey) : {}
  )
  useEffect(() => {
    if (props.canWrite) saveDraft(storageKey, edits)
  }, [edits, storageKey, props.canWrite])

  const [search, setSearch] = useState('')
  const [vendor, setVendor] = useState('')
  const [billing, setBilling] = useState<BillingFilter>('all')
  const [unpricedOnly, setUnpricedOnly] = useState(false)
  const [editedOnly, setEditedOnly] = useState(false)
  const [page, setPage] = useState(0)
  const [diffs, setDiffs] = useState<DiffEntry[] | null>(null)
  const [conflict, setConflict] = useState(false)
  const [confirmDiscard, setConfirmDiscard] = useState(false)

  const rows = useMemo(() => sheet?.pricing ?? [], [sheet])

  // Edits of a model the server no longer lists are never sent.
  const liveEdits = useMemo(() => {
    const known = new Set(rows.map((r) => r.model_name))
    return Object.fromEntries(
      Object.entries(edits).filter(([model]) => known.has(model))
    )
  }, [edits, rows])
  const batch = useMemo(() => buildBatch(liveEdits), [liveEdits])
  const errors = useMemo(() => validateAll(liveEdits), [liveEdits])
  const invalidModels = Object.keys(errors)
  const dirty = batch.length > 0
  const editedModels = useMemo(
    () => new Set(Object.keys(liveEdits)),
    [liveEdits]
  )

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return rows.filter((row) => {
      if (vendor && row.vendor !== vendor) return false
      if (billing === 'ratio' && row.quota_type !== 0) return false
      if (billing === 'per-call' && row.quota_type !== 1) return false
      if (unpricedOnly && row.price_configured) return false
      if (editedOnly && !editedModels.has(row.model_name)) return false
      if (q && !row.model_name.toLowerCase().includes(q)) return false
      return true
    })
  }, [rows, search, vendor, billing, unpricedOnly, editedOnly, editedModels])

  const pageCount = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
  const safePage = Math.min(page, pageCount - 1)
  const visible = filtered.slice(safePage * PAGE_SIZE, (safePage + 1) * PAGE_SIZE)

  // The per-call column only exists when some listed model is per-call (or an
  // edit already targets one); otherwise it would be a column of dashes.
  const showModelPrice =
    filtered.some((r) => r.quota_type === 1) ||
    Object.values(liveEdits).some((e) => 'model_price' in e)
  const showSearchUnit = rows.some((r) => r.search_unit_price !== undefined)
  const unpricedCount = rows.filter((r) => !r.price_configured).length

  const changeEdits = (fn: (prev: Edits) => Edits) => {
    setEdits(fn)
    // Any further edit invalidates the diff shown by the last preview.
    setDiffs(null)
  }
  const onScalar = (model: string, field: ScalarField, value: string) =>
    changeEdits((prev) => setScalar(prev, model, field, value))
  const onTiers = (row: PricingRow, tiers: TierDraft[]) =>
    changeEdits((prev) => setTiers(prev, row, tiers))
  const onReset = (model: string) =>
    changeEdits((prev) => clearModel(prev, model))

  const preview = useMutation({
    mutationFn: () => previewPricing(batch),
    onSuccess: (result) => setDiffs(result),
    onError: (err) => toast.error(toApiError(err).message),
  })

  const save = useMutation({
    // The version is the one this page last read: a stale one loses the CAS.
    mutationFn: () => savePricing(batch, sheet?.version ?? 0),
    onSuccess: (result) => {
      toast.success(t('{{n}} models updated', { n: result.updated_count }))
      setEdits({})
      setDiffs(null)
      setConflict(false)
      void qc.invalidateQueries({ queryKey: pricingQueryKey })
    },
    onError: (err) => {
      const apiError = toApiError(err)
      if (isVersionConflict(apiError)) {
        toast.error(
          t('Pricing changed since you last loaded it — refreshing')
        )
        setConflict(true)
        setDiffs(null)
        void qc.invalidateQueries({ queryKey: pricingQueryKey })
        return
      }
      toast.error(apiError.message)
    },
  })

  const busy = preview.isPending || save.isPending
  const canSubmit = dirty && invalidModels.length === 0 && !busy

  if (sheetQuery.isPending) return <LoadingState />

  const loadFailed = sheetQuery.isError && sheet === undefined

  let countText = '—'
  if (!loadFailed) {
    countText =
      filtered.length === 1
        ? t('1 model')
        : t('{{n}} models', { n: filtered.length })
  }

  const actions = (
    <div className='flex flex-wrap items-center gap-2'>
      <Button
        type='button'
        variant='outline'
        onClick={() => void sheetQuery.refetch()}
        disabled={sheetQuery.isFetching}
        data-testid='pricing-refresh'
      >
        <RefreshCw className={sheetQuery.isFetching ? 'animate-spin' : ''} aria-hidden />
        {t('Refresh')}
      </Button>
      {!readOnly && (
        <>
          <Button
            type='button'
            variant='outline'
            disabled={!canSubmit}
            onClick={() => preview.mutate()}
            data-testid='pricing-preview'
          >
            <Eye aria-hidden />
            {preview.isPending ? t('Previewing...') : t('Preview')}
          </Button>
          <Button
            type='button'
            disabled={!canSubmit}
            onClick={() => save.mutate()}
            data-testid='pricing-save'
          >
            <Save aria-hidden />
            {save.isPending ? t('Saving...') : t('Save')}
          </Button>
        </>
      )}
    </div>
  )

  return (
    <div className='space-y-6'>
      <PageHeader
        title={t('Pricing Versions')}
        description={t('Vendor cost, ratios and billing type of every model your organization can call.')}
        actions={actions}
      />

      {readOnly && (
        <Alert data-testid='pricing-read-only'>
          <AlertTitle>{t('Read-only')}</AlertTitle>
          <AlertDescription>
            {t('Prices are global to the platform, so only a platform root user can change them.')}
          </AlertDescription>
        </Alert>
      )}

      {conflict && (
        <Alert variant='destructive' data-testid='pricing-conflict'>
          <TriangleAlert aria-hidden />
          <AlertTitle>
            {t('Prices changed while you were editing')}
          </AlertTitle>
          <AlertDescription>
            {t('Someone else saved new prices. The table was reloaded and your edits are kept: review them against the new prices, then save again.')}
          </AlertDescription>
        </Alert>
      )}

      {invalidModels.length > 0 && (
        <Alert variant='destructive' data-testid='pricing-invalid'>
          <TriangleAlert aria-hidden />
          <AlertTitle>{t('Some values are not valid')}</AlertTitle>
          <AlertDescription>
            {t('Fix the highlighted fields before previewing or saving: {{models}}', {
              models: invalidModels.join(', '),
            })}
          </AlertDescription>
        </Alert>
      )}

      {sheetQuery.isError && sheet !== undefined && (
        <Alert variant='destructive' data-testid='pricing-refresh-error'>
          <TriangleAlert aria-hidden />
          <AlertTitle>{t('Failed to refresh pricing data')}</AlertTitle>
          <AlertDescription>
            {sheetQuery.error.message}{' '}
            <Button
              type='button'
              variant='link'
              className='h-auto p-0'
              onClick={() => void sheetQuery.refetch()}
            >
              {t('Retry')}
            </Button>
          </AlertDescription>
        </Alert>
      )}

      <div className='flex flex-wrap items-center gap-2'>
        <div className='relative'>
          <Search
            className='text-muted-foreground pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2'
            aria-hidden
          />
          <Input
            value={search}
            onChange={(e) => {
              setSearch(e.target.value)
              setPage(0)
            }}
            placeholder={t('Search models')}
            aria-label={t('Search models')}
            className='w-56 ps-8'
          />
        </div>
        <NativeSelect
          value={vendor}
          onChange={(e) => {
            setVendor(e.target.value)
            setPage(0)
          }}
          aria-label={t('Vendor')}
        >
          <NativeSelectOption value=''>{t('All vendors')}</NativeSelectOption>
          {(sheet?.vendors ?? []).map((v) => (
            <NativeSelectOption key={v} value={v}>
              {v}
            </NativeSelectOption>
          ))}
        </NativeSelect>
        <NativeSelect
          value={billing}
          onChange={(e) => {
            setBilling(e.target.value as BillingFilter)
            setPage(0)
          }}
          aria-label={t('Billing type')}
        >
          <NativeSelectOption value='all'>{t('All billing types')}</NativeSelectOption>
          <NativeSelectOption value='ratio'>{t('Ratio-based')}</NativeSelectOption>
          <NativeSelectOption value='per-call'>{t('Per-call price')}</NativeSelectOption>
        </NativeSelect>
        <Button
          type='button'
          variant={unpricedOnly ? 'default' : 'outline'}
          aria-pressed={unpricedOnly}
          onClick={() => {
            setUnpricedOnly((v) => !v)
            setPage(0)
          }}
          data-testid='filter-unpriced'
        >
          {t('Unpriced only')}
          {unpricedCount > 0 && <Badge variant='warning'>{unpricedCount}</Badge>}
        </Button>
        {!readOnly && (
          <Button
            type='button'
            variant={editedOnly ? 'default' : 'outline'}
            aria-pressed={editedOnly}
            disabled={editedModels.size === 0 && !editedOnly}
            onClick={() => {
              setEditedOnly((v) => !v)
              setPage(0)
            }}
            data-testid='filter-edited'
          >
            {t('Edited only')}
          </Button>
        )}
        <div className='ms-auto flex items-center gap-3'>
          {sheet && (
            <span className='text-muted-foreground text-xs' data-testid='pricing-version'>
              {t('Version')} {sheet.version}
            </span>
          )}
          <span className='text-muted-foreground font-mono text-xs' data-testid='pricing-model-count'>
            {countText}
          </span>
        </div>
      </div>

      {!readOnly && editedModels.size > 0 && (
        <div className='flex flex-wrap items-center gap-3 rounded-md border px-3 py-2 text-sm' data-testid='pricing-dirty-bar'>
          <span>
            {t('{{n}} models edited and not saved', { n: editedModels.size })}
          </span>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={busy}
            onClick={() => setConfirmDiscard(true)}
            data-testid='pricing-discard'
          >
            {t('Discard all edits')}
          </Button>
        </div>
      )}

      {loadFailed && (
        <div data-testid='pricing-load-error'>
          <ErrorState
            title={t('Failed to load pricing data')}
            description={sheetQuery.error?.message}
            onRetry={() => void sheetQuery.refetch()}
          />
        </div>
      )}

      {!loadFailed && filtered.length === 0 && (
        <EmptyState
          title={rows.length === 0 ? t('No models') : t('No models match the filters')}
          description={
            rows.length === 0
              ? t('No model has a price entry yet.')
              : t('Change or clear the filters to see more models.')
          }
        />
      )}

      {!loadFailed && filtered.length > 0 && (
        <div className='space-y-3'>
          <div className='rounded-lg border'>
            <PricingTable
              rows={visible}
              edits={liveEdits}
              errors={errors}
              readOnly={readOnly}
              showModelPrice={showModelPrice}
              showSearchUnit={showSearchUnit}
              onScalar={onScalar}
              onTiers={onTiers}
              onReset={onReset}
            />
          </div>
          {pageCount > 1 && (
            <div className='flex items-center justify-end gap-2 text-sm'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={safePage === 0}
                onClick={() => setPage(safePage - 1)}
              >
                {t('Previous')}
              </Button>
              <span className='text-muted-foreground tabular-nums'>
                {safePage + 1} / {pageCount}
              </span>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={safePage >= pageCount - 1}
                onClick={() => setPage(safePage + 1)}
              >
                {t('Next')}
              </Button>
            </div>
          )}
        </div>
      )}

      {sheet && <GroupRatios ratios={sheet.groupRatio} />}

      <PreviewDialog
        open={diffs !== null}
        onOpenChange={(open) => {
          if (!open) setDiffs(null)
        }}
        diffs={diffs ?? []}
        saving={save.isPending}
        canSave={canSubmit || save.isPending}
        onSave={() => save.mutate()}
      />

      <ConfirmDialog
        open={confirmDiscard}
        onOpenChange={setConfirmDiscard}
        title={t('Discard all unsaved edits?')}
        desc={t('Every price you changed since the last save goes back to the value on the server.')}
        confirmText={t('Discard')}
        destructive
        handleConfirm={() => {
          setEdits({})
          setDiffs(null)
          setConfirmDiscard(false)
        }}
      />
    </div>
  )
}

/**
 * Model pricing (tenant admins). Reads are open to them; the write routes are
 * platform-root only on the server (the maps behind them are process-global),
 * so everyone else gets the same table read-only.
 */
export function OrgPricingPage() {
  const { t } = useTranslation()
  const me = useQuery(currentUserQueryOptions)

  if (me.isPending) return <LoadingState />
  if (me.isError) {
    return (
      <ErrorState
        title={t('Failed to load your account')}
        description={me.error.message}
        onRetry={() => void me.refetch()}
      />
    )
  }
  return (
    <PricingEditor
      key={me.data.id}
      userId={me.data.id}
      canWrite={hasMinRole(me.data.role, ROLE.root)}
    />
  )
}
