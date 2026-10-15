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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { ListChecks, Plus, ShieldAlert, X } from 'lucide-react'
import { useState, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { TitledCard } from '@/components/ui/titled-card'
import { toApiError } from '@/lib/api'

import { clearAllowlist, modelLimitsQueryKey, saveAllowlist } from '../api'
import { entryProblem, type EntryProblem } from '../lib/allowlist'
import type { Allowlist } from '../types'
import { TypedConfirmDialog } from './typed-confirm-dialog'

interface AllowlistCardProps {
  tenantId: string
  loading: boolean
  /** Last successful read; null while loading or when the read failed. */
  data: Allowlist | null
  /** Bumps on every successful read so the draft re-seeds from the server. */
  version: number
  error: unknown
  onRetry: () => void
}

export function AllowlistCard(props: AllowlistCardProps) {
  const { t } = useTranslation()
  return (
    <TitledCard
      title={t('Model availability')}
      description={t(
        'Per-tenant model allow-list · the mode is set platform-wide by the operator, not per tenant'
      )}
      icon={<ListChecks className='size-4' />}
    >
      {props.loading ? (
        <LoadingState className='min-h-[120px]' />
      ) : (
        // Re-keyed per tenant and per successful read: a draft can never carry
        // over to another tenant, and a save/clear/retry re-seeds from truth.
        <AllowlistEditor
          key={`${props.tenantId}:${props.version}`}
          tenantId={props.tenantId}
          data={props.data}
          error={props.error}
          onRetry={props.onRetry}
        />
      )}
    </TitledCard>
  )
}

type Pending = 'save-empty' | 'clear' | null

function AllowlistEditor(props: {
  tenantId: string
  data: Allowlist | null
  error: unknown
  onRetry: () => void
}) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const { tenantId, data } = props
  // Unknown is not "observe" and it is not "unconfigured": the platform may be
  // in enforce mode with a restrictive row this page simply failed to read, so
  // every write control stays locked until a read succeeds. An empty draft must
  // never reach a PUT from here, or a read failure becomes a silent deny-all.
  const [draft, setDraft] = useState<string[]>(() => data?.allowed_models ?? [])
  const [entry, setEntry] = useState('')
  const [problem, setProblem] = useState<EntryProblem>(null)
  const [pending, setPending] = useState<Pending>(null)

  const refresh = () =>
    qc.invalidateQueries({ queryKey: [...modelLimitsQueryKey, 'allowlist'] })
  const fail = (e: unknown) => toast.error(toApiError(e).message)

  const save = useMutation({
    mutationFn: () => saveAllowlist(tenantId, draft),
    onSuccess: async () => {
      toast.success(t('Model allow-list saved'))
      setPending(null)
      await refresh()
    },
    onError: fail,
  })
  const clear = useMutation({
    mutationFn: () => clearAllowlist(tenantId),
    onSuccess: async () => {
      toast.success(t('Model allow-list cleared — tenant is unrestricted'))
      setPending(null)
      await refresh()
    },
    onError: fail,
  })

  const addEntry = () => {
    const value = entry.trim()
    if (!value) return
    const bad = entryProblem(value, draft)
    if (bad === 'duplicate') {
      // Re-adding what is already listed is a no-op, not an error.
      setEntry('')
      setProblem(null)
      return
    }
    if (bad) {
      setProblem(bad)
      return
    }
    setDraft((list) => [...list, value])
    setEntry('')
    setProblem(null)
  }
  const onEntryKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      addEntry()
    }
  }

  // Saving an explicitly empty list is a valid, deliberate deny-all, but it is
  // also the one-click way to lock every model out for the tenant: it goes
  // through the same typed confirmation as clearing the row.
  const requestSave = () => {
    if (draft.length === 0) setPending('save-empty')
    else save.mutate()
  }

  if (data === null) {
    return (
      <div className='grid gap-4'>
        <Alert variant='destructive' data-testid='allowlist-error'>
          <ShieldAlert />
          <AlertTitle>
            {t(
              'Could not read the allow-list for this tenant — state unknown, not editable.'
            )}
          </AlertTitle>
          <AlertDescription>
            <div className='flex flex-wrap items-center gap-2'>
              {props.error != null && (
                <span>{toApiError(props.error).message}</span>
              )}
              <Button variant='outline' size='sm' onClick={props.onRetry}>
                {t('Retry')}
              </Button>
            </div>
          </AlertDescription>
        </Alert>
        <div className='flex flex-wrap gap-2'>
          <Button disabled>{t('Save allow-list')}</Button>
          <Button variant='outline' disabled>
            {t('Clear (make unrestricted)')}
          </Button>
        </div>
      </div>
    )
  }

  const enforce = data.mode === 'enforce'
  const busy = save.isPending || clear.isPending
  let problemText = ''
  if (problem === 'wildcard') {
    problemText = t('Use * only as a trailing wildcard, e.g. model-a-*')
  } else if (problem === 'too-long') {
    problemText = t('Entries are limited to 128 characters.')
  }

  return (
    <div className='grid gap-4'>
      <div
        data-testid='allowlist-mode'
        data-mode={enforce ? 'enforce' : 'observe'}
        className='flex flex-wrap items-center gap-2 text-sm'
      >
        <Badge variant={enforce ? 'warning' : 'outline'}>
          {enforce ? t('Enforce') : t('Observe')}
        </Badge>
        <span className={enforce ? 'text-warning' : 'text-muted-foreground'}>
          {enforce
            ? t('a model off the list is refused (HTTP 403)')
            : t('a model off the list still answers; the miss is only counted')}
        </span>
      </div>
      {!data.configured && (
        <p
          data-testid='allowlist-unrestricted'
          className='text-muted-foreground text-sm'
        >
          {t(
            'No allow-list configured — every catalog model is reachable for this tenant.'
          )}
        </p>
      )}

      <ul
        className='flex flex-wrap gap-1.5'
        aria-label={t('Allowed models')}
        data-testid='allowlist-entries'
      >
        {draft.map((model) => (
          <li key={model}>
            <Badge variant='outline' className='h-6 gap-1 pr-1 font-mono'>
              {model}
              <button
                type='button'
                className='hover:bg-muted rounded-full p-0.5 disabled:opacity-50'
                aria-label={t('Remove {{model}}', { model })}
                disabled={busy}
                onClick={() =>
                  setDraft((list) => list.filter((m) => m !== model))
                }
              >
                <X className='size-3' />
              </button>
            </Badge>
          </li>
        ))}
      </ul>

      <div className='grid gap-1.5'>
        <div className='flex max-w-md gap-2'>
          <Input
            aria-label={t('Model name or prefix pattern')}
            aria-invalid={problem != null}
            className='font-mono'
            placeholder={t('model-a or model-a-*')}
            value={entry}
            onChange={(e) => {
              setEntry(e.target.value)
              setProblem(null)
            }}
            onKeyDown={onEntryKey}
          />
          <Button type='button' variant='outline' onClick={addEntry}>
            <Plus />
            {t('Add')}
          </Button>
        </div>
        {problem != null && (
          <p role='alert' className='text-destructive text-xs'>
            {problemText}
          </p>
        )}
      </div>

      {draft.length === 0 && (
        <p className='text-warning text-sm'>
          {t(
            'Saving an empty list denies every model for this tenant — it is not the same as leaving the allow-list unconfigured.'
          )}
        </p>
      )}

      <div className='flex flex-wrap gap-2'>
        <Button disabled={busy} onClick={requestSave}>
          {save.isPending ? t('Saving...') : t('Save allow-list')}
        </Button>
        <Button
          variant='outline'
          disabled={busy || !data.configured}
          onClick={() => setPending('clear')}
        >
          {t('Clear (make unrestricted)')}
        </Button>
      </div>

      <TypedConfirmDialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (!open) setPending(null)
        }}
        title={
          pending === 'clear'
            ? t('Remove the allow-list for {{tenant}}?', { tenant: tenantId })
            : t('Save an empty allow-list for {{tenant}}?', {
                tenant: tenantId,
              })
        }
        desc={
          pending === 'clear'
            ? t('The tenant becomes unrestricted.')
            : t('This denies every model.')
        }
        expected={tenantId}
        confirmText={t('Confirm')}
        isLoading={busy}
        handleConfirm={() => {
          if (pending === 'clear') clear.mutate()
          else save.mutate()
        }}
      />
    </div>
  )
}
