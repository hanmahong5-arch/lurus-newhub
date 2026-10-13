import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { AdminUsersPage } from '@/features/admin-users'

function Guarded() {
  return (
    <RequireAccess requires='platformStaff'>
      <AdminUsersPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/admin/users/')({
  component: Guarded,
})
