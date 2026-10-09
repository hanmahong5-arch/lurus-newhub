import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select'
import { toApiError } from '@/lib/api'

import { channelsQueryKey, updateChannelSetting } from '../api'
import {
  buildSettingJson,
  PLAN_KINDS,
  planToForm,
  TEST_MODES,
  type PlanForm,
  type PlanFormError,
} from '../lib/map'
import type { ChannelDetail } from '../types'
import { Field } from './shared'

export function PlanTab(props: {
  channelId: number
  detail: ChannelDetail
  canWrite: boolean
}) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [form, setForm] = useState<PlanForm>(() => planToForm(props.detail.plan))
  const [error, setError] = useState<PlanFormError | null>(null)

  const set = <K extends keyof PlanForm>(k: K, v: PlanForm[K]) => {
    setForm((f) => ({ ...f, [k]: v }))
    setError(null)
  }

  const save = useMutation({
    mutationFn: (setting: string) =>
      updateChannelSetting(props.channelId, setting),
    onSuccess: async () => {
      toast.success(t('Saved'))
      await qc.invalidateQueries({ queryKey: channelsQueryKey })
    },
    onError: (e: unknown) => {
      toast.error(toApiError(e).message)
    },
  })

  const kindLabel: Record<string, string> = {
    zhipu_coding: t('Zhipu coding plan'),
    kimi_coding: t('Moonshot coding plan'),
    minimax: t('MiniMax plan'),
  }
  const modeLabel: Record<string, string> = {
    all: t('Test everything'),
    auto_ban_only: t('Only test auto-disabled channels'),
    none: t('Never test'),
  }
  const errorText: Record<PlanFormError, string> = {
    kind: t('Pick a supported plan type.'),
    threshold: t('Enter a threshold between 1 and 100, or leave it empty.'),
    expiry: t('Enter a valid expiry date.'),
    fee: t('Enter a monthly fee of zero or more.'),
    'test-mode': t('Pick a supported test mode.'),
    'setting-corrupt': t(
      'The stored channel settings are not valid JSON, so they were not changed. Fix them in the legacy console first.'
    ),
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (save.isPending || !props.canWrite) return
    const built = buildSettingJson(props.detail.settingRaw, form)
    if (!built.ok) {
      setError(built.error)
      return
    }
    save.mutate(built.setting)
  }

  const ro = !props.canWrite

  return (
    <form
      onSubmit={submit}
      className='grid gap-4'
      noValidate
      data-testid='plan-form'
    >
      <Field label={t('Plan type')}>
        <NativeSelect
          className='w-full'
          value={form.planKind}
          disabled={ro}
          onChange={(e) => set('planKind', e.target.value)}
          aria-label={t('Plan type')}
        >
          <NativeSelectOption value=''>{t('Pay as you go')}</NativeSelectOption>
          {PLAN_KINDS.map((k) => (
            <NativeSelectOption key={k} value={k}>
              {kindLabel[k]}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      </Field>
      <Field
        label={t('Park threshold (%)')}
        hint={t(
          'Used percent at which a plan window parks the channel until it resets. Empty uses the platform default (95).'
        )}
      >
        <Input
          inputMode='decimal'
          value={form.thresholdPct}
          disabled={ro}
          onChange={(e) => set('thresholdPct', e.target.value)}
          placeholder='95'
          aria-label={t('Park threshold (%)')}
        />
      </Field>
      <Field
        label={t('Plan expires on')}
        hint={t('Leave empty for a plan that never expires.')}
      >
        <Input
          type='date'
          value={form.expiresOn}
          disabled={ro}
          onChange={(e) => set('expiresOn', e.target.value)}
          aria-label={t('Plan expires on')}
        />
      </Field>
      <Field
        label={t('Monthly plan fee (CNY)')}
        hint={t(
          'What the subscription costs per month. Used to compute utilization; empty means pay-per-use.'
        )}
      >
        <Input
          inputMode='decimal'
          value={form.monthlyFeeCny}
          disabled={ro}
          onChange={(e) => set('monthlyFeeCny', e.target.value)}
          placeholder='200'
          aria-label={t('Monthly plan fee (CNY)')}
        />
      </Field>
      <Field
        label={t('Scheduled test mode')}
        hint={t(
          'Plan channels default to testing only auto-disabled ones, because a scheduled test burns plan quota.'
        )}
      >
        <NativeSelect
          className='w-full'
          value={form.testMode}
          disabled={ro}
          onChange={(e) => set('testMode', e.target.value)}
          aria-label={t('Scheduled test mode')}
        >
          <NativeSelectOption value=''>{t('Default')}</NativeSelectOption>
          {TEST_MODES.map((m) => (
            <NativeSelectOption key={m} value={m}>
              {modeLabel[m]}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      </Field>

      {error != null && (
        <p role='alert' className='text-destructive text-sm'>
          {errorText[error]}
        </p>
      )}
      {props.canWrite && (
        <div>
          <Button type='submit' disabled={save.isPending}>
            {t('Save plan settings')}
          </Button>
        </div>
      )}
    </form>
  )
}
