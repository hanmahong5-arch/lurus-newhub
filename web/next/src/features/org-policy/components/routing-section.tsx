import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { toApiError } from '@/lib/api'

import {
  deleteRoutingPolicy,
  policyKey,
  routableModelIdsQueryOptions,
  routingPolicyQueryOptions,
  saveRoutingPolicy,
} from '../api'
import {
  MAX_CANDIDATES,
  MAX_CRITERIA_BYTES,
  MAX_INSTRUCTIONS_BYTES,
  addressable,
  blankCandidate,
  byteLength,
  emptyDraft,
  validateDraft,
  type RoutingCandidate,
  type RoutingDraft,
  type RoutingError,
} from '../lib/routing'

function useErrorText() {
  const { t } = useTranslation()
  return (e: RoutingError): string => {
    const n = 'index' in e ? e.index + 1 : 0
    switch (e.code) {
      case 'candidates-count':
        return t('A policy needs between 1 and {{max}} candidates.', {
          max: MAX_CANDIDATES,
        })
      case 'min-confidence':
        return t('Confidence threshold must be a number from 0 to 1.')
      case 'instructions-too-long':
        return t('Instructions can be at most {{max}} bytes.', {
          max: MAX_INSTRUCTIONS_BYTES,
        })
      case 'evaluator-too-long':
        return t('The evaluator model name is too long.')
      case 'evaluator-required':
        return t('Choose an evaluator model before enabling the policy.')
      case 'candidate-id':
        return t(
          'Candidate {{n}}: the id must be letters, digits, "_", "." or "-", up to 64 characters.',
          { n }
        )
      case 'candidate-id-reserved':
        return t('Candidate {{n}}: this id is reserved.', { n })
      case 'candidate-id-duplicate':
        return t('Candidate {{n}}: the id is already used.', { n })
      case 'candidate-model':
        return t('Candidate {{n}}: enter a model name (up to 128 bytes).', {
          n,
        })
      case 'criteria-required':
        return t(
          'Candidate {{n}}: describe when this candidate should be chosen.',
          { n }
        )
      case 'criteria-too-long':
        return t('Candidate {{n}}: the criteria can be at most {{max}} bytes.', {
          n,
          max: MAX_CRITERIA_BYTES,
        })
      case 'default-unknown':
        return t('The default candidate must be one of the candidate ids.')
      case 'default-required':
        return t(
          'Pick a default candidate, or add this model itself as a candidate.'
        )
    }
  }
}

function errorKey(e: RoutingError): string {
  return 'index' in e ? `${e.code}:${e.index}` : e.code
}

