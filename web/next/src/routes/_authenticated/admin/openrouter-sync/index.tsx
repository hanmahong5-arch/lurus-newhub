import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { AdminOpenRouterSyncPage } from '@/features/admin-openrouter-sync'

function Guarded() {
  return (
    <RequireAccess requires='platformStaff'>
      <AdminOpenRouterSyncPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/admin/openrouter-sync/')({
  component: Guarded,
})
