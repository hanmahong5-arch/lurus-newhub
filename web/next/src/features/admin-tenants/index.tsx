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
import { Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/section-page-layout'
import { Button } from '@/components/ui/button'

import { TenantFormSheet } from './components/tenant-form-sheet'
import { TenantInvitesSheet } from './components/tenant-invites-sheet'
import { TenantLimitsDialog } from './components/tenant-limits-dialog'
import { TenantPoolSheet } from './components/tenant-pool-sheet'
import { TenantStatsSheet } from './components/tenant-stats-sheet'
import { TenantStatusDialog } from './components/tenant-status-dialog'
import { TenantsProvider, useTenants } from './components/tenants-provider'
import { TenantsTable } from './components/tenants-table'

function TenantsContent() {
  const { t } = useTranslation()
  const { dialog, setDialog } = useTenants()

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>{t('Tenants')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button size='sm' onClick={() => setDialog({ kind: 'create' })}>
            <Plus className='h-4 w-4' />
            {t('New tenant')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <TenantsTable />
        </SectionPageLayout.Content>
      </SectionPageLayout>

      {dialog?.kind === 'create' && <TenantFormSheet />}
      {dialog?.kind === 'edit' && (
        <TenantFormSheet key={dialog.tenant.id} tenant={dialog.tenant} />
      )}
      {dialog?.kind === 'stats' && (
        <TenantStatsSheet key={dialog.tenant.id} tenant={dialog.tenant} />
      )}
      {dialog?.kind === 'pool' && (
        <TenantPoolSheet key={dialog.tenant.id} tenant={dialog.tenant} />
      )}
      {dialog?.kind === 'invites' && (
        <TenantInvitesSheet key={dialog.tenant.id} tenant={dialog.tenant} />
      )}
      {dialog?.kind === 'limits' && (
        <TenantLimitsDialog key={dialog.tenant.id} tenant={dialog.tenant} />
      )}
      {dialog?.kind === 'status' && (
        <TenantStatusDialog
          key={`${dialog.tenant.id}:${dialog.action}`}
          tenant={dialog.tenant}
          action={dialog.action}
        />
      )}
    </>
  )
}

/**
 * Platform tenant administration: list, create and edit tenants, change their
 * status, and manage each one's credit pool, invites, stats and rate limits.
 * The route gates the page to platform staff; the server enforces it.
 */
export function AdminTenantsPage() {
  return (
    <TenantsProvider>
      <TenantsContent />
    </TenantsProvider>
  )
}
