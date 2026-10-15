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
import { useId, useState, type FormEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
  sideDrawerSwitchItemClassName,
} from '@/components/drawer-layout'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'

import {
  EMPTY_JOB_FORM,
  buildJobBody,
  categoryLabel,
  categoryOptions,
  formFromJob,
  scheduleLabel,
  type JobFormError,
  type JobFormValues,
} from '../lib'
import {
  SCHEDULES,
  TOP_N_MAX,
  type JobBody,
  type OpenRouterChannel,
  type SyncCategory,
  type SyncJob,
} from '../types'

interface JobFormSheetProps {
  /** Present = edit, absent = create. */
  job?: SyncJob
  channels: OpenRouterChannel[]
  categories: SyncCategory[]
  saving: boolean
  /** The server's refusal, shown inline so the form stays open with it. */
  serverError: string | null
  onSubmit: (body: JobBody) => void
  onClose: () => void
}

function Field(props: {
  label: string
  htmlFor?: string
  hint?: string
  children: ReactNode
}) {
  return (
    <div className='grid gap-1.5'>
      <Label htmlFor={props.htmlFor}>{props.label}</Label>
      {props.children}
      {props.hint != null && (
        <p className='text-muted-foreground text-xs'>{props.hint}</p>
      )}
    </div>
  )
}

/** Create / edit drawer of one sync job. */
export function JobFormSheet(props: JobFormSheetProps) {
  const { t } = useTranslation()
  const { job } = props
  const formId = useId()
  const [form, setForm] = useState<JobFormValues>(() =>
    job ? formFromJob(job) : EMPTY_JOB_FORM
  )
  const [error, setError] = useState<JobFormError | null>(null)

  const set = <K extends keyof JobFormValues>(k: K, v: JobFormValues[K]) => {
    setForm((f) => ({ ...f, [k]: v }))
    setError(null)
  }

  const cats = categoryOptions(props.categories, form.categories)
  // A job can point at a channel the picker no longer lists; keep it
  // selectable instead of silently re-targeting the job on save.
  const hasCurrentChannel =
    form.channelId === '' ||
    props.channels.some((c) => String(c.id) === form.channelId)

  const toggleCategory = (key: string, on: boolean) =>
    set(
      'categories',
      on ? [...form.categories, key] : form.categories.filter((k) => k !== key)
    )

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (props.saving) return
    const built = buildJobBody(form)
    if (!built.ok) {
      setError(built.error)
      return
    }
    props.onSubmit(built.body)
  }

  const errorText: Record<JobFormError, string> = {
    required: t('Name, target channel and categories are required.'),
    'top-n': t('Top N must be a whole number from 0 to {{max}}.', {
      max: TOP_N_MAX,
    }),
  }
  const shown = error != null ? errorText[error] : props.serverError

  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open && !props.saving) props.onClose()
      }}
    >
      <SheetContent
        className={sideDrawerContentClassName('max-w-none sm:!max-w-[520px]')}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {job ? t('Edit job #{{id}}', { id: job.id }) : t('New sync job')}
          </SheetTitle>
          <SheetDescription>
            {t(
              'Imports free OpenRouter models of the chosen categories into the target channel.'
            )}
          </SheetDescription>
        </SheetHeader>

        <form
          id={formId}
          onSubmit={submit}
          noValidate
          aria-busy={props.saving}
          className={sideDrawerFormClassName('gap-5')}
        >
          <SideDrawerSection>
            <Field label={t('Job name')} htmlFor={`${formId}-name`}>
              <Input
                id={`${formId}-name`}
                value={form.name}
                onChange={(e) => set('name', e.target.value)}
                placeholder={t('e.g. Daily free reasoning Top 10')}
                autoFocus
              />
            </Field>

            <Field
              label={t('Target channel (OpenRouter)')}
              htmlFor={`${formId}-channel`}
              hint={
                props.channels.length === 0
                  ? t('No OpenRouter channel exists yet. Create one first.')
                  : undefined
              }
            >
              <NativeSelect
                id={`${formId}-channel`}
                className='w-full'
                value={form.channelId}
                onChange={(e) => set('channelId', e.target.value)}
              >
                <NativeSelectOption value=''>
                  {t('Select an OpenRouter channel')}
                </NativeSelectOption>
                {!hasCurrentChannel && (
                  <NativeSelectOption value={form.channelId}>
                    {`#${form.channelId}`}
                  </NativeSelectOption>
                )}
                {props.channels.map((c) => (
                  <NativeSelectOption key={c.id} value={String(c.id)}>
                    {`${c.name} (#${c.id})`}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>

            <div
              className='grid gap-1.5'
              role='group'
              aria-labelledby={`${formId}-cats`}
            >
              <Label id={`${formId}-cats`}>{t('Categories (multiple)')}</Label>
              <div className='grid gap-2 sm:grid-cols-2'>
                {cats.map((c) => {
                  return (
                    <label
                      key={c.key}
                      className='flex cursor-pointer items-center gap-2 rounded-md border px-2.5 py-2 text-sm'
                    >
                      <Checkbox
                        checked={form.categories.includes(c.key)}
                        onCheckedChange={(v) =>
                          toggleCategory(c.key, v === true)
                        }
                      />
                      <span>{categoryLabel(c.key, c.label, t)}</span>
                    </label>
                  )
                })}
              </div>
            </div>

            <Field
              label={t('Top N (0 = unlimited)')}
              htmlFor={`${formId}-topn`}
              hint={t('Keep only the N most used models. 0 keeps every match.')}
            >
              <Input
                id={`${formId}-topn`}
                type='number'
                inputMode='numeric'
                min={0}
                max={TOP_N_MAX}
                value={form.topN}
                onChange={(e) => set('topN', e.target.value)}
              />
            </Field>

            <Field label={t('Schedule')} htmlFor={`${formId}-schedule`}>
              <NativeSelect
                id={`${formId}-schedule`}
                className='w-full'
                value={form.schedule}
                onChange={(e) => set('schedule', e.target.value)}
              >
                {SCHEDULES.map((s) => (
                  <NativeSelectOption key={s} value={s}>
                    {scheduleLabel(s, t)}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>

            <div className={sideDrawerSwitchItemClassName()}>
              <div className='flex flex-col gap-0.5'>
                <Label className='text-sm'>{t('Enabled')}</Label>
                <p className='text-muted-foreground text-xs'>
                  {t('A disabled job never runs on its schedule.')}
                </p>
              </div>
              <Switch
                checked={form.enabled}
                onCheckedChange={(v) => set('enabled', v)}
                aria-label={t('Enabled')}
              />
            </div>
          </SideDrawerSection>

          {shown != null && shown !== '' && (
            <p role='alert' className='text-destructive text-sm'>
              {shown}
            </p>
          )}
        </form>

        <SheetFooter className={sideDrawerFooterClassName()}>
          <Button
            type='button'
            variant='outline'
            onClick={props.onClose}
            disabled={props.saving}
          >
            {t('Cancel')}
          </Button>
          <Button type='submit' form={formId} disabled={props.saving}>
            {t('Save')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
