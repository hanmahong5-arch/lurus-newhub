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
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { handleServerError } from '@/lib/handle-server-error'

import { runTenantAction } from '../api'
import { statusActionCopy } from '../lib/labels'
import type { Tenant, TenantAction } from '../types'
import { useTenants } from './tenants-provider'

interface TenantStatusDialogProps {
  tenant: Tenant
  action: TenantAction
}

/**
 * Enable / disable / suspend change what every key and channel under the
 * tenant can do, so the person must type the tenant's name to go through.
 */
export function TenantStatusDialog({
  tenant,
  action,
}: TenantStatusDialogProps) {
  const { t } = useTranslation()
  const { setDialog, refresh } = useTenants()
  const inputId = useId()
  const [typed, setTyped] = useState('')
  const copy = statusActionCopy(action, tenant.name, t)

  const run = useMutation({
    mutationFn: () => runTenantAction(tenant.id, action),
    onSuccess: () => {
      toast.success(copy.done)
      refresh()
      setDialog(null)
    },
    onError: (error) =>
      handleServerError(error, t('Failed to change the tenant status')),
  })

  const confirmed = typed.trim() === tenant.name

  return (
    <ConfirmDialog
      open
      destructive={action !== 'enable'}
      onOpenChange={(open) => {
        if (!open && !run.isPending) setDialog(null)
      }}
      title={copy.title}
      desc={
        <>
          {t('This affects every key and channel under this tenant.')}
          {action === 'enable' &&
            ` ${t('Traffic from the tenant resumes right away.')}`}
        </>
      }
      confirmText={run.isPending ? t('Working...') : copy.confirm}
      isLoading={run.isPending}
      disabled={!confirmed}
      handleConfirm={() => {
        if (confirmed && !run.isPending) run.mutate()
      }}
    >
      <div className='grid gap-1.5'>
        <Label htmlFor={inputId}>
          {t('Type the tenant name "{{name}}" to confirm', {
            name: tenant.name,
          })}
        </Label>
        <Input
          id={inputId}
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          autoComplete='off'
          autoFocus
          disabled={run.isPending}
        />
      </div>
    </ConfirmDialog>
  )
}
