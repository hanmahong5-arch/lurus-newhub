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
import { Textarea } from '@/components/ui/textarea'

import {
  checkHeaderOverride,
  checkParamOverride,
  prettyJson,
  type JsonCheck,
} from '../lib/json'
import type { ChannelTemplate, TemplateWriteBody } from '../types'
import { Field } from './field'

interface TemplateFormDialogProps {
  /** Present = edit (bumps the version), absent = create. */
  template?: ChannelTemplate
  saving: boolean
  onSubmit: (body: TemplateWriteBody) => void
  onClose: () => void
}

export function TemplateFormDialog(props: TemplateFormDialogProps) {
  const { t } = useTranslation()
  const { template } = props
  const [name, setName] = useState(template?.name ?? '')
  const [description, setDescription] = useState(template?.description ?? '')
  const [params, setParams] = useState(
    prettyJson(template?.param_override ?? '')
  )
  const [headers, setHeaders] = useState(
    prettyJson(template?.header_override ?? '')
  )
  const [submitted, setSubmitted] = useState(false)

  const paramCheck = checkParamOverride(params)
  const headerCheck = checkHeaderOverride(headers)

  const message = (c: JsonCheck): string | null => {
    if (c.ok) return null
    switch (c.reason) {
      case 'syntax':
        return t('Not valid JSON.')
      case 'not-object':
        return t('Must be a JSON object, such as {"key": "value"}.')
      case 'header-key':
        return t('A header name is not valid.')
      case 'header-value':
        return t('Every header value must be a string.')
    }
  }

  const paramMsg = message(paramCheck)
  const headerMsg = message(headerCheck)
  const nameMissing = name.trim() === ''
  const bothEmpty = params.trim() === '' && headers.trim() === ''

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (props.saving) return
    setSubmitted(true)
    if (nameMissing || bothEmpty || paramMsg || headerMsg) return
    props.onSubmit({
      name: name.trim(),
      description: description.trim(),
      param_override: params.trim(),
      header_override: headers.trim(),
    })
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !props.saving) props.onClose()
      }}
    >
      <DialogContent className='sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>
            {template ? t('Edit template') : t('Add template')}
          </DialogTitle>
          <DialogDescription>
            {template
              ? t(
                  'Saving raises the version. Channels keep their current overrides until the template is applied again.'
                )
              : t(
                  'A template holds parameter and header overrides that can be written into many channels at once.'
                )}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className='grid gap-4' noValidate>
          <Field label={t('Name')}>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              aria-label={t('Name')}
              maxLength={64}
              autoFocus
            />
          </Field>
          <Field label={t('Description')}>
            <Input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              aria-label={t('Description')}
              maxLength={255}
            />
          </Field>
          <Field
            label={t('Parameter overrides (JSON)')}
            hint={t(
              'Leave empty to leave that part of each channel unchanged.'
            )}
          >
            <Textarea
              value={params}
              onChange={(e) => setParams(e.target.value)}
              aria-label={t('Parameter overrides (JSON)')}
              aria-invalid={paramMsg != null}
              className='min-h-28 font-mono text-xs'
              spellCheck={false}
            />
            {paramMsg != null && (
              <p role='alert' className='text-destructive text-xs'>
                {paramMsg}
              </p>
            )}
          </Field>
          <Field
            label={t('Header overrides (JSON)')}
            hint={t(
              'Leave empty to leave that part of each channel unchanged.'
            )}
          >
            <Textarea
              value={headers}
              onChange={(e) => setHeaders(e.target.value)}
              aria-label={t('Header overrides (JSON)')}
              aria-invalid={headerMsg != null}
              className='min-h-24 font-mono text-xs'
              spellCheck={false}
            />
            {headerMsg != null && (
              <p role='alert' className='text-destructive text-xs'>
                {headerMsg}
              </p>
            )}
          </Field>
          <div>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => {
                setParams(prettyJson(params))
                setHeaders(prettyJson(headers))
              }}
            >
              {t('Format JSON')}
            </Button>
          </div>

          {submitted && nameMissing && (
            <p role='alert' className='text-destructive text-sm'>
              {t('Enter a name for the template.')}
            </p>
          )}
          {submitted && bothEmpty && (
            <p role='alert' className='text-destructive text-sm'>
              {t('Fill in parameter overrides, header overrides, or both.')}
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
              {template ? t('Save') : t('Create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
