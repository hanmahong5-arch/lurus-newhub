import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { AdminModelLimitsPage } from '@/features/admin-model-limits'

function Guarded() {
  return (
    <RequireAccess requires='platformStaff'>
      <AdminModelLimitsPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/admin/model-limits/')({
  component: Guarded,
})
