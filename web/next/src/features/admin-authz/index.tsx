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
import { RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { SectionPageLayout } from '@/components/section-page-layout'
import { Button } from '@/components/ui/button'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { authzKeys, fetchCatalog, fetchGrants, revokeGrant } from './api'
import { CatalogCard } from './components/catalog-card'
import { GrantForm } from './components/grant-form'
import { GrantsTable } from './components/grants-table'
import { classifyReadError, combineFailures } from './lib/load-status'
import type { Grant } from './types'

export function AdminAuthzPage() {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [revoking, setRevoking] = useState<Grant | null>(null)

  // errorToast:false: the failure is rendered inline below; the global
  // handler would add a second, contextless toast on top of it.
  const catalog = useQuery({
    queryKey: authzKeys.catalog,
    queryFn: fetchCatalog,
    retry: false,
    meta: { errorToast: false },
  })
  const grants = useQuery({
    queryKey: authzKeys.grants,
    queryFn: fetchGrants,
    retry: false,
    meta: { errorToast: false },
  })

  const revoke = useMutation({
    mutationFn: (id: number) => revokeGrant(id),
    meta: { errorToast: false },
    onSuccess: () => toast.success(t('Grant revoked')),
    onError: (error) =>
      toast.error(getServerErrorMessage(error, t('Failed to revoke grant'))),
    // Win or lose, close the prompt and re-read: a 404 means the row was
    // already withdrawn elsewhere and the list is stale.
    onSettled: async () => {
      setRevoking(null)
      await qc.invalidateQueries({ queryKey: authzKeys.grants })
    },
  })

  const refresh = async () => {
    await Promise.all([catalog.refetch(), grants.refetch()])
  }

  // Never leave half a page up: a create form with an unread catalog offers no
  // resources, and a stale list would outlive the failure. Either read failing
  // is a failed page, and a refusal outranks a plain error.
  const failure = combineFailures([
    catalog.isError ? classifyReadError(catalog.error) : null,
    grants.isError ? classifyReadError(grants.error) : null,
  ])

  let content: React.ReactNode
  if (failure === 'forbidden') {
    content = (
      <ErrorState
        title={t('Root access required')}
        description={t('Only root can manage delegated permission grants.')}
      />
    )
  } else if (failure) {
    // Not "No grants issued yet.": that sentence states who holds delegated
    // root powers, and a read that failed found out nothing.
    content = (
      <div data-testid='authz-load-error'>
        <ErrorState
          title={t("Couldn't load delegated grants")}
          description={getServerErrorMessage(
            catalog.error ?? grants.error,
            t('The request failed. Try again.')
          )}
          onRetry={() => void refresh()}
        />
      </div>
    )
  } else if (!catalog.data || !grants.data) {
    content = <LoadingState />
  } else {
    content = (
      <div className='flex flex-col gap-4'>
        <GrantForm catalog={catalog.data} />
        <GrantsTable grants={grants.data} onRevoke={setRevoking} />
        <CatalogCard catalog={catalog.data} />
      </div>
    )
  }

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('Delegated admin permissions')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button
            size='sm'
            variant='outline'
            onClick={() => void refresh()}
            disabled={catalog.isFetching || grants.isFetching}
          >
            <RefreshCw className='size-4' />
            {t('Refresh')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>{content}</SectionPageLayout.Content>
      </SectionPageLayout>

      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(open) => {
          if (!open && !revoke.isPending) setRevoking(null)
        }}
        title={t('Revoke this grant?')}
        desc={
          revoking
            ? t(
                'User {{user}} will lose {{resource}} / {{action}} immediately. They keep their admin role.',
                {
                  user: revoking.user_id,
                  resource: revoking.resource,
                  action: revoking.action,
                }
              )
            : ''
        }
        confirmText={t('Revoke')}
        destructive
        isLoading={revoke.isPending}
        handleConfirm={() => revoking && revoke.mutate(revoking.id)}
      />
    </>
  )
}
