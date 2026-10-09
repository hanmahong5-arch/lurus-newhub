import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { isApiError, toApiError } from '@/lib/api'

import {
  channelHealthQueryOptions,
  channelsQueryKey,
  restoreChannelKey,
  testChannelKey,
  updateKeySettings,
  type KeySettingsBody,
} from '../api'
import {
  buildKeySettings,
  DEFAULT_KEY_WEIGHT,
  MAX_KEY_WEIGHT,
} from '../lib/key-form'
import { canRestore } from '../lib/map'
import type { ChannelDetail, KeyHealth, KeyProbeResult } from '../types'
import { Countdown, Field, ReasonBadges, WindowBars } from './shared'

interface Props {
  channelId: number
  detail: ChannelDetail | undefined
  canWrite: boolean
}

function keyWeight(k: KeyHealth, d: ChannelDetail | undefined): number {
  return d?.keyWeight[k.index] ?? k.weight ?? DEFAULT_KEY_WEIGHT
}

function keyProxy(k: KeyHealth, d: ChannelDetail | undefined): string {
  return d?.keyProxy[k.index] ?? ''
}

export function KeysTab(props: Props) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const health = useQuery(channelHealthQueryOptions(props.channelId))
  const [probes, setProbes] = useState<Record<number, KeyProbeResult>>({})
  const [busy, setBusy] = useState<number | null>(null)
  const [editing, setEditing] = useState<KeyHealth | null>(null)

  const refresh = () => qc.invalidateQueries({ queryKey: channelsQueryKey })
  const fail = (e: unknown) => {
    toast.error(toApiError(e).message)
  }
  const dropProbe = (idx: number) =>
    setProbes((p) => {
      const next = { ...p }
      delete next[idx]
      return next
    })

  const test = useMutation({
    mutationFn: (idx: number) => testChannelKey(props.channelId, idx),
    onMutate: (idx) => setBusy(idx),
    onSettled: () => setBusy(null),
    onSuccess: (r, idx) => setProbes((p) => ({ ...p, [idx]: r })),
    onError: fail,
  })

  const restore = useMutation({
    mutationFn: (v: { idx: number; proof: string }) =>
      restoreChannelKey(props.channelId, v.idx, v.proof),
    onMutate: (v) => setBusy(v.idx),
    onSettled: () => setBusy(null),
    onSuccess: async (_d, v) => {
      toast.success(t('Key restored'))
      dropProbe(v.idx)
      await refresh()
    },
    onError: (e, v) => {
      // proof_stale: the key state moved on, so the old proof can never work.
      if (isApiError(e) && e.code === 'proof_stale') {
        dropProbe(v.idx)
        void refresh()
      }
      fail(e)
    },
  })

  const save = useMutation({
    mutationFn: (v: { idx: number; body: KeySettingsBody }) =>
      updateKeySettings(props.channelId, v.idx, v.body),
    onSuccess: async () => {
      toast.success(t('Saved'))
      setEditing(null)
      await refresh()
    },
    onError: fail,
  })

  if (health.isPending) return <LoadingState />
  if (health.isError) {
    return (
      <ErrorState
        title={t('Could not load channel keys')}
        description={toApiError(health.error).message}
        onRetry={() => void health.refetch()}
      />
    )
  }
  const keys = health.data.keys
  const multi = props.detail?.isMultiKey === true
  const nowSec = Math.floor(Date.now() / 1000)

  return (
    <div className='grid gap-3' data-testid='keys-tab'>
      {props.detail != null && !multi && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'This channel holds a single key. Weight and proxy overrides need a multi-key channel.'
          )}
        </p>
      )}
      {keys.length === 0 && (
        <p className='text-muted-foreground text-sm'>
          {t('This channel has no keys.')}
        </p>
      )}
      {keys.map((k) => {
        const probe = probes[k.index]
        const proxy = keyProxy(k, props.detail)
        const rowBusy = busy === k.index
        return (
          <div
            key={k.index}
            className='grid gap-2 rounded-lg border p-3'
            data-testid={`key-${k.index}`}
          >
            <div className='flex flex-wrap items-center gap-2'>
              <span className='font-medium'>
                {k.name !== ''
                  ? k.name
                  : t('Key #{{index}}', { index: k.index })}
              </span>
              <code className='text-muted-foreground font-mono text-xs'>
                {k.fingerprint}
              </code>
              <Badge variant={k.routable ? 'default' : 'destructive'}>
                {k.routable ? t('Routable') : t('Not routable')}
              </Badge>
              {k.cooldownUntil > 0 && <Countdown until={k.cooldownUntil} />}
            </div>
            {!k.routable && <ReasonBadges reasons={k.reasons} />}
            {k.lastError !== '' && (
              <p className='text-muted-foreground text-xs break-all'>
                {k.lastError}
              </p>
            )}
            <WindowBars windows={k.windows} />
            <dl className='grid grid-cols-2 gap-2 text-xs'>
              <div>
                <dt className='text-muted-foreground'>{t('Weight')}</dt>
                <dd data-testid={`key-${k.index}-weight`}>
                  {multi ? keyWeight(k, props.detail) : '--'}
                </dd>
              </div>
              <div className='min-w-0'>
                <dt className='text-muted-foreground'>{t('Proxy')}</dt>
                <dd className='break-all' data-testid={`key-${k.index}-proxy`}>
                  {proxy !== '' ? proxy : t(k.hasProxy ? 'Set' : 'None')}
                </dd>
              </div>
            </dl>

            {probe != null && (
              <p
                role='status'
                className={
                  probe.ok
                    ? 'text-success text-sm'
                    : 'text-destructive text-sm'
                }
                data-testid={`key-${k.index}-probe`}
              >
                {probe.ok
                  ? t('Test passed ({{ms}} ms)', { ms: probe.latencyMs })
                  : t('Test failed: {{message}}', { message: probe.error })}
              </p>
            )}

            {props.canWrite && (
              <div className='flex flex-wrap items-center gap-2'>
                <Button
                  size='sm'
                  variant='outline'
                  disabled={rowBusy}
                  onClick={() => test.mutate(k.index)}
                >
                  {t('Test key')}
                </Button>
                {probe != null && canRestore(probe, nowSec) && (
                  <Button
                    size='sm'
                    disabled={rowBusy}
                    onClick={() =>
                      restore.mutate({
                        idx: k.index,
                        proof: probe.restoreProof,
                      })
                    }
                  >
                    {t('Restore with proof')}
                  </Button>
                )}
                {multi && (
                  <Button
                    size='sm'
                    variant='ghost'
                    onClick={() => setEditing(k)}
                    aria-label={t('Edit key #{{index}}', { index: k.index })}
                  >
                    <Pencil />
                    {t('Edit')}
                  </Button>
                )}
              </div>
            )}
          </div>
        )
      })}

      {editing != null && (
        <KeySettingsDialog
          key={editing.index}
          keyHealth={editing}
          weight={keyWeight(editing, props.detail)}
          proxy={keyProxy(editing, props.detail)}
          saving={save.isPending}
          onSubmit={(body) => save.mutate({ idx: editing.index, body })}
          onClose={() => setEditing(null)}
        />
      )}
    </div>
  )
}

