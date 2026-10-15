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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, ShieldAlert } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/section-page-layout'
import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toApiError } from '@/lib/api'

import {
  DEFAULT_TENANT,
  allowlistQueryOptions,
  deleteModelLimit,
  limitsQueryOptions,
  modelLimitsQueryKey,
  saveModelLimit,
  tenantOptionsQueryOptions,
} from './api'
import { AllowlistCard } from './components/allowlist-card'
import { LimitDialog } from './components/limit-dialog'
import { LimitsCard } from './components/limits-card'
import { TypedConfirmDialog } from './components/typed-confirm-dialog'
import { isForbidden } from './lib/forbidden'
import type { ModelLimit, ModelLimitBody, TenantOption } from './types'

type Dialog =
  | { kind: 'create' }
  | { kind: 'edit'; row: ModelLimit }
  | { kind: 'delete'; row: ModelLimit }
  | null

const DEFAULT_OPTIONS: TenantOption[] = [
  { id: DEFAULT_TENANT, name: DEFAULT_TENANT },
]

export function AdminModelLimitsPage() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [tenantId, setTenantId] = useState(DEFAULT_TENANT)
  const [dialog, setDialog] = useState<Dialog>(null)

  const tenants = useQuery(tenantOptionsQueryOptions)
  const limits = useQuery(limitsQueryOptions(tenantId))
  const allowlist = useQuery(allowlistQueryOptions(tenantId))

  const forbidden =
    isForbidden(tenants.error) ||
    isForbidden(limits.error) ||
    isForbidden(allowlist.error)

  const refreshLimits = () =>
    qc.invalidateQueries({ queryKey: [...modelLimitsQueryKey, 'limits'] })
  const fail = (e: unknown) => toast.error(toApiError(e).message)

  const save = useMutation({
    mutationFn: (v: { tenant: string; body: ModelLimitBody }) =>
      saveModelLimit(v.tenant, v.body),
    onSuccess: async () => {
      toast.success(t('Model limit saved'))
      setDialog(null)
      await refreshLimits()
    },
    onError: fail,
  })
  const remove = useMutation({
    mutationFn: (v: { tenant: string; model: string }) =>
      deleteModelLimit(v.tenant, v.model),
    onSuccess: async () => {
      toast.success(t('Model limit deleted'))
      setDialog(null)
      await refreshLimits()
    },
    onError: fail,
  })

  const options = tenants.data ?? DEFAULT_OPTIONS
  // A failed tenant read still leaves the bootstrap tenant usable.
  const tenantsFailed = tenants.isError && !isForbidden(tenants.error)

  const actions = forbidden ? null : (
    <>
      <NativeSelect
        size='sm'
        aria-label={t('Tenant')}
        value={tenantId}
        onChange={(e) => setTenantId(e.target.value)}
      >
        {options.map((o) => (
          <NativeSelectOption key={o.id} value={o.id}>
            {o.name}
          </NativeSelectOption>
        ))}
      </NativeSelect>
      <Button size='sm' onClick={() => setDialog({ kind: 'create' })}>
        <Plus />
        {t('New model limit')}
      </Button>
    </>
  )

  let content
  if (forbidden) {
    content = (
      <div data-testid='limits-forbidden'>
        <ErrorState
          icon={ShieldAlert}
          title={t('Admin access required')}
          description={t(
            'You do not have permission to manage model rate limits. Contact a platform administrator.'
          )}
        />
      </div>
    )
  } else {
    content = (
      <div className='mx-auto grid w-full max-w-5xl gap-4'>
        {tenantsFailed && (
          <p role='alert' className='text-warning text-sm'>
            {t(
              'The tenant list could not be loaded; only the default tenant is selectable. {{message}}',
              { message: toApiError(tenants.error).message }
            )}
          </p>
        )}
        <LimitsCard
          rows={limits.data}
          loading={limits.isPending}
          error={limits.error}
          onRetry={() => void limits.refetch()}
          onCreate={() => setDialog({ kind: 'create' })}
          onEdit={(row) => setDialog({ kind: 'edit', row })}
          onDelete={(row) => setDialog({ kind: 'delete', row })}
        />
        <AllowlistCard
          tenantId={tenantId}
          loading={allowlist.isPending}
          data={allowlist.data ?? null}
          version={allowlist.dataUpdatedAt}
          error={allowlist.error}
          onRetry={() => void allowlist.refetch()}
        />
      </div>
    )
  }

  const deleting = dialog?.kind === 'delete' ? dialog.row : null

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('Model limits & availability')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>{actions}</SectionPageLayout.Actions>
        <SectionPageLayout.Content>{content}</SectionPageLayout.Content>
      </SectionPageLayout>

      {(dialog?.kind === 'create' || dialog?.kind === 'edit') && (
        <LimitDialog
          key={dialog.kind === 'edit' ? dialog.row.id : 'new'}
          limit={dialog.kind === 'edit' ? dialog.row : undefined}
          tenantId={tenantId}
          saving={save.isPending}
          onSubmit={(body) => save.mutate({ tenant: tenantId, body })}
          onClose={() => setDialog(null)}
        />
      )}

      <TypedConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open) setDialog(null)
        }}
        title={t('Delete the {{model}} limit for this tenant?', {
          model: deleting?.model ?? '',
        })}
        desc={t('The model becomes uncapped for this tenant.')}
        expected={deleting?.model ?? ''}
        confirmText={t('Delete')}
        isLoading={remove.isPending}
        handleConfirm={() =>
          deleting && remove.mutate({ tenant: tenantId, model: deleting.model })
        }
      />
    </>
  )
}
