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
import { Code2, Palette } from 'lucide-react'
import {
  useCallback,
  useId,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { JsonCodeEditor } from '@/components/json-code-editor'
import { Button } from '@/components/ui/button'
import { FieldError } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  baselineOf,
  isFieldDirty,
  serializeField,
  validateField,
  type DraftReader,
  type OptionField,
} from '../lib/option-fields'
import { OptionDraftContext, type OptionDraft } from '../hooks/use-option-draft'
import { useOptionMap, type OptionMap } from '../hooks/use-system-options'
import { useUpdateOption } from '../hooks/use-update-option'
import { FormDirtyIndicator } from './form-dirty-indicator'
import { FormNavigationGuard } from './form-navigation-guard'
import {
  SettingsForm,
  SettingsFormGrid,
  SettingsFormGridItem,
  SettingsSwitchField,
} from './settings-form-layout'
import { SettingsPageFormActions } from './settings-page-context'

/**
 * Keys whose change must be confirmed before anything is sent (a value that
 * rescales money everywhere). The dialog lists each guarded change as
 * "old -> new"; cancelling sends nothing and keeps the edits.
 */
export type SaveGuard = {
  keys: string[]
  /** English source strings; translated at render time. */
  title: string
  description: string
  confirmText: string
}

type OptionFormProps = {
  fields: OptionField[]
  /** Raw option map; defaults to the shared /api/v2/admin/options query. */
  map?: OptionMap
  /** Rendered under the fields (notes, danger zones). */
  children?: ReactNode
  /** Shown above the grid. */
  intro?: ReactNode
  saveLabel?: string
  /**
   * Render only some fields. A field that is not shown still counts as dirty
   * and is validated and saved with the rest.
   */
  show?: (field: OptionField) => boolean
  guard?: SaveGuard
}

/**
 * The generic option engine: render a list of declared fields, track which
 * ones changed, validate, and write only the changed keys - one PUT per key,
 * in declaration order. A refusal stops the run, is shown under the field and
 * in a toast, and leaves that field (and the ones after it) dirty so a retry
 * sends only what has not been saved.
 */