function KeySettingsDialog(props: {
  keyHealth: KeyHealth
  weight: number
  proxy: string
  saving: boolean
  onSubmit: (body: KeySettingsBody) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [weight, setWeight] = useState(String(props.weight))
  const [proxy, setProxy] = useState(props.proxy)
  const [error, setError] = useState(false)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (props.saving) return
    const built = buildKeySettings(
      { weight, proxy },
      { weight: props.weight, proxy: props.proxy }
    )
    if (!built.ok) {
      setError(true)
      return
    }
    if (Object.keys(built.body).length === 0) {
      props.onClose()
      return
    }
    props.onSubmit(built.body)
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !props.saving) props.onClose()
      }}
    >
      <DialogContent className='sm:max-w-md'>
        <DialogHeader>
          <DialogTitle>
            {t('Edit key #{{index}}', { index: props.keyHealth.index })}
          </DialogTitle>
          <DialogDescription>
            {t('Changes apply to new requests right away.')}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className='grid gap-4' noValidate>
          <Field
            label={t('Weight')}
            hint={t('0 to {{max}}. 0 means this key is never picked.', {
              max: MAX_KEY_WEIGHT,
            })}
          >
            <Input
              inputMode='numeric'
              value={weight}
              onChange={(e) => {
                setWeight(e.target.value)
                setError(false)
              }}
              aria-label={t('Weight')}
            />
          </Field>
          <Field
            label={t('Proxy URL')}
            hint={t(
              'Overrides the channel proxy for this key. The address goes through a safety check on save, so internal addresses are rejected. Empty clears the override.'
            )}
          >
            <Input
              value={proxy}
              onChange={(e) => setProxy(e.target.value)}
              placeholder='http://proxy.example.com:8080'
              aria-label={t('Proxy URL')}
            />
          </Field>
          {error && (
            <p role='alert' className='text-destructive text-sm'>
              {t('Enter a whole number from 0 to {{max}}.', {
                max: MAX_KEY_WEIGHT,
              })}
            </p>
          )}
          <DialogFooter>
            <Button
              type='button'
              variant='outline'
              onClick={props.onClose}
              disabled={props.saving}
            >
              {t('Cancel')}
            </Button>
            <Button type='submit' disabled={props.saving}>
              {t('Save')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
