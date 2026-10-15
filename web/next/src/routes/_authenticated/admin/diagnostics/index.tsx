import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { AdminDiagnosticsPage } from '@/features/admin-diagnostics'

function Guarded() {
  return (
    <RequireAccess requires='platformStaff'>
      <AdminDiagnosticsPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/admin/diagnostics/')({
  component: Guarded,
})