function PolicyForm(props: { model: string; stored: RoutingDraft | null }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const errorText = useErrorText()
  const { model, stored } = props
  const [draft, setDraft] = useState<RoutingDraft>(
    () => stored ?? emptyDraft(model)
  )
  const [errors, setErrors] = useState<RoutingError[]>([])
  const [confirmEnable, setConfirmEnable] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)

  const done = async (msg: string) => {
    toast.success(msg)
    setConfirmEnable(false)
    setConfirmDelete(false)
    await qc.invalidateQueries({ queryKey: policyKey })
  }
  const fail = (e: unknown) => {
    setConfirmEnable(false)
    setConfirmDelete(false)
    toast.error(toApiError(e).message)
  }
  const save = useMutation({
    mutationFn: () => saveRoutingPolicy(model, draft),
    onSuccess: () => done(t('Saved')),
    onError: fail,
  })
  const remove = useMutation({
    mutationFn: () => deleteRoutingPolicy(model),
    onSuccess: () => done(t('Policy deleted')),
    onError: fail,
  })

  const set = <K extends keyof RoutingDraft>(k: K, v: RoutingDraft[K]) =>
    setDraft((d) => ({ ...d, [k]: v }))
  const setCand = (i: number, patch: Partial<RoutingCandidate>) =>
    setDraft((d) => ({
      ...d,
      candidates: d.candidates.map((c, j) =>
        j === i ? { ...c, ...patch } : c
      ),
    }))

  const submit = () => {
    const errs = validateDraft(draft, model)
    setErrors(errs)
    if (errs.length > 0) return
    // Turning routing on rewrites live requests: ask once.
    if (draft.enabled && !(stored?.enabled ?? false)) {
      setConfirmEnable(true)
      return
    }
    save.mutate()
  }

  const busy = save.isPending || remove.isPending
  const atLimit = draft.candidates.length >= MAX_CANDIDATES

  return (
    <div className='space-y-5' data-testid='routing-editor'>
      <div className='flex items-center justify-between gap-3 rounded-lg border p-3'>
        <div>
          <Label>{t('Enabled')}</Label>
          <p className='text-muted-foreground text-xs'>
            {t('While off, requests for this model are not rewritten.')}
          </p>
        </div>
        <Switch
          checked={draft.enabled}
          onCheckedChange={(v) => set('enabled', v)}
          aria-label={t('Enabled')}
        />
      </div>

      <div className='grid gap-4 md:grid-cols-2'>
        <div className='grid gap-1.5'>
          <Label htmlFor='routing-evaluator'>{t('Evaluator model')}</Label>
          <Input
            id='routing-evaluator'
            value={draft.evaluatorModel}
            onChange={(e) => set('evaluatorModel', e.target.value)}
            className='font-mono'
          />
        </div>
        <div className='grid gap-1.5'>
          <Label htmlFor='routing-confidence'>
            {t('Confidence threshold')}
          </Label>
          <Input
            id='routing-confidence'
            inputMode='decimal'
            value={draft.minConfidence}
            onChange={(e) => set('minConfidence', e.target.value)}
          />
          <p className='text-muted-foreground text-xs'>
            {t('Between 0 and 1. Below it the default candidate is used.')}
          </p>
        </div>
      </div>

      <div className='grid gap-1.5'>
        <Label htmlFor='routing-instructions'>{t('Instructions')}</Label>
        <Textarea
          id='routing-instructions'
          value={draft.instructions}
          onChange={(e) => set('instructions', e.target.value)}
        />
        <p className='text-muted-foreground text-xs'>
          {byteLength(draft.instructions)} / {MAX_INSTRUCTIONS_BYTES}{' '}
          {t('bytes')}
        </p>
      </div>

      <div className='space-y-3'>
        <div className='flex items-center justify-between'>
          <h3 className='text-sm font-medium'>
            {t('Candidates')} ({draft.candidates.length}/{MAX_CANDIDATES})
          </h3>
          <Button
            variant='outline'
            size='sm'
            disabled={atLimit}
            onClick={() =>
              set('candidates', [...draft.candidates, blankCandidate()])
            }
          >
            {t('Add candidate')}
          </Button>
        </div>
        <ul className='space-y-3'>
          {draft.candidates.map((c, i) => (
            <li
              key={c.uid}
              className='space-y-2 rounded-lg border p-3'
              data-testid={`routing-candidate-${i}`}
            >
              <div className='grid gap-2 md:grid-cols-[1fr_1fr_auto]'>
                <Input
                  value={c.id}
                  onChange={(e) => setCand(i, { id: e.target.value })}
                  placeholder={t('Candidate id')}
                  aria-label={t('Candidate {{n}} id', { n: i + 1 })}
                  className='font-mono'
                />
                <Input
                  value={c.model}
                  onChange={(e) => setCand(i, { model: e.target.value })}
                  placeholder={t('Model')}
                  aria-label={t('Candidate {{n}} model', { n: i + 1 })}
                  className='font-mono'
                />
                <Button
                  variant='ghost'
                  size='sm'
                  disabled={draft.candidates.length <= 1}
                  onClick={() =>
                    set(
                      'candidates',
                      draft.candidates.filter((_, j) => j !== i)
                    )
                  }
                  aria-label={t('Remove candidate {{n}}', { n: i + 1 })}
                >
                  {t('Remove')}
                </Button>
              </div>
              <Textarea
                value={c.criteria}
                onChange={(e) => setCand(i, { criteria: e.target.value })}
                placeholder={t('When should this candidate be chosen?')}
                aria-label={t('Candidate {{n}} criteria', { n: i + 1 })}
              />
              <p className='text-muted-foreground text-xs'>
                {byteLength(c.criteria)} / {MAX_CRITERIA_BYTES} {t('bytes')}
              </p>
            </li>
          ))}
        </ul>
      </div>

      <div className='grid gap-1.5 md:w-1/2'>
        <Label htmlFor='routing-default'>{t('Default candidate')}</Label>
        <select
          id='routing-default'
          value={draft.defaultCandidate}
          onChange={(e) => set('defaultCandidate', e.target.value)}
          className='border-input h-9 rounded-lg border bg-transparent px-2 text-sm'
        >
          <option value=''>{t('The requested model itself')}</option>
          {draft.candidates
            .filter((c) => c.id !== '')
            .map((c) => (
              <option key={c.uid} value={c.id}>
                {c.id}
              </option>
            ))}
        </select>
        <p className='text-muted-foreground text-xs'>
          {t('Used when the evaluator cannot decide or is not confident.')}
        </p>
      </div>

      {errors.length > 0 && (
        <ul role='alert' className='text-destructive space-y-0.5 text-sm'>
          {errors.map((e) => (
            <li key={errorKey(e)}>{errorText(e)}</li>
          ))}
        </ul>
      )}

      <div className='flex items-center gap-2'>
        <Button onClick={submit} disabled={busy}>
          {save.isPending ? t('Saving...') : t('Save')}
        </Button>
        {stored !== null && (
          <Button
            variant='outline'
            disabled={busy}
            onClick={() => setConfirmDelete(true)}
          >
            {t('Delete policy')}
          </Button>
        )}
      </div>

      <ConfirmDialog
        open={confirmEnable}
        onOpenChange={setConfirmEnable}
        title={t('Enable decision routing?')}
        desc={t(
          'Requests for "{{model}}" will be sent to the evaluator and may be rewritten to another model. Every rewrite is announced in response headers and audited.',
          { model }
        )}
        confirmText={t('Enable')}
        isLoading={save.isPending}
        handleConfirm={() => save.mutate()}
      />
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={t('Delete this policy?')}
        desc={t(
          'Requests for "{{model}}" will no longer be routed by a decision model.',
          { model }
        )}
        confirmText={t('Delete')}
        destructive
        isLoading={remove.isPending}
        handleConfirm={() => remove.mutate()}
      />
    </div>
  )
}

