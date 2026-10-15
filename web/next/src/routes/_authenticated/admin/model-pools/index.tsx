import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { AdminModelPoolsPage } from '@/features/admin-model-pools'

function Guarded() {
  return (
    <RequireAccess requires='platformStaff'>
      <AdminModelPoolsPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/admin/model-pools/')({
  component: Guarded,
})