export function OptionForm({
  fields,
  map: mapProp,
  children,
  intro,
  saveLabel,
  show,
  guard,
}: OptionFormProps) {
  const { t } = useTranslation()
  const shared = useOptionMap()
  const map = mapProp ?? shared.map
  const update = useUpdateOption()
  const baseId = useId()

  const [edits, setEdits] = useState<Record<string, string>>({})
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)
  const [confirming, setConfirming] = useState(false)

  const baselines = useMemo(() => {
    const out: Record<string, string> = {}
    for (const field of fields) out[field.key] = baselineOf(field, map)
    return out
  }, [fields, map])

  const valueOf = useCallback(
    (key: string) => edits[key] ?? baselines[key] ?? '',
    [edits, baselines]
  )

  const reader: DraftReader = useMemo(
    () => ({
      bool: (key) => valueOf(key) === 'true',
      str: (key) => valueOf(key),
    }),
    [valueOf]
  )

  const dirtyFields = fields.filter((field) =>
    isFieldDirty(field, edits[field.key], baselines[field.key] ?? '')
  )
  const dirty = dirtyFields.length > 0

  const setValue = (key: string, value: string) => {
    setEdits((current) => ({ ...current, [key]: value }))
    setErrors((current) => {
      if (!(key in current)) return current
      const next = { ...current }
      delete next[key]
      return next
    })
  }

  const handleReset = () => {
    setEdits({})
    setErrors({})
  }

  const guardedDirty = guard
    ? dirtyFields.filter((field) => guard.keys.includes(field.key))
    : []

  const handleSave = async (confirmed = false) => {
    if (dirtyFields.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    const found: Record<string, string> = {}
    for (const field of dirtyFields) {
      const problem = validateField(field, valueOf(field.key))
      if (problem) found[field.key] = problem
    }
    if (Object.keys(found).length > 0) {
      setErrors(found)
      toast.error(t('Fix the highlighted fields before saving'))
      return
    }

    if (guard && !confirmed && guardedDirty.length > 0) {
      setConfirming(true)
      return
    }

    setSaving(true)
    setErrors({})
    let saved = 0
    try {
      for (const field of dirtyFields) {
        const draft = valueOf(field.key)
        try {
          await update.mutateAsync({
            key: field.key,
            value: serializeField(field, draft),
          })
        } catch (error) {
          const message = getServerErrorMessage(
            error,
            t('Failed to update setting')
          )
          setErrors({ [field.key]: message })
          toast.error(`${t(field.label)}: ${message}`)
          return
        }
        saved += 1
        setEdits((current) => {
          const next = { ...current }
          delete next[field.key]
          return next
        })
      }
      toast.success(
        saved === 1
          ? t('Setting updated successfully')
          : t('{{count}} settings updated successfully', { count: saved })
      )
    } finally {
      setSaving(false)
    }
  }

  const visible = fields.filter(
    (field) =>
      (!show || show(field)) && (!field.visibleWhen || field.visibleWhen(reader))
  )

  // An error on a field that is not on screen (another tab, or hidden by its
  // switch) would otherwise be invisible; show it above the form instead.
  const visibleKeys = new Set(visible.map((field) => field.key))
  const orphanErrors = Object.entries(errors).filter(
    ([key]) => !visibleKeys.has(key)
  )

  const draftApi: OptionDraft = useMemo(
    () => ({
      value: valueOf,
      saved: (key) => baselines[key] ?? '',
      setValue,
      disabled: saving,
    }),
    // setValue only touches state setters, so it is stable enough.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [valueOf, baselines, saving]
  )

  return (
    <OptionDraftContext.Provider value={draftApi}>
      <FormNavigationGuard when={dirty} />
      <SettingsForm
        onSubmit={(event) => {
          event.preventDefault()
          void handleSave()
        }}
      >
        <SettingsPageFormActions
          onSave={() => void handleSave()}
          onReset={handleReset}
          isSaving={saving}
          isResetDisabled={!dirty}
          saveLabel={saveLabel}
        />
        <FormDirtyIndicator isDirty={dirty} />
        {intro}
        {orphanErrors.length > 0 ? (
          <div
            role='alert'
            data-settings-form-span='full'
            className='border-destructive/40 bg-destructive/10 text-destructive rounded-lg border px-3 py-2 text-sm'
          >
            <ul className='flex flex-col gap-1'>
              {orphanErrors.map(([key, message]) => (
                <li key={key}>
                  {t(fields.find((f) => f.key === key)?.label ?? key)}:{' '}
                  {t(message)}
                </li>
              ))}
            </ul>
          </div>
        ) : null}
        <SettingsFormGrid>
          {visible.map((field) => {
            const id = `${baseId}-${field.key}`
            const value = valueOf(field.key)
            const error = errors[field.key]
            const disabled = saving || Boolean(field.disabledWhen?.(reader))
            const full =
              field.span === 'full' ||
              field.kind === 'json' ||
              (field.kind === 'text' && field.rows !== undefined) ||
              (field.kind === 'list' && field.separator !== 'comma')
            return (
              <SettingsFormGridItem
                key={field.key}
                span={full ? 'full' : undefined}
              >
                <FieldView
                  id={id}
                  field={field}
                  value={value}
                  error={error}
                  disabled={disabled}
                  onChange={(next) => setValue(field.key, next)}
                />
              </SettingsFormGridItem>
            )
          })}
        </SettingsFormGrid>
        {children}
      </SettingsForm>
      {guard ? (
        <ConfirmDialog
          open={confirming}
          onOpenChange={setConfirming}
          title={t(guard.title)}
          desc={
            <div className='flex flex-col gap-3'>
              <p>{t(guard.description)}</p>
              <ul className='flex flex-col gap-1 font-mono text-xs'>
                {guardedDirty.map((field) => (
                  <li key={field.key}>
                    {t(field.label)}: {baselines[field.key] || '-'}
                    {' -> '}
                    {valueOf(field.key).trim()}
                  </li>
                ))}
              </ul>
            </div>
          }
          confirmText={t(guard.confirmText)}
          destructive
          handleConfirm={() => {
            setConfirming(false)
            void handleSave(true)
          }}
        />
      ) : null}
    </OptionDraftContext.Provider>
  )
}

type FieldViewProps = {
  id: string
  field: OptionField
  value: string
  error?: string
  disabled: boolean
  onChange: (value: string) => void
}

