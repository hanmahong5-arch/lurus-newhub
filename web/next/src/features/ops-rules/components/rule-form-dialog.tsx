import { useState, type FormEvent } from 'react'
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
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'

import {
  EMPTY_RULE_FORM,
  buildRuleBody,
  formFromRule,
  type RuleFormError,
  type RuleFormValues,
} from '../lib/rule-form'
import {
  RULE_KINDS,
  RULE_MODES,
  RULE_PATTERN_TYPES,
  RULE_ROLE_SCOPES,
  type ContentRule,
  type RuleWriteBody,
} from '../types'
import { Field } from './field'
import { useRuleLabels } from './labels'

interface RuleFormDialogProps {
  /** Present = edit, absent = create. */
  rule?: ContentRule
  builtins: string[]
  maxPatternLen: number
  saving: boolean
  onSubmit: (body: RuleWriteBody) => void
  onClose: () => void
}

export function RuleFormDialog(props: RuleFormDialogProps) {
  const { t } = useTranslation()
  const labels = useRuleLabels()
  const { rule } = props
  const [form, setForm] = useState<RuleFormValues>(() =>
    rule ? formFromRule(rule) : EMPTY_RULE_FORM
  )
  const [error, setError] = useState<RuleFormError | null>(null)

  const set = <K extends keyof RuleFormValues>(k: K, v: RuleFormValues[K]) => {
    setForm((f) => ({ ...f, [k]: v }))
    setError(null)
  }

  const errorText: Record<RuleFormError, string> = {
    name: t('Enter a name for the rule.'),
    ordinal: t('Order must be a whole number, 0 or greater.'),
    builtin: t('Choose a built-in detector.'),
    pattern: t('Enter a regular expression.'),
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (props.saving) return
    const built = buildRuleBody(form)
    if (!built.ok) {
      setError(built.error)
      return
    }
    props.onSubmit(built.body)
  }

  const isBuiltin = form.pattern_type === 'builtin'
  // An edited rule may name a detector the server no longer lists.
  const builtinChoices =
    form.builtin !== '' && !props.builtins.includes(form.builtin)
      ? [form.builtin, ...props.builtins]
      : props.builtins

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !props.saving) props.onClose()
      }}
    >
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{rule ? t('Edit rule') : t('Add rule')}</DialogTitle>
          <DialogDescription>
            {t(
              'Platform rules run on every tenant. New rules start in observe mode: they only record matches.'
            )}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className='grid gap-4' noValidate>
          <Field label={t('Name')}>
            <Input
              value={form.name}
              onChange={(e) => set('name', e.target.value)}
              aria-label={t('Name')}
              maxLength={64}
              autoFocus
            />
          </Field>

          <div className='grid grid-cols-2 gap-3'>
            <Field label={t('Applies to')}>
              <NativeSelect
                className='w-full'
                value={form.role_scope}
                onChange={(e) => set('role_scope', e.target.value)}
                aria-label={t('Applies to')}
              >
                {RULE_ROLE_SCOPES.map((v) => (
                  <NativeSelectOption key={v} value={v}>
                    {labels.role(v)}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
            <Field label={t('Action')}>
              <NativeSelect
                className='w-full'
                value={form.kind}
                onChange={(e) => set('kind', e.target.value)}
                aria-label={t('Action')}
              >
                {RULE_KINDS.map((v) => (
                  <NativeSelectOption key={v} value={v}>
                    {labels.kind(v)}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
          </div>

          <div className='grid grid-cols-2 gap-3'>
            <Field label={t('Pattern type')}>
              <NativeSelect
                className='w-full'
                value={form.pattern_type}
                onChange={(e) => set('pattern_type', e.target.value)}
                aria-label={t('Pattern type')}
              >
                {RULE_PATTERN_TYPES.map((v) => (
                  <NativeSelectOption key={v} value={v}>
                    {labels.patternType(v)}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
            <Field label={t('Mode')}>
              <NativeSelect
                className='w-full'
                value={form.mode}
                onChange={(e) => set('mode', e.target.value)}
                aria-label={t('Mode')}
              >
                {RULE_MODES.map((v) => (
                  <NativeSelectOption key={v} value={v}>
                    {labels.mode(v)}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
          </div>

          {isBuiltin ? (
            <Field label={t('Built-in detector')}>
              <NativeSelect
                className='w-full'
                value={form.builtin}
                onChange={(e) => set('builtin', e.target.value)}
                aria-label={t('Built-in detector')}
              >
                <NativeSelectOption value=''>
                  {t('Choose...')}
                </NativeSelectOption>
                {builtinChoices.map((b) => (
                  <NativeSelectOption key={b} value={b}>
                    {b}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
          ) : (
            <Field
              label={t('Regular expression')}
              hint={
                props.maxPatternLen > 0
                  ? t(
                      'At most {{count}} characters; it must not match empty text.',
                      {
                        count: props.maxPatternLen,
                      }
                    )
                  : undefined
              }
            >
              <Input
                value={form.pattern}
                onChange={(e) => set('pattern', e.target.value)}
                aria-label={t('Regular expression')}
                className='font-mono'
              />
            </Field>
          )}

          {form.kind === 'mask' && (
            <Field
              label={t('Replacement')}
              hint={t('Text that replaces each match. Empty uses the default.')}
            >
              <Input
                value={form.replacement}
                onChange={(e) => set('replacement', e.target.value)}
                aria-label={t('Replacement')}
                maxLength={64}
              />
            </Field>
          )}

          <Field
            label={t('Order')}
            hint={t('Rules run from the lowest number to the highest.')}
          >
            <Input
              inputMode='numeric'
              value={form.ordinal}
              onChange={(e) => set('ordinal', e.target.value)}
              aria-label={t('Order')}
            />
          </Field>

          <div className='flex items-center justify-between gap-3'>
            <Label>{t('Enabled')}</Label>
            <Switch
              checked={form.enabled}
              onCheckedChange={(v) => set('enabled', v)}
              aria-label={t('Enabled')}
            />
          </div>

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
              {rule ? t('Save') : t('Create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
