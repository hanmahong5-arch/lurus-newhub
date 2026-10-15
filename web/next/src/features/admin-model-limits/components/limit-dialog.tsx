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
import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

import { parseCap } from '../lib/caps'
import type { ModelLimit, ModelLimitBody } from '../types'

interface LimitDialogProps {
  /** Present = edit (the model is the row key, so it is locked). */
  limit?: ModelLimit
  tenantId: string
  saving: boolean
  onSubmit: (body: ModelLimitBody) => void
  onClose: () => void
}

function capText(value: number | undefined): string {
  return value != null && value > 0 ? String(value) : ''
}

export function LimitDialog(props: LimitDialogProps) {
  const { t } = useTranslation()
  const { limit } = props
  const isNew = limit === undefined
  const [model, setModel] = useState(limit?.model ?? '')
  const [rpm, setRpm] = useState(capText(limit?.rate_limit_rpm))
  const [tpm, setTpm] = useState(capText(limit?.rate_limit_tpm))
  const trimmed = model.trim()

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!trimmed) return
    props.onSubmit({
      model: trimmed,
      rate_limit_rpm: parseCap(rpm),
      rate_limit_tpm: parseCap(tpm),
    })
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !props.saving) props.onClose()
      }}
      title={isNew ? t('New model limit') : t('Edit model limit')}
      description={t('Tenant: {{tenant}}', { tenant: props.tenantId })}
      contentClassName='sm:max-w-md'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            disabled={props.saving}
            onClick={props.onClose}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form='model-limit-form'
            disabled={props.saving || !trimmed}
          >
            {props.saving ? t('Saving...') : t('Save')}
          </Button>
        </>
      }
    >
      <form
        id='model-limit-form'
        className='grid gap-4'
        onSubmit={submit}
        noValidate
      >
        <div className='grid gap-1.5'>
          <Label htmlFor='model-limit-model'>{t('Model name')}</Label>
          <Input
            id='model-limit-model'
            value={model}
            disabled={!isNew}
            placeholder='model-a'
            autoComplete='off'
            onChange={(e) => setModel(e.target.value)}
          />
          <p className='text-muted-foreground text-xs'>
            {isNew
              ? t('Exact model name as requested (e.g. model-a). No wildcards.')
              : t(
                  'The model is the row key and cannot be renamed. Delete the limit and create a new one instead.'
                )}
          </p>
        </div>
        <div className='grid grid-cols-2 gap-3'>
          <div className='grid gap-1.5'>
            <Label htmlFor='model-limit-rpm'>{t('RPM limit')}</Label>
            <Input
              id='model-limit-rpm'
              type='number'
              inputMode='numeric'
              min={0}
              placeholder='0'
              value={rpm}
              onChange={(e) => setRpm(e.target.value)}
            />
          </div>
          <div className='grid gap-1.5'>
            <Label htmlFor='model-limit-tpm'>{t('TPM limit')}</Label>
            <Input
              id='model-limit-tpm'
              type='number'
              inputMode='numeric'
              min={0}
              placeholder='0'
              value={tpm}
              onChange={(e) => setTpm(e.target.value)}
            />
          </div>
        </div>
        <p className='text-muted-foreground text-xs'>
          {t('Per-minute caps for this model under the tenant. 0 = unlimited.')}
        </p>
      </form>
    </Dialog>
  )
}
