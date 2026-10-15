import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { AdminAuthzPage } from '@/features/admin-authz'

function Guarded() {
  return (
    <RequireAccess requires='platformStaff'>
      <AdminAuthzPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/admin/authz/')({
  component: Guarded,
})
