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
import { Link } from '@tanstack/react-router'
import { Check } from 'lucide-react'
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { formatQuota } from '@/lib/money'
import { useMoneyConfig } from '@/lib/status'

import { createToken } from '../api'
import {
  TOKEN_DRAFT_INIT,
  TOKEN_NAME_MAX_BYTES,
  buildTokenBody,
  capUnitLabel,
  clearTokenDraft,
  dateAfterDays,
  describeTokenError,
  loadTokenDraft,
  saveTokenDraft,
  validateLimits,
  validateScope,
  type LimitsIssue,
  type ScopeIssue,
  type TokenDraft,
} from '../lib/token'
import { Field, ReviewList, StepCard, StepError } from './step-card'
import { Stepper } from './stepper'

const TOTAL_STEPS = 3
/** Query-key root of the Keys page, so its list refetches after an issue. */
const KEYS_QUERY_KEY = ['keys'] as const

interface TokenWizardProps {
  /** False while another flow tab is showing; clears a lingering secret. */
  active: boolean
}

/**
 * Three-step "issue a key" wizard: name and scope, limits, review. The review
 * step opens a typed-name confirmation; the issued secret is shown exactly
 * once on the success screen and is dropped from state when that screen is
 * left (or the tab is switched away).
 */
export function TokenWizard(props: TokenWizardProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const money = useMoneyConfig()
  const uid = useId()

  const [step, setStep] = useState(1)
  const [draft, setDraft] = useState<TokenDraft>(loadTokenDraft)
  const [stepError, setStepError] = useState<string | null>(null)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [typedName, setTypedName] = useState('')
  const [issuedKey, setIssuedKey] = useState<string | null>(null)

  // Only the form values are persisted (never the issued secret).
  useEffect(() => {
    if (issuedKey === null) saveTokenDraft(draft)
  }, [draft, issuedKey])

  const reset = () => {
    clearTokenDraft()
    setDraft(TOKEN_DRAFT_INIT)
    setIssuedKey(null)
    setStep(1)
    setStepError(null)
    setSubmitError(null)
    setConfirmOpen(false)
    setTypedName('')
  }

  // Leaving the tab with the secret on screen discards it.
  useEffect(() => {
    if (!props.active && issuedKey !== null) reset()
    // reset only touches state setters; re-running on `issuedKey` is intended.
    // oxlint-disable-next-line react/exhaustive-deps
  }, [props.active, issuedKey])

  const issue = useMutation({
    mutationFn: createToken,
    onSuccess: (issued) => {
      clearTokenDraft()
      setIssuedKey(issued.key)
      setConfirmOpen(false)
      setTypedName('')
      void queryClient.invalidateQueries({ queryKey: KEYS_QUERY_KEY })
    },
    onError: (e) => {
      const message = describeTokenError(e, t)
      setSubmitError(message)
      setConfirmOpen(false)
      toast.error(message)
    },
  })

  const set = <K extends keyof TokenDraft>(k: K, v: TokenDraft[K]) => {
    setDraft((d) => ({ ...d, [k]: v }))
    setStepError(null)
    setSubmitError(null)
  }

  const scopeMessage: Record<ScopeIssue, string> = {
    name_required: t('Enter a name for the key.'),
    name_too_long: t(
      'The name is longer than {{max}} bytes (a Chinese character takes 3).',
      {
        max: TOKEN_NAME_MAX_BYTES,
      }
    ),
  }
  const limitsMessage: Record<LimitsIssue, string> = {
    cap_unavailable: t(
      'The currency settings have not loaded, so a quota cannot be set. Reload the page.'
    ),
    cap_invalid: t('Enter a quota greater than zero, such as 5 or 12.50.'),
    cap_too_large: t('The quota exceeds the maximum allowed.'),
    expiry_invalid: t('Enter a valid expiry date.'),
    expiry_past: t('The expiry date must be in the future.'),
  }

  const nowSec = () => Math.floor(Date.now() / 1000)

  const goNextFromScope = () => {
    const problem = validateScope(draft)
    if (problem !== null) {
      setStepError(scopeMessage[problem])
      return
    }
    setStepError(null)
    setStep(2)
  }

  const goNextFromLimits = () => {
    const limits = validateLimits(draft, money, nowSec())
    if (!limits.ok) {
      setStepError(limitsMessage[limits.issue])
      return
    }
    setStepError(null)
    setStep(3)
  }

  const submit = () => {
    if (issue.isPending) return
    const scope = validateScope(draft)
    const limits = validateLimits(draft, money, nowSec())
    if (scope !== null) {
      setConfirmOpen(false)
      setStep(1)
      setStepError(scopeMessage[scope])
      return
    }
    if (!limits.ok) {
      setConfirmOpen(false)
      setStep(2)
      setStepError(limitsMessage[limits.issue])
      return
    }
    setSubmitError(null)
    issue.mutate(buildTokenBody(draft, limits))
  }

  const stepLabels = [t('Name and scope'), t('Limits'), t('Review')]
  const unit = capUnitLabel(money)

  if (issuedKey !== null) {
    return (
      <div className='flex flex-col gap-6' data-testid='newtoken-wizard'>
        <Stepper steps={stepLabels} current={TOTAL_STEPS + 1} />
        <section
          className='flex flex-col items-center gap-4 py-6 text-center'
          data-testid='newtoken-success'
        >
          <span className='flex size-14 items-center justify-center rounded-full bg-emerald-600 text-white'>
            <Check className='size-7' aria-hidden='true' />
          </span>
          <div className='space-y-1'>
            <h2 className='text-xl font-semibold tracking-tight'>
              {t('Key created')}
            </h2>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Copy the key now; it will not be shown again after you leave this page.'
              )}
            </p>
          </div>
          <div className='flex max-w-full items-center gap-2 rounded-md border p-2'>
            <code
              data-testid='newtoken-key'
              className='min-w-0 font-mono text-sm break-all'
            >
              {issuedKey}
            </code>
            <CopyButton value={issuedKey} aria-label={t('Copy key')} />
          </div>
          <div className='flex flex-wrap items-center justify-center gap-2'>
            <Button
              type='button'
              data-testid='newtoken-another-btn'
              onClick={reset}
            >
              {t('Create another key')}
            </Button>
            <Button variant='outline' render={<Link to='/keys' />}>
              {t('Go to Keys')}
            </Button>
          </div>
        </section>
      </div>
    )
  }

  const limitsPreview = validateLimits(draft, money, nowSec())
  let quotaText = draft.cap.trim()
  if (draft.unlimited) quotaText = t('Unlimited')
  else if (limitsPreview.ok) quotaText = formatQuota(limitsPreview.quota, money)
  const nameMatches = typedName.trim() === draft.name.trim()

  return (
    <div className='flex flex-col gap-6' data-testid='newtoken-wizard'>
      <Stepper steps={stepLabels} current={step} />

      {step === 1 && (
        <StepCard
          step={1}
          total={TOTAL_STEPS}
          title={t('What is this key for?')}
          description={t(
            'Give it a recognisable name and, optionally, a group.'
          )}
          footer={
            <Button
              type='button'
              data-testid='newtoken-next'
              onClick={goNextFromScope}
            >
              {t('Next step')}
            </Button>
          }
        >
          <div className='grid gap-4 rounded-lg border p-4'>
            <Field label={t('Key name')} htmlFor={`${uid}-name`}>
              <Input
                id={`${uid}-name`}
                data-testid='newtoken-name'
                value={draft.name}
                placeholder={t('e.g. service-a · prod')}
                aria-invalid={stepError !== null}
                onChange={(e) => set('name', e.target.value)}
              />
            </Field>
            <Field
              label={t('Group (optional)')}
              htmlFor={`${uid}-group`}
              hint={t('Leave empty to use the default group.')}
            >
              <Input
                id={`${uid}-group`}
                data-testid='newtoken-group'
                value={draft.group}
                placeholder='default'
                onChange={(e) => set('group', e.target.value)}
              />
            </Field>
          </div>
          {stepError !== null && (
            <StepError data-testid='newtoken-step-error'>{stepError}</StepError>
          )}
        </StepCard>
      )}

      {step === 2 && (
        <StepCard
          step={2}
          total={TOTAL_STEPS}
          title={t('Set limits')}
          footer={
            <>
              <Button
                type='button'
                variant='outline'
                data-testid='newtoken-back'
                onClick={() => {
                  setStepError(null)
                  setStep(1)
                }}
              >
                {t('Back')}
              </Button>
              <Button
                type='button'
                data-testid='newtoken-next'
                onClick={goNextFromLimits}
              >
                {t('Next step')}
              </Button>
            </>
          }
        >
          <div className='grid gap-4 rounded-lg border p-4'>
            <div className='flex items-center justify-between gap-4'>
              <label
                htmlFor={`${uid}-unlimited`}
                className='text-sm font-medium'
              >
                {t('Unlimited quota')}
              </label>
              <Switch
                id={`${uid}-unlimited`}
                data-testid='newtoken-unlimited'
                checked={draft.unlimited}
                onCheckedChange={(v) => set('unlimited', v)}
              />
            </div>
            {!draft.unlimited && (
              <Field
                label={
                  unit === '' ? t('Quota') : t('Quota ({{unit}})', { unit })
                }
                htmlFor={`${uid}-quota`}
                hint={t(
                  'The total this key may spend, in your display currency.'
                )}
              >
                <Input
                  id={`${uid}-quota`}
                  data-testid='newtoken-quota'
                  inputMode='decimal'
                  value={draft.cap}
                  placeholder='5'
                  aria-invalid={stepError !== null}
                  onChange={(e) => set('cap', e.target.value)}
                />
              </Field>
            )}
            <Field
              label={t('Expires on (leave empty for never)')}
              htmlFor={`${uid}-expires`}
              hint={t('The key stops working at the end of that day.')}
            >
              <Input
                id={`${uid}-expires`}
                type='date'
                data-testid='newtoken-expires'
                value={draft.expires}
                aria-invalid={stepError !== null}
                onChange={(e) => set('expires', e.target.value)}
              />
              <div className='flex flex-wrap gap-1.5'>
                {(
                  [
                    [t('Never'), ''],
                    [t('30 days'), dateAfterDays(new Date(), 30)],
                    [t('90 days'), dateAfterDays(new Date(), 90)],
                    [t('1 year'), dateAfterDays(new Date(), 365)],
                  ] as const
                ).map(([label, value]) => (
                  <Button
                    key={label}
                    type='button'
                    size='sm'
                    variant={draft.expires === value ? 'secondary' : 'outline'}
                    onClick={() => set('expires', value)}
                  >
                    {label}
                  </Button>
                ))}
              </div>
            </Field>
          </div>
          {stepError !== null && (
            <StepError data-testid='newtoken-step-error'>{stepError}</StepError>
          )}
        </StepCard>
      )}

      {step === 3 && (
        <StepCard
          step={3}
          total={TOTAL_STEPS}
          title={t('Review key')}
          footer={
            <>
              <Button
                type='button'
                variant='outline'
                data-testid='newtoken-back'
                disabled={issue.isPending}
                onClick={() => setStep(2)}
              >
                {t('Back')}
              </Button>
              <Button
                type='button'
                data-testid='newtoken-open-confirm'
                disabled={issue.isPending}
                onClick={() => {
                  setTypedName('')
                  setConfirmOpen(true)
                }}
              >
                {t('Create key')}
              </Button>
            </>
          }
        >
          <ReviewList
            data-testid='newtoken-review'
            rows={[
              { label: t('Name'), value: draft.name.trim() || '—' },
              {
                label: t('Group'),
                value: draft.group.trim() || 'default',
              },
              { label: t('Quota'), value: quotaText },
              {
                label: t('Expires'),
                value: draft.expires === '' ? t('Never') : draft.expires,
              },
            ]}
          />
          {submitError !== null && (
            <StepError data-testid='newtoken-create-error'>
              {submitError}
            </StepError>
          )}
          <ConfirmDialog
            open={confirmOpen}
            onOpenChange={(open) => {
              if (!issue.isPending) setConfirmOpen(open)
            }}
            title={t('Confirm key creation')}
            desc={
              <ul className='list-disc space-y-1 pl-5'>
                <li>{t('The key is shown only once after creation.')}</li>
                <li>
                  {t('Once created it counts against your quota immediately.')}
                </li>
              </ul>
            }
            confirmText={t('Create key')}
            disabled={!nameMatches}
            isLoading={issue.isPending}
            handleConfirm={submit}
          >
            <div className='grid gap-1.5'>
              <label htmlFor={`${uid}-confirm`} className='text-sm'>
                {t('Type the key name to confirm: {{name}}', {
                  name: draft.name.trim(),
                })}
              </label>
              <Input
                id={`${uid}-confirm`}
                data-testid='newtoken-confirm-input'
                value={typedName}
                autoComplete='off'
                onChange={(e) => setTypedName(e.target.value)}
              />
            </div>
          </ConfirmDialog>
        </StepCard>
      )}
    </div>
  )
}
