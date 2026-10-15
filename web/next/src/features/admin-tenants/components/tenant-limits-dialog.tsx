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
import { useId, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

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
import { handleServerError } from '@/lib/handle-server-error'

import { updateTenant } from '../api'
import { limitFieldValue, parseLimit } from '../lib/tenant-form'
import type { Tenant } from '../types'
import { Field } from './ui-bits'
import { useTenants } from './tenants-provider'

/**
 * Tenant-level RPM / TPM caps (aggregate across all of its keys). The wire
 * keys mirror entity/tenant.go; 0 = unlimited and is shown as an empty field.
 */
export function TenantLimitsDialog({ tenant }: { tenant: Tenant }) {
  const { t } = useTranslation()
  const { setDialog, refresh } = useTenants()
  const formId = useId()
  const [rpm, setRpm] = useState(limitFieldValue(tenant.rate_limit_rpm))
  const [tpm, setTpm] = useState(limitFieldValue(tenant.rate_limit_tpm))

  const save = useMutation({
    mutationFn: () =>
      updateTenant(tenant.id, {
        rate_limit_rpm: parseLimit(rpm),
        rate_limit_tpm: parseLimit(tpm),
      }),
    onSuccess: () => {
      toast.success(t('Rate limits saved'))
      refresh()
      setDialog(null)
    },
    onError: (error) =>
      handleServerError(error, t('Failed to save rate limits')),
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!save.isPending) save.mutate()
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) setDialog(null)
      }}
    >
      <DialogContent className='sm:max-w-md'>
        <DialogHeader>
          <DialogTitle>
            {t('{{name}} · rate limits', { name: tenant.name })}
          </DialogTitle>
          <DialogDescription>
            {t(
              'Aggregate caps across all keys under this tenant. 0 = unlimited.'
            )}
          </DialogDescription>
        </DialogHeader>
        <form
          id={formId}
          onSubmit={submit}
          noValidate
          className='grid gap-4 sm:grid-cols-2'
        >
          <Field label={t('RPM limit')} htmlFor={`${formId}-rpm`}>
            <Input
              id={`${formId}-rpm`}
              type='number'
              min={0}
              step={1}
              value={rpm}
              onChange={(e) => setRpm(e.target.value)}
              placeholder={t('0 = unlimited')}
              disabled={save.isPending}
            />
          </Field>
          <Field label={t('TPM limit')} htmlFor={`${formId}-tpm`}>
            <Input
              id={`${formId}-tpm`}
              type='number'
              min={0}
              step={1}
              value={tpm}
              onChange={(e) => setTpm(e.target.value)}
              placeholder={t('0 = unlimited')}
              disabled={save.isPending}
            />
          </Field>
        </form>
        <DialogFooter>
          <Button
            type='button'
            variant='outline'
            disabled={save.isPending}
            onClick={() => setDialog(null)}
          >
            {t('Cancel')}
          </Button>
          <Button form={formId} type='submit' disabled={save.isPending}>
            {save.isPending ? t('Saving...') : t('Save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
