import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { AdminSystemTasksPage } from '@/features/admin-system-tasks'

function Guarded() {
  return (
    <RequireAccess requires='platformStaff'>
      <AdminSystemTasksPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/admin/system-tasks/')({
  component: Guarded,
})