function FieldView({
  id,
  field,
  value,
  error,
  disabled,
  onChange,
}: FieldViewProps) {
  const { t } = useTranslation()
  const describedBy = [
    field.description ? `${id}-description` : null,
    error ? `${id}-error` : null,
  ]
    .filter(Boolean)
    .join(' ')

  if (field.kind === 'switch') {
    return (
      <div>
        <SettingsSwitchField
          controlId={id}
          checked={value === 'true'}
          onCheckedChange={(checked) => onChange(checked ? 'true' : 'false')}
          label={t(field.label)}
          description={field.description ? t(field.description) : undefined}
          disabled={disabled}
        />
        {error ? (
          <FieldError id={`${id}-error`}>{t(error)}</FieldError>
        ) : null}
      </div>
    )
  }

  const hint =
    field.kind === 'number' && field.hint
      ? field.hint(
          value.trim() !== '' && Number.isFinite(Number(value))
            ? Number(value)
            : null
        )
      : null

  return (
    <div className='flex min-w-0 flex-col gap-1.5'>
      <Label htmlFor={id}>{t(field.label)}</Label>
      {field.kind === 'text' && field.rows !== undefined ? (
        <Textarea
          id={id}
          rows={field.rows}
          value={value}
          disabled={disabled}
          placeholder={field.placeholder ? t(field.placeholder) : undefined}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy || undefined}
          onChange={(event) => onChange(event.target.value)}
        />
      ) : null}
      {field.kind === 'list' && field.separator !== 'comma' ? (
        <Textarea
          id={id}
          rows={field.rows ?? 4}
          value={value}
          disabled={disabled}
          placeholder={field.placeholder}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy || undefined}
          onChange={(event) => onChange(event.target.value)}
        />
      ) : null}
      {field.kind === 'list' && field.separator === 'comma' ? (
        <Input
          id={id}
          value={value}
          disabled={disabled}
          placeholder={field.placeholder}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy || undefined}
          onChange={(event) => onChange(event.target.value)}
        />
      ) : null}
      {field.kind === 'text' && field.rows === undefined ? (
        <Input
          id={id}
          value={value}
          disabled={disabled}
          maxLength={field.maxLength}
          placeholder={field.placeholder ? t(field.placeholder) : undefined}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy || undefined}
          onChange={(event) => onChange(event.target.value)}
        />
      ) : null}
      {field.kind === 'number' ? (
        <Input
          id={id}
          type='number'
          inputMode='decimal'
          value={value}
          disabled={disabled}
          min={field.min}
          max={field.max}
          step={field.step ?? (field.integer ? 1 : 'any')}
          placeholder={field.placeholder ? t(field.placeholder) : undefined}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy || undefined}
          onChange={(event) => onChange(event.target.value)}
        />
      ) : null}
      {field.kind === 'secret' ? (
        <Input
          id={id}
          type='password'
          autoComplete='new-password'
          value={value}
          disabled={disabled}
          placeholder={t(
            field.placeholder ?? 'Leave blank to keep the current value'
          )}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy || undefined}
          onChange={(event) => onChange(event.target.value)}
        />
      ) : null}
      {field.kind === 'select' ? (
        <Select
          items={field.options.map((option) => ({
            value: option.value,
            label: t(option.label),
          }))}
          value={value}
          onValueChange={(next) => onChange(next ?? '')}
          disabled={disabled}
        >
          <SelectTrigger id={id} className='w-full'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectGroup>
              {field.options.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {t(option.label)}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      ) : null}
      {field.kind === 'json' ? (
        <JsonFieldControl
          id={id}
          field={field}
          value={value}
          disabled={disabled}
          invalid={Boolean(error)}
          describedBy={describedBy}
          onChange={onChange}
        />
      ) : null}
      {hint ? <p className='text-muted-foreground text-xs'>{hint}</p> : null}
      {field.description ? (
        <p
          id={`${id}-description`}
          className='text-muted-foreground text-xs leading-normal'
        >
          {t(field.description)}
        </p>
      ) : null}
      {error ? <FieldError id={`${id}-error`}>{t(error)}</FieldError> : null}
    </div>
  )
}

type JsonFieldControlProps = {
  id: string
  field: Extract<OptionField, { kind: 'json' }>
  value: string
  disabled: boolean
  invalid: boolean
  describedBy: string
  onChange: (value: string) => void
}

function JsonFieldControl({
  id,
  field,
  value,
  disabled,
  invalid,
  describedBy,
  onChange,
}: JsonFieldControlProps) {
  const { t } = useTranslation()
  const [raw, setRaw] = useState(false)
  const Editor = field.editor
  // The visual editor can only show a document that parses; a half-typed
  // document stays in the raw editor until it is valid again.
  const parses = validateField(field, value) === null
  const visual = Editor !== undefined && !raw && parses

  return (
    <div className='flex flex-col gap-2'>
      {Editor ? (
        <div className='flex justify-end'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={disabled || (raw && !parses)}
            onClick={() => setRaw((current) => !current)}
          >
            {visual ? (
              <>
                <Code2 data-icon='inline-start' />
                {t('JSON Mode')}
              </>
            ) : (
              <>
                <Palette data-icon='inline-start' />
                {t('Visual Mode')}
              </>
            )}
          </Button>
        </div>
      ) : null}
      {visual && Editor ? (
        <Editor value={value} onChange={onChange} disabled={disabled} />
      ) : (
        <JsonCodeEditor
          id={id}
          value={value}
          onChange={onChange}
          disabled={disabled}
          ariaLabel={t(field.label)}
          heightClassName={field.heightClassName}
          placeholder={field.placeholder}
          aria-invalid={invalid ? true : undefined}
          aria-describedby={describedBy || undefined}
        />
      )}
      {Editor && !parses ? (
        <p className='text-muted-foreground text-xs'>
          {t('Fix the JSON to return to the visual editor.')}
        </p>
      ) : null}
    </div>
  )
}
