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
import { useState, type FormEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

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
import { Label } from '@/components/ui/label'
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import type { MoneyConfig } from '@/lib/money'

import {
  EMPTY_FORM,
  buildWriteBody,
  formFromToken,
  type FormError,
  type KeyFormValues,
} from '../lib/form'
import { capUnitLabel } from '../lib/quota'
import type { ApiToken, ProjectOption, TokenWriteBody } from '../types'

interface KeyFormDialogProps {
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

function Field(props: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className='grid gap-1.5'>
      <Label>{props.label}</Label>
      {props.children}
      {props.hint != null && (
        <p className='text-muted-foreground text-xs'>{props.hint}</p>
      )}
    </div>
  )
}

export function KeyFormDialog(props: KeyFormDialogProps) {
  const { t } = useTranslation()
  const { token, money } = props
  const [form, setForm] = useState<KeyFormValues>(() =>
    token ? formFromToken(token, money) : EMPTY_FORM
  )
  const [error, setError] = useState<FormError | null>(null)

  const set = <K extends keyof KeyFormValues>(k: K, v: KeyFormValues[K]) => {
    setForm((f) => ({ ...f, [k]: v }))
    setError(null)
  }

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
    const built = buildWriteBody(form, money, token?.used_quota ?? 0)
    if (!built.ok) {
      setError(built.error)
      return
    }
    props.onSubmit(built.body)
  }

  const unit = capUnitLabel(money)

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !props.saving) props.onClose()
      }}
    >
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{token ? t('Edit key') : t('Create key')}</DialogTitle>
          <DialogDescription>
            {token
              ? t('Changes apply to new requests right away.')
              : t('The full key is shown once, right after it is created.')}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className='grid gap-4' noValidate>
          <Field label={t('Name')}>
            <Input
              value={form.name}
              onChange={(e) => set('name', e.target.value)}
              placeholder={t('e.g. prod-backend')}
              aria-label={t('Name')}
              autoFocus
            />
          </Field>

          <div className='flex items-center justify-between gap-3'>
            <Label>{t('Unlimited quota')}</Label>
            <Switch
              checked={form.unlimited}
              onCheckedChange={(v) => set('unlimited', v)}
              aria-label={t('Unlimited quota')}
            />
          </div>
          {!form.unlimited && (
            <Field
              label={t('Total quota')}
              hint={
                token
                  ? t('Total cap including what this key has already used.')
                  : undefined
              }
            >
              <div className='flex items-center gap-2'>
                {unit !== '' && (
                  <span className='text-muted-foreground text-sm'>{unit}</span>
                )}
                <Input
                  inputMode='decimal'
                  value={form.cap}
                  onChange={(e) => set('cap', e.target.value)}
                  placeholder='200'
                  aria-label={t('Total quota')}
                />
              </div>
            </Field>
          )}

          <Field
            label={t('Expires on')}
            hint={t('Leave empty for a key that never expires.')}
          >
            <Input
              type='date'
              value={form.expiry}
              onChange={(e) => set('expiry', e.target.value)}
              aria-label={t('Expires on')}
            />
          </Field>

          <div className='flex items-center justify-between gap-3'>
            <Label>{t('Restrict models')}</Label>
            <Switch
              checked={form.limitModels}
              onCheckedChange={(v) => set('limitModels', v)}
              aria-label={t('Restrict models')}
            />
          </div>
          {form.limitModels && (
            <Field label={t('Allowed models (comma-separated)')}>
              <Input
                value={form.models}
                onChange={(e) => set('models', e.target.value)}
                aria-label={t('Allowed models (comma-separated)')}
              />
            </Field>
          )}

          <Field
            label={t('Allowed IPs')}
            hint={t('One per line or comma-separated. Empty = any address.')}
          >
            <Input
              value={form.allowIps}
              onChange={(e) => set('allowIps', e.target.value)}
              aria-label={t('Allowed IPs')}
            />
          </Field>

          {!props.projectsFailed && (
            <Field
              label={t('Project')}
              hint={t(
                'Attributes spend to a project for reporting. Grants no access.'
              )}
            >
              <NativeSelect
                className='w-full'
                value={form.projectId}
                onChange={(e) => set('projectId', e.target.value)}
                aria-label={t('Project')}
              >
                <NativeSelectOption value=''>{t('Unassigned')}</NativeSelectOption>
                {props.projects.map((p) => (
                  <NativeSelectOption key={p.id} value={String(p.id)}>
                    {p.name}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
          )}

          {error != null && (
            <p role='alert' className='text-destructive text-sm'>
              {errorText[error]}
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
              {token ? t('Save') : t('Create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
