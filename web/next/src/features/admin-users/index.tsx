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
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/section-page-layout'

import { UserQuotaDialog } from './components/user-quota-dialog'
import { UserReset2faDialog } from './components/user-reset-2fa-dialog'
import { UsersDeleteDialog } from './components/users-delete-dialog'
import { UsersEditDrawer } from './components/users-edit-drawer'
import { UsersProvider, useUsers } from './components/users-provider'
import { UsersTable } from './components/users-table'

function UsersContent() {
  const { t } = useTranslation()
  const { dialog, quotaUser } = useUsers()

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>{t('Users')}</SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <UsersTable />
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <UsersEditDrawer user={dialog?.kind === 'edit' ? dialog.user : null} />
      <UserQuotaDialog user={quotaUser} />
      <UsersDeleteDialog
        user={dialog?.kind === 'delete' ? dialog.user : null}
      />
      <UserReset2faDialog
        user={dialog?.kind === 'force-disable-2fa' ? dialog.user : null}
      />
    </>
  )
}

export function AdminUsersPage() {
  return (
    <UsersProvider>
      <UsersContent />
    </UsersProvider>
  )
}
