import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { toApiError } from '@/lib/api'

import {
  allowlistQueryOptions,
  policyKey,
  routableModelIdsQueryOptions,
  saveAllowlist,
} from '../api'
import {
  checkEntry,
  initialSelection,
  sameSet,
  type Allowlist,
  type EntryCheck,
} from '../lib/policy'

function ModelChips(props: { models: string[]; empty: string }) {
  if (props.models.length === 0) {
    return <span className='text-muted-foreground text-sm'>{props.empty}</span>
  }
  return (
    <div className='flex flex-wrap gap-1.5'>
      {props.models.map((m) => (
        <Badge key={m} variant='secondary' className='font-mono'>
          {m}
        </Badge>
      ))}
    </div>
  )
}

function Editor(props: { data: Allowlist }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const { data } = props
  const ceiling = data.platformUnrestricted ? null : data.platformAllowed
  const routable = useQuery({
    ...routableModelIdsQueryOptions,
    enabled: data.platformUnrestricted,
  })

  const start = initialSelection(data)
  const [selected, setSelected] = useState<string[]>(start)
  const [entry, setEntry] = useState('')
  const [entryError, setEntryError] = useState<EntryCheck | null>(null)
  const [confirmEmpty, setConfirmEmpty] = useState(false)

  // Candidates the tenant can tick: the platform grant when there is one,
  // otherwise the models this tenant can route. Whatever is already selected
  // stays visible even if it dropped out of the list.
  const base = ceiling ?? routable.data ?? []
  const candidates = [...new Set([...base, ...selected])]

  const save = useMutation({
    mutationFn: (models: string[]) => saveAllowlist(models),
    onSuccess: async () => {
      toast.success(t('Saved'))
      setConfirmEmpty(false)
      await qc.invalidateQueries({ queryKey: policyKey })
    },
    onError: (e) => {
      setConfirmEmpty(false)
      toast.error(toApiError(e).message)
    },
  })

  const toggle = (m: string, on: boolean) =>
    setSelected((s) => (on ? [...s, m] : s.filter((x) => x !== m)))

  const add = () => {
    const verdict = checkEntry(entry, selected, ceiling)
    if (verdict !== 'ok') {
      setEntryError(verdict)
      return
    }
    setSelected((s) => [...s, entry.trim()])
    setEntry('')
    setEntryError(null)
  }

  const entryText: Record<EntryCheck, string> = {
    ok: '',
    blank: t('Enter a model name.'),
    duplicate: t('That model is already in the list.'),
    wildcard: t('Use * only as the last character, for example model-a*.'),
    'too-long': t('A model name can be at most 128 bytes.'),
    'not-granted': t('The platform has not granted this model to your organization.'),
  }

  const dirty = !sameSet(selected, start) || !data.tenantConfigured
  const submit = () => {
    if (selected.length === 0) {
      setConfirmEmpty(true)
      return
    }
    save.mutate(selected)
  }

  return (
    <div className='space-y-5'>
      <div className='grid gap-4 md:grid-cols-3'>
        <div className='space-y-2 rounded-lg border p-3'>
          <h3 className='text-sm font-medium'>{t('Granted by the platform')}</h3>
          {data.platformUnrestricted ? (
            <p className='text-muted-foreground text-sm'>
              {t('No platform limit: every model is available to you.')}
            </p>
          ) : (
            <ModelChips
              models={data.platformAllowed}
              empty={t('The platform has granted no models.')}
            />
          )}
        </div>
        <div className='space-y-2 rounded-lg border p-3'>
          <h3 className='text-sm font-medium'>{t('Selected by your organization')}</h3>
          {data.tenantConfigured ? (
            <ModelChips
              models={data.tenantSelected}
              empty={t('Nothing selected: all requests are refused.')}
            />
          ) : (
            <p className='text-muted-foreground text-sm'>
              {t('Not narrowed yet: the platform grant applies as is.')}
            </p>
          )}
        </div>
        <div className='space-y-2 rounded-lg border p-3'>
          <h3 className='text-sm font-medium'>{t('Effective')}</h3>
          <ModelChips
            models={data.effective.map((m) => (m === '*' ? t('All models') : m))}
            empty={t('No model can be called.')}
          />
        </div>
      </div>

      <div className='space-y-3'>
        <div>
          <h3 className='text-sm font-medium'>{t('Choose the models your organization may call')}</h3>
          <p className='text-muted-foreground text-xs'>
            {t('You can only narrow the platform grant, never widen it. Changes reach all instances within about 30 seconds.')}
          </p>
        </div>

        {data.platformUnrestricted && routable.isError && (
          <p role='alert' className='text-destructive text-xs'>
            {t('Could not load the model list: {{message}}. You can still add model names by hand.', {
              message: toApiError(routable.error).message,
            })}
          </p>
        )}

        {candidates.length === 0 ? (
          <p className='text-muted-foreground text-sm'>
            {t('No models to choose from.')}
          </p>
        ) : (
          <ul className='grid gap-2 sm:grid-cols-2 lg:grid-cols-3'>
            {candidates.map((m) => (
              <li key={m}>
                <label className='flex items-center gap-2 rounded-md border px-2.5 py-1.5 text-sm'>
                  <Checkbox
                    checked={selected.includes(m)}
                    onCheckedChange={(on) => toggle(m, on === true)}
                  />
                  <span className='truncate font-mono'>{m}</span>
                </label>
              </li>
            ))}
          </ul>
        )}

        <div className='flex flex-wrap items-start gap-2'>
          <div className='grid gap-1'>
            <Input
              value={entry}
              onChange={(e) => {
                setEntry(e.target.value)
                setEntryError(null)
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  add()
                }
              }}
              placeholder={t('Add a model name or prefix*')}
              aria-label={t('Add a model name or prefix*')}
              className='w-64'
            />
            {entryError !== null && (
              <p role='alert' className='text-destructive text-xs'>
                {entryText[entryError]}
              </p>
            )}
          </div>
          <Button variant='outline' onClick={add}>
            {t('Add')}
          </Button>
        </div>
      </div>

      <div className='flex items-center gap-2'>
        <Button onClick={submit} disabled={save.isPending || !dirty}>
          {save.isPending ? t('Saving...') : t('Save')}
        </Button>
        <Button
          variant='ghost'
          onClick={() => setSelected(start)}
          disabled={save.isPending || sameSet(selected, start)}
        >
          {t('Reset')}
        </Button>
      </div>

      <ConfirmDialog
        open={confirmEmpty}
        onOpenChange={setConfirmEmpty}
        title={t('Block every model?')}
        desc={t('With nothing selected, all requests from your organization will be refused until you select models again.')}
        confirmText={t('Block all')}
        destructive
        isLoading={save.isPending}
        handleConfirm={() => save.mutate([])}
      />
    </div>
  )
}

export function AllowlistSection() {
  const { t } = useTranslation()
  const q = useQuery(allowlistQueryOptions)
  if (q.isPending) return <LoadingState />
  if (q.isError) {
    return (
      <ErrorState
        title={t('Failed to load the model allow-list')}
        description={q.error.message}
        onRetry={() => void q.refetch()}
      />
    )
  }
  // key: remount the editor when the saved state changes, so the draft
  // restarts from what the server now holds.
  return (
    <Editor
      key={JSON.stringify([q.data.tenantConfigured, q.data.tenantSelected, q.data.platformAllowed])}
      data={q.data}
    />
  )
}
