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
import { useQuery } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { SectionPageLayout } from '@/components/section-page-layout'
import { Button } from '@/components/ui/button'
import { useIsAdmin } from '@/hooks/use-admin'
import { ROLE } from '@/lib/auth'
import { currentUserQueryOptions } from '@/lib/user'

import { RedemptionsDeleteDialog } from './components/redemptions-delete-dialog'
import { RedemptionsExportDialog } from './components/redemptions-export-dialog'
import { RedemptionsMutateDrawer } from './components/redemptions-mutate-drawer'
import {
  RedemptionsProvider,
  useRedemptions,
} from './components/redemptions-provider'
import { RedemptionsTable } from './components/redemptions-table'

function RedemptionsContent() {
  const { t } = useTranslation()
  const { dialog, setDialog } = useRedemptions()
  const { data: viewer } = useQuery(currentUserQueryOptions)
  const isRoot = (viewer?.role ?? 0) >= ROLE.root

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>{t('Redemption Codes')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <div className='flex flex-wrap gap-2'>
            <Button
              size='sm'
              variant='outline'
              onClick={() => setDialog({ kind: 'delete-invalid' })}
            >
              <Trash2 className='text-destructive h-4 w-4' />
              {t('Delete Invalid')}
            </Button>
            <Button size='sm' onClick={() => setDialog({ kind: 'create' })}>
              <Plus className='h-4 w-4' />
              {t('Create Code')}
            </Button>
          </div>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <RedemptionsTable />
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <RedemptionsMutateDrawer
        open={dialog?.kind === 'create' || dialog?.kind === 'edit'}
        redemption={dialog?.kind === 'edit' ? dialog.redemption : undefined}
      />
      <RedemptionsExportDialog
        batch={dialog?.kind === 'created' ? dialog.batch : null}
      />
      <RedemptionsDeleteDialog
        redemption={dialog?.kind === 'delete' ? dialog.redemption : null}
        invalidOpen={dialog?.kind === 'delete-invalid'}
        global={isRoot}
      />
    </>
  )
}

/**
 * The tenant routes behind this page answer to platform staff (role >= admin).
 * A tenant admin who is not staff is told so here rather than being shown a
 * table that can only fail with 403.
 */
export function RedemptionCodesPage() {
  const { t } = useTranslation()
  const isStaff = useIsAdmin()
  const { isLoading } = useQuery(currentUserQueryOptions)

  // The tenant routes answer to staff only: do not fire a list request (and
  // flash a table) before the viewer is known.
  if (isLoading) return <LoadingState />

  if (!isStaff) {
    return (
      <ErrorState
        title={t('Administrator access required')}
        description={t(
          'Managing redemption codes needs a platform administrator account.'
        )}
      />
    )
  }

  return (
    <RedemptionsProvider>
      <RedemptionsContent />
    </RedemptionsProvider>
  )
}
