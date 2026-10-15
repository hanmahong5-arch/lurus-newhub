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
import type { TFunction } from 'i18next'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { cn } from '@/lib/utils'

import {
  createChannel,
  fetchUpstreamModels,
  testChannel,
  updateChannelModels,
} from '../api'
import {
  CHANNEL_FORM_INIT,
  baseUrlAfterVendorPick,
  buildChannelBody,
  canCreateChannel,
  describeChannelError,
  mergeModels,
  needsBaseUrl,
  otherKind,
  splitModels,
  validateChannelForm,
  validationMessage,
  type ChannelForm,
} from '../lib/channel'
import {
  VENDOR_CHOICES,
  type VendorChoice,
  type VendorHint,
} from '../lib/vendors'
import type { ChannelTestResult, UpstreamModels } from '../types'
import { Field, ReviewList, StepCard, StepError } from './step-card'
import { Stepper } from './stepper'

const TOTAL_STEPS = 4

/** The channel as persisted at the step 2 -> 3 transition. */
interface PersistedChannel {
  id: number
  name: string
  models: string
}

/** Channel list query-key root, so the Channels page refetches after a create. */
const CHANNELS_QUERY_KEY = ['ops-channels'] as const

function vendorHintText(hint: VendorHint, t: TFunction): string {
  if (hint === 'oai') return t('OAI-compatible')
  if (hint === 'custom') return t('OAI-compatible URL')
  return t('Native protocol')
}

function testResultText(r: ChannelTestResult, t: TFunction): string {
  if (r.success) {
    return r.latencyMs === null
      ? t('Reachable')
      : t('Reachable · {{ms}}ms', { ms: r.latencyMs })
  }
  return r.error === ''
    ? t('Unreachable')
    : t('Unreachable: {{error}}', { error: r.error })
}

/**
 * Four-step "connect a vendor" wizard: vendor, credentials, models, review.
 *
 * Three real backend calls drive it: POST /channels (create, step 2 -> 3),
 * GET /channels/:id/upstream-models (discovery, step 3) and POST
 * /channels/:id/test (connection test, step 4). Discovery and test both read
 * the stored key server-side, which is why the channel is created at the
 * step 2 -> 3 transition and not at the end of the wizard.
 */