function PolicyEditor(props: { model: string }) {
  const { t } = useTranslation()
  const q = useQuery(routingPolicyQueryOptions(props.model))
  if (q.isPending) return <LoadingState />
  if (q.isError) {
    return (
      <ErrorState
        title={t('Failed to load the routing policy')}
        description={q.error.message}
        onRetry={() => void q.refetch()}
      />
    )
  }
  // Remount when the stored policy changes so the draft restarts from it.
  return (
    <PolicyForm
      key={`${props.model}:${JSON.stringify(q.data)}`}
      model={props.model}
      stored={q.data}
    />
  )
}

export function RoutingSection() {
  const { t } = useTranslation()
  const models = useQuery(routableModelIdsQueryOptions)
  const [selected, setSelected] = useState<string | null>(null)

  if (models.isPending) return <LoadingState />
  if (models.isError) {
    return (
      <ErrorState
        title={t('Failed to load the model list')}
        description={models.error.message}
        onRetry={() => void models.refetch()}
      />
    )
  }
  const list = models.data
  return (
    <div className='space-y-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Let a decision model pick which candidate serves a request for a public model. Off by default; each rewrite is announced and audited.'
        )}
      </p>
      {list.length === 0 ? (
        <p className='text-muted-foreground text-sm'>
          {t('No models to choose from.')}
        </p>
      ) : (
        <ul className='flex flex-wrap gap-2'>
          {list.map((m) => (
            <li key={m}>
              <Button
                variant={selected === m ? 'default' : 'outline'}
                size='sm'
                className='font-mono'
                disabled={!addressable(m)}
                title={
                  addressable(m)
                    ? undefined
                    : t('Model names containing "/" cannot be configured here.')
                }
                onClick={() => setSelected(m)}
              >
                {m}
              </Button>
            </li>
          ))}
        </ul>
      )}
      {selected !== null && <PolicyEditor key={selected} model={selected} />}
    </div>
  )
}
