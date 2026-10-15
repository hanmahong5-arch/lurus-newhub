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
import { useMutation } from '@tanstack/react-query'
import { Gauge, Settings2, Wallet } from 'lucide-react'
import { useId, useMemo, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  SideDrawerSection,
  SideDrawerSectionHeader,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { useQuota } from '@/hooks/use-quota'
import { handleServerError } from '@/lib/handle-server-error'

import { createTenant, updateTenant } from '../api'
import { NAME_MAX_LENGTH, SLUG_MAX_LENGTH } from '../constants'
import { planLabel } from '../lib/labels'
import {
  EMPTY_CREATE_FORM,
  editFormFromTenant,
  parseCreateForm,
  parseEditForm,
  planChoices,
  slugify,
  type CreateField,
  type CreateFormValues,
  type EditFormValues,
  type FieldError,
} from '../lib/tenant-form'
import type { Tenant } from '../types'
import { Field, SwitchRow } from './ui-bits'
import { useTenants } from './tenants-provider'

interface TenantFormSheetProps {
  /** Present = edit, absent = create. */
  tenant?: Tenant
}

export function TenantFormSheet({ tenant }: TenantFormSheetProps) {
  return tenant ? <EditForm tenant={tenant} /> : <CreateForm />
}

function useFieldErrorText() {
  const { t } = useTranslation()
  return (error: FieldError | undefined): string | undefined => {
    switch (error) {
      case undefined:
        return undefined
      case 'required':
        return t('This field is required')
      case 'too-long':
        return t('This value is too long.')
      case 'invalid-number':
        return t('Enter a whole number')
      case 'invalid-amount':
        return t('Enter a valid amount')
      case 'currency':
        return t('The currency settings have not loaded yet.')
      case 'cannot-clear':
        return t('This value can be changed here but not cleared.')
    }
  }
}

function useUnitLabel(): string {
  const { t } = useTranslation()
  const { isTokens, symbol } = useQuota()
  return isTokens ? t('Tokens') : symbol
}

// ─── Create ───────────────────────────────────────────────────────────────

function CreateForm() {
  const { t } = useTranslation()
  const { setDialog, refresh } = useTenants()
  const { fromDisplayAmount } = useQuota()
  const errorText = useFieldErrorText()
  const unit = useUnitLabel()
  const formId = useId()
  const [values, setValues] = useState<CreateFormValues>(EMPTY_CREATE_FORM)
  const [slugTouched, setSlugTouched] = useState(false)
  const [errors, setErrors] = useState<
    Partial<Record<CreateField, FieldError>>
  >({})

  const set = <K extends CreateField>(key: K, value: CreateFormValues[K]) => {
    setValues((v) => ({ ...v, [key]: value }))
    setErrors((e) => ({ ...e, [key]: undefined }))
  }

  const create = useMutation({
    mutationFn: createTenant,
    onSuccess: () => {
      toast.success(t('Tenant created'))
      refresh()
      setDialog(null)
    },
    onError: (error) => handleServerError(error, t('Failed to create tenant')),
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (create.isPending) return
    const parsed = parseCreateForm(values, fromDisplayAmount)
    if (!parsed.ok) {
      setErrors(parsed.errors)
      return
    }
    create.mutate(parsed.body)
  }

  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open && !create.isPending) setDialog(null)
      }}
    >
      <SheetContent
        className={sideDrawerContentClassName('max-w-none sm:!max-w-[560px]')}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('New tenant')}</SheetTitle>
          <SheetDescription>
            {t(
              'A tenant is an isolated organization with its own keys, channels and budget.'
            )}
          </SheetDescription>
        </SheetHeader>

        <form
          id={formId}
          onSubmit={submit}
          noValidate
          aria-busy={create.isPending}
          className={sideDrawerFormClassName('gap-5')}
        >
          <fieldset disabled={create.isPending} className='contents'>
            <SideDrawerSection>
              <SideDrawerSectionHeader
                title={t('Identity')}
                description={t('How the tenant is named and found.')}
                icon={<Settings2 className='size-4' />}
                iconTone='info'
              />
              <Field
                label={t('Name')}
                htmlFor={`${formId}-name`}
                error={errorText(errors.name)}
              >
                <Input
                  id={`${formId}-name`}
                  value={values.name}
                  maxLength={NAME_MAX_LENGTH}
                  onChange={(e) => {
                    set('name', e.target.value)
                    if (!slugTouched) {
                      setValues((v) => ({
                        ...v,
                        slug: slugify(e.target.value.trim()),
                      }))
                    }
                  }}
                  placeholder={t('e.g. Acme Corp')}
                  autoFocus
                />
              </Field>
              <Field
                label={t('Slug')}
                htmlFor={`${formId}-slug`}
                hint={t(
                  'Lower-case letters, digits and dashes. Appears in the tenant URL.'
                )}
                error={errorText(errors.slug)}
              >
                <Input
                  id={`${formId}-slug`}
                  value={values.slug}
                  maxLength={SLUG_MAX_LENGTH}
                  onChange={(e) => {
                    setSlugTouched(true)
                    set('slug', slugify(e.target.value))
                  }}
                  placeholder={t('e.g. acme')}
                  className='font-mono'
                />
              </Field>
              <Field
                label={t('Identity provider organization ID')}
                htmlFor={`${formId}-org`}
                hint={t(
                  'The organization at the sign-in provider that this tenant maps to.'
                )}
                error={errorText(errors.orgId)}
              >
                <Input
                  id={`${formId}-org`}
                  value={values.orgId}
                  onChange={(e) => set('orgId', e.target.value)}
                  className='font-mono'
                  autoComplete='off'
                />
              </Field>
            </SideDrawerSection>

            <SideDrawerSection>
              <SideDrawerSectionHeader
                title={t('Plan and limits')}
                description={t(
                  'Leave a limit blank to use the platform default.'
                )}
                icon={<Wallet className='size-4' />}
                iconTone='success'
              />
              <Field label={t('Plan')} htmlFor={`${formId}-plan`}>
                <NativeSelect
                  id={`${formId}-plan`}
                  className='w-full'
                  value={values.plan}
                  onChange={(e) => set('plan', e.target.value)}
                >
                  {planChoices().map((plan) => (
                    <NativeSelectOption key={plan} value={plan}>
                      {planLabel(plan, t)}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </Field>
              <Field
                label={t('Seats')}
                htmlFor={`${formId}-seats`}
                hint={t('Most people who can belong to the tenant.')}
                error={errorText(errors.maxUsers)}
              >
                <Input
                  id={`${formId}-seats`}
                  inputMode='numeric'
                  value={values.maxUsers}
                  onChange={(e) => set('maxUsers', e.target.value)}
                  placeholder='100'
                />
              </Field>
              <Field
                label={
                  unit
                    ? t('Monthly quota cap ({{currency}})', { currency: unit })
                    : t('Monthly quota cap')
                }
                htmlFor={`${formId}-cap`}
                hint={t(
                  'What the tenant may spend per month. Blank uses the platform default.'
                )}
                error={errorText(errors.quotaCap)}
              >
                <Input
                  id={`${formId}-cap`}
                  inputMode='decimal'
                  autoComplete='off'
                  value={values.quotaCap}
                  onChange={(e) => set('quotaCap', e.target.value)}
                />
              </Field>
            </SideDrawerSection>

            <SideDrawerSection>
              <SideDrawerSectionHeader
                title={t('Rate limits')}
                description={t(
                  'Aggregate caps across all keys of the tenant. Blank or 0 = unlimited.'
                )}
                icon={<Gauge className='size-4' />}
                iconTone='warning'
              />
              <div className='grid gap-4 sm:grid-cols-2'>
                <Field
                  label={t('RPM limit')}
                  htmlFor={`${formId}-rpm`}
                  error={errorText(errors.rpm)}
                >
                  <Input
                    id={`${formId}-rpm`}
                    inputMode='numeric'
                    value={values.rpm}
                    onChange={(e) => set('rpm', e.target.value)}
                    placeholder={t('0 = unlimited')}
                  />
                </Field>
                <Field
                  label={t('TPM limit')}
                  htmlFor={`${formId}-tpm`}
                  error={errorText(errors.tpm)}
                >
                  <Input
                    id={`${formId}-tpm`}
                    inputMode='numeric'
                    value={values.tpm}
                    onChange={(e) => set('tpm', e.target.value)}
                    placeholder={t('0 = unlimited')}
                  />
                </Field>
              </div>
            </SideDrawerSection>
          </fieldset>
        </form>

        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose
            render={<Button variant='outline' disabled={create.isPending} />}
          >
            {t('Cancel')}
          </SheetClose>
          <Button form={formId} type='submit' disabled={create.isPending}>
            {create.isPending ? t('Creating...') : t('Create tenant')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

// ─── Edit ─────────────────────────────────────────────────────────────────

function EditForm({ tenant }: { tenant: Tenant }) {
  const { t } = useTranslation()
  const { setDialog, refresh } = useTenants()
  const { fromDisplayAmount, toDisplayAmount } = useQuota()
  const errorText = useFieldErrorText()
  const unit = useUnitLabel()
  const formId = useId()

  // What the tenant stands as today; the person's edits are kept on top of it
  // so the cap shows up as soon as the currency settings arrive.
  const initial = useMemo(
    () => editFormFromTenant(tenant, toDisplayAmount),
    [tenant, toDisplayAmount]
  )
  const [edits, setEdits] = useState<Partial<EditFormValues>>({})
  const [errors, setErrors] = useState<
    Partial<Record<keyof EditFormValues, FieldError>>
  >({})
  const values: EditFormValues = { ...initial, ...edits }

  const set = <K extends keyof EditFormValues>(
    key: K,
    value: EditFormValues[K]
  ) => {
    setEdits((e) => ({ ...e, [key]: value }))
    setErrors((e) => ({ ...e, [key]: undefined }))
  }

  const parsed = parseEditForm(values, initial, fromDisplayAmount)
  const unchanged = parsed.ok && Object.keys(parsed.patch).length === 0

  const save = useMutation({
    mutationFn: (patch: Parameters<typeof updateTenant>[1]) =>
      updateTenant(tenant.id, patch),
    onSuccess: () => {
      toast.success(t('Tenant updated'))
      refresh()
      setDialog(null)
    },
    onError: (error) => handleServerError(error, t('Failed to update tenant')),
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (save.isPending) return
    if (!parsed.ok) {
      setErrors(parsed.errors)
      return
    }
    if (unchanged) return
    save.mutate(parsed.patch)
  }

  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) setDialog(null)
      }}
    >
      <SheetContent
        className={sideDrawerContentClassName('max-w-none sm:!max-w-[560px]')}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('Edit tenant')}</SheetTitle>
          <SheetDescription>{tenant.name}</SheetDescription>
        </SheetHeader>

        <form
          id={formId}
          onSubmit={submit}
          noValidate
          aria-busy={save.isPending}
          className={sideDrawerFormClassName('gap-5')}
        >
          <fieldset disabled={save.isPending} className='contents'>
            <SideDrawerSection>
              <SideDrawerSectionHeader
                title={t('Identity')}
                description={t('How the tenant is named and found.')}
                icon={<Settings2 className='size-4' />}
                iconTone='info'
              />
              <Field
                label={t('Name')}
                htmlFor={`${formId}-name`}
                error={errorText(errors.name)}
              >
                <Input
                  id={`${formId}-name`}
                  value={values.name}
                  maxLength={NAME_MAX_LENGTH}
                  onChange={(e) => set('name', e.target.value)}
                  autoFocus
                />
              </Field>
              <Field
                label={t('Slug')}
                htmlFor={`${formId}-slug`}
                hint={t('The slug is fixed once the tenant exists.')}
              >
                <Input
                  id={`${formId}-slug`}
                  value={tenant.slug}
                  readOnly
                  disabled
                  className='font-mono'
                />
              </Field>
              <Field
                label={t('Identity provider organization ID')}
                htmlFor={`${formId}-org`}
              >
                <Input
                  id={`${formId}-org`}
                  value={tenant.zitadel_org_id}
                  readOnly
                  disabled
                  className='font-mono'
                />
              </Field>
            </SideDrawerSection>

            <SideDrawerSection>
              <SideDrawerSectionHeader
                title={t('Plan and limits')}
                description={t(
                  'Seats and the quota cap can be changed but not removed.'
                )}
                icon={<Wallet className='size-4' />}
                iconTone='success'
              />
              <Field label={t('Plan')} htmlFor={`${formId}-plan`}>
                <NativeSelect
                  id={`${formId}-plan`}
                  className='w-full'
                  value={values.plan}
                  onChange={(e) => set('plan', e.target.value)}
                >
                  {planChoices(initial.plan).map((plan) => (
                    <NativeSelectOption key={plan} value={plan}>
                      {planLabel(plan, t)}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </Field>
              <Field
                label={t('Seats')}
                htmlFor={`${formId}-seats`}
                hint={t('Most people who can belong to the tenant.')}
                error={errorText(errors.maxUsers)}
              >
                <Input
                  id={`${formId}-seats`}
                  inputMode='numeric'
                  value={values.maxUsers}
                  onChange={(e) => set('maxUsers', e.target.value)}
                />
              </Field>
              <Field
                label={
                  unit
                    ? t('Monthly quota cap ({{currency}})', { currency: unit })
                    : t('Monthly quota cap')
                }
                htmlFor={`${formId}-cap`}
                hint={t('What the tenant may spend per month.')}
                error={errorText(errors.quotaCap)}
              >
                <Input
                  id={`${formId}-cap`}
                  inputMode='decimal'
                  autoComplete='off'
                  value={values.quotaCap}
                  onChange={(e) => set('quotaCap', e.target.value)}
                />
              </Field>
              <SwitchRow
                label={t('Platform wallet is the only balance gate')}
                description={t(
                  'When on, a request the platform wallet approved is no longer refused by the local balance check.'
                )}
                checked={values.walletAuthoritative}
                onChange={(v) => set('walletAuthoritative', v)}
              />
            </SideDrawerSection>
          </fieldset>
        </form>

        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose
            render={<Button variant='outline' disabled={save.isPending} />}
          >
            {t('Cancel')}
          </SheetClose>
          <Button
            form={formId}
            type='submit'
            disabled={save.isPending || unchanged}
          >
            {save.isPending ? t('Saving...') : t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