export function ChannelWizard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const uid = useId()

  const [step, setStep] = useState(1)
  const [form, setForm] = useState<ChannelForm>(CHANNEL_FORM_INIT)
  // Whether the person ever typed into the base url themselves; see
  // baseUrlAfterVendorPick.
  const [baseUrlTouched, setBaseUrlTouched] = useState(false)
  const [created, setCreated] = useState<PersistedChannel | null>(null)
  const [createError, setCreateError] = useState<string | null>(null)
  const [discovery, setDiscovery] = useState<UpstreamModels | null>(null)
  const [discoveryError, setDiscoveryError] = useState<string | null>(null)
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set())
  const [testResult, setTestResult] = useState<ChannelTestResult | null>(null)

  const locked = created !== null

  const create = useMutation({
    mutationFn: createChannel,
    onMutate: () => setCreateError(null),
    onSuccess: (channel, body) => {
      setCreated({ id: channel.id, name: channel.name, models: body.models })
      // The upstream key was sent once; do not keep holding it in the page.
      setForm((f) => ({ ...f, key: '' }))
      void queryClient.invalidateQueries({ queryKey: CHANNELS_QUERY_KEY })
      setStep(3)
    },
    onError: (e) => setCreateError(describeChannelError(e, t)),
  })

  const discover = useMutation({
    mutationFn: fetchUpstreamModels,
    onMutate: () => {
      setDiscoveryError(null)
      setDiscovery(null)
    },
    onSuccess: (d) => {
      setDiscovery(d)
      setSelected(new Set(d.new))
    },
    onError: (e) => setDiscoveryError(describeChannelError(e, t)),
  })

  const apply = useMutation({
    mutationFn: (v: { id: number; models: string }) =>
      updateChannelModels(v.id, v.models),
    onMutate: () => setDiscoveryError(null),
    onSuccess: (_data, v) => {
      setCreated((c) => (c === null ? c : { ...c, models: v.models }))
      void queryClient.invalidateQueries({ queryKey: CHANNELS_QUERY_KEY })
      setStep(4)
    },
    onError: (e) => setDiscoveryError(describeChannelError(e, t)),
  })

  const test = useMutation({
    mutationFn: testChannel,
    onMutate: () => setTestResult(null),
    onSuccess: (r) => setTestResult(r),
    onError: (e) =>
      setTestResult({
        success: false,
        latencyMs: null,
        error: describeChannelError(e, t),
      }),
  })

  const setField = <K extends keyof ChannelForm>(k: K, v: ChannelForm[K]) => {
    setForm((f) => ({ ...f, [k]: v }))
    if (k === 'baseURL') setBaseUrlTouched(true)
    setCreateError(null)
  }

  const pickVendor = (vendor: VendorChoice) => {
    if (locked) return
    setForm((f) => ({
      ...f,
      type: vendor.type,
      baseURL: baseUrlAfterVendorPick(vendor.type, f.baseURL, baseUrlTouched),
      // The region / api-version value of one vendor means nothing to another.
      other: vendor.type === f.type ? f.other : '',
    }))
    setCreateError(null)
  }

  const submitCreate = () => {
    if (create.isPending || locked) return
    const issue = validateChannelForm(form)
    if (issue !== null) {
      setCreateError(validationMessage(issue, t))
      return
    }
    create.mutate(buildChannelBody(form))
  }

  const applyModels = () => {
    if (created === null || apply.isPending || selected.size === 0) return
    apply.mutate({
      id: created.id,
      models: mergeModels(created.models, selected),
    })
  }

  const toggleModel = (m: string) =>
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(m)) next.delete(m)
      else next.add(m)
      return next
    })

  const finish = () => {
    setForm(CHANNEL_FORM_INIT)
    setBaseUrlTouched(false)
    setCreated(null)
    setCreateError(null)
    setDiscovery(null)
    setDiscoveryError(null)
    setSelected(new Set())
    setTestResult(null)
    create.reset()
    discover.reset()
    apply.reset()
    test.reset()
    setStep(1)
  }

  const stepLabels = [t('Vendor'), t('Credentials'), t('Models'), t('Review')]
  const back = (
    <Button
      type='button'
      variant='outline'
      data-testid='newchannel-back'
      onClick={() => setStep((s) => Math.max(1, s - 1))}
    >
      {t('Back')}
    </Button>
  )

  return (
    <div className='flex flex-col gap-6' data-testid='newchannel-wizard'>
      <Stepper steps={stepLabels} current={step} />

      {step === 1 && (
        <StepCard
          step={1}
          total={TOTAL_STEPS}
          title={t('Pick a vendor')}
          description={t(
            'We handle the protocol differences for you. Pick the closest match.'
          )}
          footer={
            <Button
              type='button'
              data-testid='newchannel-next'
              onClick={() => setStep(2)}
            >
              {t('Next step')}
            </Button>
          }
        >
          <div
            className='grid grid-cols-2 gap-3 md:grid-cols-4'
            role='radiogroup'
            aria-label={t('Vendor')}
          >
            {VENDOR_CHOICES.map((v) => {
              const selectedVendor = Number(form.type) === v.type
              const hint = vendorHintText(v.hint, t)
              return (
                <button
                  key={v.key}
                  type='button'
                  role='radio'
                  aria-checked={selectedVendor}
                  disabled={locked}
                  data-testid={`newchannel-vendor-${v.key}`}
                  onClick={() => pickVendor(v)}
                  className={cn(
                    'hover:bg-muted/50 flex flex-col items-start gap-1 rounded-lg border p-3 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-60',
                    selectedVendor && 'border-primary ring-primary ring-1'
                  )}
                >
                  <span className='font-medium'>{v.label ?? t('Custom')}</span>
                  <span className='text-muted-foreground text-xs'>{hint}</span>
                </button>
              )
            })}
          </div>
          {locked && (
            <p className='text-muted-foreground text-xs'>
              {t(
                'The channel is already created. Use Finish on the last step to start a new one.'
              )}
            </p>
          )}
        </StepCard>
      )}

      {step === 2 && (
        <StepCard
          step={2}
          total={TOTAL_STEPS}
          title={t('Credentials and endpoint')}
          description={t('Sent once on create and never echoed back.')}
          footer={
            <>
              {back}
              {locked ? (
                <Button
                  type='button'
                  data-testid='newchannel-continue-btn'
                  onClick={() => setStep(3)}
                >
                  {t('Continue')}
                </Button>
              ) : (
                <Button
                  type='button'
                  data-testid='newchannel-create-btn'
                  disabled={create.isPending || !canCreateChannel(form)}
                  onClick={submitCreate}
                >
                  {create.isPending
                    ? t('Creating channel...')
                    : t('Create channel')}
                </Button>
              )}
            </>
          }
        >
          <div className='grid gap-4 rounded-lg border p-4'>
            <Field
              label={t('Channel name')}
              htmlFor={`${uid}-name`}
              hint={t('An identifier, up to 100 characters.')}
            >
              <Input
                id={`${uid}-name`}
                data-testid='newchannel-name'
                value={form.name}
                disabled={locked || create.isPending}
                maxLength={100}
                placeholder='openai/main-2'
                onChange={(e) => setField('name', e.target.value)}
              />
            </Field>
            <Field
              label={t('Base URL')}
              htmlFor={`${uid}-baseurl`}
              hint={
                needsBaseUrl(Number(form.type))
                  ? t('Required: this vendor has no default host.')
                  : t('Optional. Override it to go through a proxy.')
              }
            >
              <Input
                id={`${uid}-baseurl`}
                data-testid='newchannel-baseurl'
                value={form.baseURL}
                disabled={locked || create.isPending}
                placeholder='https://api.openai.com/v1'
                aria-required={needsBaseUrl(Number(form.type))}
                onChange={(e) => setField('baseURL', e.target.value)}
              />
            </Field>
            <Field
              label={t('API key')}
              htmlFor={`${uid}-key`}
              hint={
                locked
                  ? t('The key was sent to the server and is not kept here.')
                  : t(
                      'One key per channel. Multi-key channels are created from the Channels page.'
                    )
              }
            >
              <Textarea
                id={`${uid}-key`}
                data-testid='newchannel-key'
                className='min-h-14 font-mono text-xs'
                value={form.key}
                disabled={locked || create.isPending}
                autoComplete='off'
                spellCheck={false}
                placeholder={locked ? '' : 'sk-...'}
                onChange={(e) => setField('key', e.target.value)}
              />
            </Field>
            <Field
              label={t('Models')}
              htmlFor={`${uid}-models`}
              hint={t('Comma-separated. At least one is required.')}
            >
              <Input
                id={`${uid}-models`}
                data-testid='newchannel-models'
                value={form.models}
                disabled={locked || create.isPending}
                placeholder='model-a,model-b'
                onChange={(e) => setField('models', e.target.value)}
              />
            </Field>
            {otherKind(Number(form.type)) !== null && (
              <Field
                label={
                  otherKind(Number(form.type)) === 'region'
                    ? t('Deployment region')
                    : t('API version')
                }
                htmlFor={`${uid}-other`}
                hint={
                  otherKind(Number(form.type)) === 'region'
                    ? t(
                        'Required. A JSON object with a "default" entry, for example {"default": "us-central1"}.'
                      )
                    : t('Optional. Leave empty to use the vendor default.')
                }
              >
                <Input
                  id={`${uid}-other`}
                  data-testid='newchannel-other'
                  value={form.other}
                  disabled={locked || create.isPending}
                  onChange={(e) => setField('other', e.target.value)}
                />
              </Field>
            )}
            <Field
              label={t('Organization ID')}
              htmlFor={`${uid}-orgid`}
              hint={t('Optional.')}
            >
              <Input
                id={`${uid}-orgid`}
                data-testid='newchannel-orgid'
                value={form.orgId}
                disabled={locked || create.isPending}
                placeholder='org-acme-prod'
                onChange={(e) => setField('orgId', e.target.value)}
              />
            </Field>
            {locked && (
              <p className='text-muted-foreground text-xs'>
                {t('Locked. Edit this channel from the Channels page.')}
              </p>
            )}
          </div>
          {createError !== null && (
            <StepError data-testid='newchannel-create-error'>
              {createError}
            </StepError>
          )}
          {created !== null && (
            <p
              className='text-sm text-emerald-600 dark:text-emerald-400'
              data-testid='newchannel-created-note'
            >
              {t('Channel #{{id}} created and enabled.', { id: created.id })}
            </p>
          )}
        </StepCard>
      )}

      {step === 3 && (
        <StepCard
          step={3}
          total={TOTAL_STEPS}
          title={t('Map models')}
          description={t(
            'Discover the models the upstream actually serves and add the ones you want.'
          )}
          footer={
            <>
              {back}
              <Button
                type='button'
                variant='outline'
                data-testid='newchannel-skip-models-btn'
                onClick={() => setStep(4)}
              >
                {t('Skip and continue')}
              </Button>
              <Button
                type='button'
                data-testid='newchannel-apply-models-btn'
                disabled={apply.isPending || selected.size === 0}
                onClick={applyModels}
              >
                {apply.isPending
                  ? t('Applying...')
                  : t('Add selected and continue')}
              </Button>
            </>
          }
        >
          {created === null ? (
            <p className='text-muted-foreground text-sm'>
              {t('Create the channel in step 2 first.')}
            </p>
          ) : (
            <div className='flex flex-col gap-3'>
              <div className='flex flex-wrap items-center gap-3'>
                <Button
                  type='button'
                  variant='outline'
                  data-testid='newchannel-discover-btn'
                  disabled={discover.isPending}
                  onClick={() => discover.mutate(created.id)}
                >
                  {discover.isPending
                    ? t('Discovering...')
                    : t('Discover upstream models')}
                </Button>
                {discovery !== null && discovery.new.length > 0 && (
                  <>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      onClick={() => setSelected(new Set(discovery.new))}
                    >
                      {t('Select all new')}
                    </Button>
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      onClick={() => setSelected(new Set())}
                    >
                      {t('Clear selection')}
                    </Button>
                  </>
                )}
              </div>
              {discoveryError !== null && (
                <StepError data-testid='newchannel-discover-error'>
                  {discoveryError}
                </StepError>
              )}
              {discovery !== null && (
                <div className='flex flex-col gap-2'>
                  <div className='max-h-80 overflow-auto rounded-lg border'>
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead className='w-10'>
                            <span className='sr-only'>{t('Select model')}</span>
                          </TableHead>
                          <TableHead>{t('Model')}</TableHead>
                          <TableHead>{t('Status')}</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {discovery.upstream.length === 0 && (
                          <TableRow>
                            <TableCell
                              colSpan={3}
                              className='text-muted-foreground'
                            >
                              {t('The upstream returned no models.')}
                            </TableCell>
                          </TableRow>
                        )}
                        {discovery.upstream.map((m) => {
                          const isNew = discovery.new.includes(m)
                          return (
                            <TableRow key={m}>
                              <TableCell>
                                {isNew && (
                                  <Checkbox
                                    data-testid={`newchannel-model-${m}`}
                                    aria-label={t('Add {{model}}', {
                                      model: m,
                                    })}
                                    checked={selected.has(m)}
                                    onCheckedChange={() => toggleModel(m)}
                                  />
                                )}
                              </TableCell>
                              <TableCell className='font-mono text-xs'>
                                {m}
                              </TableCell>
                              <TableCell>
                                {isNew ? (
                                  <Badge variant='secondary'>
                                    {t('Not added yet')}
                                  </Badge>
                                ) : (
                                  <Badge variant='outline'>
                                    {t('Configured')}
                                  </Badge>
                                )}
                              </TableCell>
                            </TableRow>
                          )
                        })}
                      </TableBody>
                    </Table>
                  </div>
                  {discovery.missing.length > 0 && (
                    <p
                      className='text-muted-foreground text-xs'
                      data-testid='newchannel-missing-note'
                    >
                      {t(
                        '{{count}} configured model(s) are no longer served by the upstream: {{models}}',
                        {
                          count: discovery.missing.length,
                          models: discovery.missing.join(', '),
                        }
                      )}
                    </p>
                  )}
                </div>
              )}
            </div>
          )}
        </StepCard>
      )}

      {step === 4 && (
        <StepCard
          step={4}
          total={TOTAL_STEPS}
          title={t('Review and enable')}
          description={t('Pre-flight summary.')}
          footer={
            <>
              {back}
              <Button
                type='button'
                data-testid='newchannel-finish-btn'
                onClick={finish}
              >
                {t('Finish and create another')}
              </Button>
            </>
          }
        >
          <ReviewList
            data-testid='newchannel-review'
            rows={[
              {
                label: t('Channel name'),
                value: created?.name ?? form.name,
              },
              {
                label: t('Vendor'),
                value:
                  VENDOR_CHOICES.find((v) => v.type === Number(form.type))
                    ?.label ?? t('Custom'),
              },
              {
                label: t('Base URL'),
                value:
                  form.baseURL.trim() ||
                  (needsBaseUrl(Number(form.type))
                    ? t('Not set')
                    : t('Provider default')),
              },
              {
                label: t('Models configured'),
                value: String(
                  splitModels(created?.models ?? form.models).length
                ),
              },
            ]}
          />
          <div className='flex flex-wrap items-center gap-3'>
            <Button
              type='button'
              variant='outline'
              data-testid='newchannel-test-btn'
              disabled={test.isPending || created === null}
              onClick={() => created !== null && test.mutate(created.id)}
            >
              {test.isPending ? t('Testing...') : t('Test channel')}
            </Button>
            {testResult !== null && (
              <span
                role='status'
                data-testid='newchannel-test-result'
                className={cn(
                  'font-mono text-xs',
                  testResult.success
                    ? 'text-emerald-600 dark:text-emerald-400'
                    : 'text-destructive'
                )}
              >
                {testResultText(testResult, t)}
              </span>
            )}
          </div>
          <p className='text-xs'>
            <Link to='/ops/channels' className='underline'>
              {t('Open the Channels page')}
            </Link>
          </p>
        </StepCard>
      )}
    </div>
  )
}
