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
import type { MoneyConfig } from '@/lib/money'

import {
  EMPTY_FORM,
  buildWriteBody,
  formFromDepartment,
  unitLabel,
  type DepartmentFormValues,
  type FormError,
} from '../lib/form'
import type { Department, DepartmentWriteBody } from '../types'

interface Props {
  /** Present = edit, absent = create. */
  department?: Department
  money: MoneyConfig | null
  saving: boolean
  onSubmit: (body: DepartmentWriteBody) => void
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

export function DepartmentFormDialog(props: Props) {
  const { t } = useTranslation()
  const { department, money } = props
  const [form, setForm] = useState<DepartmentFormValues>(() =>
    department ? formFromDepartment(department, money) : EMPTY_FORM
  )
  const [error, setError] = useState<FormError | null>(null)

  const set = <K extends keyof DepartmentFormValues>(
    k: K,
    v: DepartmentFormValues[K]
  ) => {
    setForm((f) => ({ ...f, [k]: v }))
    setError(null)
  }

  const errorText: Record<FormError, string> = {
    name: t('Enter a department name.'),
    'name-long': t('The department name is too long (at most 128 characters).'),
    'description-long': t('The description is too long (at most 512 characters).'),
    'code-format': t(
      'The external code must be at most 64 bytes and contain no control characters.'
    ),
    'budget-invalid': t('Enter a budget such as 500 or 1200.50, or leave it empty.'),
    'budget-unavailable': t(
      'The currency settings have not loaded, so a budget cannot be set. Reload the page.'
    ),
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (props.saving) return
    const built = buildWriteBody(form, money, department?.monthlyBudgetQuota)
    if (!built.ok) {
      setError(built.error)
      return
    }
    props.onSubmit(built.body)
  }

  const unit = unitLabel(money)

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !props.saving) props.onClose()
      }}
    >
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>
            {department ? t('Edit department') : t('New department')}
          </DialogTitle>
          <DialogDescription>
            {t(
              'A department groups keys and spend for reporting and budgets.'
            )}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className='grid gap-4' noValidate>
          <Field label={t('Name')}>
            <Input
              value={form.name}
              onChange={(e) => set('name', e.target.value)}
              aria-label={t('Name')}
              autoFocus
            />
          </Field>
          <Field label={t('Description')}>
            <Input
              value={form.description}
              onChange={(e) => set('description', e.target.value)}
              aria-label={t('Description')}
            />
          </Field>
          <Field
            label={t('External code')}
            hint={t(
              'Matched against the X-Lurus-Dept request header to attribute calls to this department. At most 64 bytes, no control characters (letters, digits and . _ @ : - are safest). Leave empty for none.'
            )}
          >
            <Input
              value={form.externalCode}
              onChange={(e) => set('externalCode', e.target.value)}
              aria-label={t('External code')}
              maxLength={64}
              className='font-mono'
            />
          </Field>
          <Field
            label={t('Monthly budget')}
            hint={t('Leave empty or 0 for no budget.')}
          >
            <div className='flex items-center gap-2'>
              {unit !== '' && (
                <span className='text-muted-foreground text-sm'>{unit}</span>
              )}
              <Input
                inputMode='decimal'
                value={form.budget}
                onChange={(e) => set('budget', e.target.value)}
                aria-label={t('Monthly budget')}
              />
            </div>
          </Field>

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
              {department ? t('Save') : t('Create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
