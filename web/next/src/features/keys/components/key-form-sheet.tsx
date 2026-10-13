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
import { useQuery } from '@tanstack/react-query'
import { ChevronDown, KeyRound, ShieldCheck, WalletCards } from 'lucide-react'
import { useId, useMemo, useState, type FormEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { DateTimePicker } from '@/components/datetime-picker'
import {
  SideDrawerSection,
  SideDrawerSectionHeader,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
  sideDrawerSwitchItemClassName,
} from '@/components/drawer-layout'
import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import type { MoneyConfig } from '@/lib/money'
import { cn } from '@/lib/utils'

import { routableModelsQueryOptions } from '../api'
import {
  EMPTY_FORM,
  buildWriteBody,
  dateToExpiry,
  expiryAfter,
  expiryToDate,
  formFromToken,
  type FormError,
  type KeyFormValues,
} from '../lib/form'
import { capUnitLabel } from '../lib/quota'
import { joinModels, splitModels } from '../lib/restrictions'
import type { ApiToken, ProjectOption, TokenWriteBody } from '../types'

interface KeyFormSheetProps {
  /** Present = edit, absent = create. */
  token?: ApiToken
  money: MoneyConfig | null
  projects: ProjectOption[]
  /** Project list failed to load; the picker is hidden rather than empty. */
  projectsFailed?: boolean
  saving: boolean
  onSubmit: (body: TokenWriteBody) => void
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

function SwitchRow(props: {
  label: string
  description?: string
  checked: boolean
  onChange: (v: boolean) => void
}) {
  return (
    <div className={sideDrawerSwitchItemClassName()}>
      <div className='flex flex-col gap-0.5'>
        <Label className='text-sm'>{props.label}</Label>
        {props.description != null && (
          <p className='text-muted-foreground text-xs'>{props.description}</p>
        )}
      </div>
      <Switch
        checked={props.checked}
        onCheckedChange={props.onChange}
        aria-label={props.label}
      />
    </div>
  )
}

/** Create / edit drawer: basics, quota, then access restrictions. */
export function KeyFormSheet(props: KeyFormSheetProps) {
  const { t } = useTranslation()
  const { token, money } = props
  const formId = useId()
  const [form, setForm] = useState<KeyFormValues>(() =>
    token ? formFromToken(token, money) : EMPTY_FORM
  )
  const [error, setError] = useState<FormError | null>(null)
  const [accessOpen, setAccessOpen] = useState(
    () => form.limitModels || form.allowIps.trim() !== ''
  )
  const models = useQuery(routableModelsQueryOptions)

  const set = <K extends keyof KeyFormValues>(k: K, v: KeyFormValues[K]) => {
    setForm((f) => ({ ...f, [k]: v }))
    setError(null)
  }

  const modelOptions = useMemo(
    () =>
      (models.data ?? []).map((m) => ({ value: m.id, label: m.id })),
    [models.data]
  )

  const errorText: Record<FormError, string> = {
    name: t('Enter a name for the key.'),
    'cap-unavailable': t(
      'The currency settings have not loaded, so a quota cannot be set. Reload the page.'
    ),
    'cap-invalid': t('Enter a quota greater than zero, such as 5 or 12.50.'),
    expiry: t('Enter a valid expiry date.'),
    models: t('List at least one model, or turn off the model restriction.'),
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (props.saving) return
    const built = buildWriteBody(
      form,
      money,
      token?.used_quota ?? 0,
      token?.expired_time ?? -1
    )
    if (!built.ok) {
      setError(built.error)
      if (built.error === 'models') setAccessOpen(true)
      return
    }
    props.onSubmit(built.body)
  }

  const unit = capUnitLabel(money)
  const presets: Array<[string, () => string]> = [
    [t('Never'), () => ''],
    [t('1 hour'), () => expiryAfter(new Date(), { hours: 1 })],
    [t('1 day'), () => expiryAfter(new Date(), { days: 1 })],
    [t('1 month'), () => expiryAfter(new Date(), { months: 1 })],
  ]

  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open && !props.saving) props.onClose()
      }}
    >
      <SheetContent
        className={sideDrawerContentClassName('max-w-none sm:!max-w-[560px]')}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{token ? t('Edit key') : t('Create key')}</SheetTitle>
          <SheetDescription>
            {token
              ? t('Changes apply to new requests right away.')
              : t('The full key is shown once, right after it is created.')}
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
            <SideDrawerSectionHeader
              title={t('Basic information')}
              description={t('Name the key and decide when it expires.')}
              icon={<KeyRound className='size-4' />}
              iconTone='info'
            />
            <Field label={t('Name')} htmlFor={`${formId}-name`}>
              <Input
                id={`${formId}-name`}
                value={form.name}
                onChange={(e) => set('name', e.target.value)}
                placeholder={t('e.g. prod-backend')}
                autoFocus
              />
            </Field>

            {!props.projectsFailed && (
              <Field
                label={t('Project')}
                htmlFor={`${formId}-project`}
                hint={t(
                  'Attributes spend to a project for reporting. Grants no access.'
                )}
              >
                <NativeSelect
                  id={`${formId}-project`}
                  className='w-full'
                  value={form.projectId}
                  onChange={(e) => set('projectId', e.target.value)}
                >
                  <NativeSelectOption value=''>
                    {t('Unassigned')}
                  </NativeSelectOption>
                  {props.projects.map((p) => (
                    <NativeSelectOption key={p.id} value={String(p.id)}>
                      {p.name}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </Field>
            )}

            <Field
              label={t('Expires on')}
              hint={t('Leave empty for a key that never expires.')}
            >
              <div className='grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center'>
                <DateTimePicker
                  value={expiryToDate(form.expiry)}
                  onChange={(d) => set('expiry', dateToExpiry(d))}
                  placeholder={t('Never expires')}
                  className='min-w-0'
                />
                <div className='grid grid-cols-4 gap-2 sm:flex'>
                  {presets.map(([label, value]) => (
                    <Button
                      key={label}
                      type='button'
                      variant='outline'
                      size='sm'
                      className='px-2 text-xs sm:px-3 sm:text-sm'
                      onClick={() => set('expiry', value())}
                    >
                      {label}
                    </Button>
                  ))}
                </div>
              </div>
            </Field>
          </SideDrawerSection>

          <SideDrawerSection>
            <SideDrawerSectionHeader
              title={t('Quota')}
              description={t('Cap how much this key can spend.')}
              icon={<WalletCards className='size-4' />}
              iconTone='success'
            />
            <SwitchRow
              label={t('Unlimited quota')}
              description={t('No spending cap on this key.')}
              checked={form.unlimited}
              onChange={(v) => set('unlimited', v)}
            />
            {!form.unlimited && (
              <Field
                label={t('Total quota')}
                htmlFor={`${formId}-cap`}
                hint={
                  token
                    ? t('Total cap including what this key has already used.')
                    : undefined
                }
              >
                <div className='flex items-center gap-2'>
                  {unit !== '' && (
                    <span className='text-muted-foreground text-sm'>
                      {unit}
                    </span>
                  )}
                  <Input
                    id={`${formId}-cap`}
                    inputMode='decimal'
                    value={form.cap}
                    onChange={(e) => set('cap', e.target.value)}
                    placeholder='200'
                  />
                </div>
              </Field>
            )}
          </SideDrawerSection>

          <Collapsible open={accessOpen} onOpenChange={setAccessOpen}>
            <SideDrawerSection>
              <CollapsibleTrigger
                render={
                  <button
                    type='button'
                    className='hover:bg-muted/40 flex w-full items-center gap-3 rounded-md py-1.5 text-start transition-colors'
                  />
                }
              >
                <SideDrawerSectionHeader
                  className='flex-1'
                  title={t('Access restrictions')}
                  description={t('Limit which models and addresses can use the key.')}
                  icon={<ShieldCheck className='size-4' />}
                  iconTone='warning'
                />
                <ChevronDown
                  className={cn(
                    'text-muted-foreground size-4 transition-transform',
                    accessOpen && 'rotate-180'
                  )}
                />
              </CollapsibleTrigger>
              <CollapsibleContent className='grid gap-4'>
                <SwitchRow
                  label={t('Restrict models')}
                  description={t('Only the models you pick can be called.')}
                  checked={form.limitModels}
                  onChange={(v) => set('limitModels', v)}
                />
                {form.limitModels && (
                  <Field
                    label={t('Allowed models')}
                    htmlFor={`${formId}-models`}
                    hint={
                      models.isError
                        ? t(
                            'Could not load the model list. Type model names to add them.'
                          )
                        : t('Pick from the list or type a model name.')
                    }
                  >
                    <MultiSelect
                      id={`${formId}-models`}
                      options={modelOptions}
                      selected={splitModels(form.models)}
                      onChange={(v) => set('models', joinModels(v))}
                      allowCreate
                      placeholder={t('Select models...')}
                    />
                  </Field>
                )}
                <Field
                  label={t('Allowed IPs')}
                  htmlFor={`${formId}-ips`}
                  hint={t(
                    'One per line or comma-separated. Empty = any address.'
                  )}
                >
                  <Textarea
                    id={`${formId}-ips`}
                    rows={3}
                    value={form.allowIps}
                    onChange={(e) => set('allowIps', e.target.value)}
                    className='font-mono text-xs'
                  />
                </Field>
              </CollapsibleContent>
            </SideDrawerSection>
          </Collapsible>

          {error != null && (
            <p role='alert' className='text-destructive text-sm'>
              {errorText[error]}
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
            {token ? t('Save') : t('Create')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
