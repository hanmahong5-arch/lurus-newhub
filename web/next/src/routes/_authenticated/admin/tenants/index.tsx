import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { AdminTenantsPage } from '@/features/admin-tenants'

function Guarded() {
  return (
    <RequireAccess requires='platformRoot'>
      <AdminTenantsPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/admin/tenants/')({
  component: Guarded,
})
