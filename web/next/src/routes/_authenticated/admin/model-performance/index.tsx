import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { AdminModelPerformancePage } from '@/features/admin-model-performance'

function Guarded() {
  return (
    <RequireAccess requires='platformRoot'>
      <AdminModelPerformancePage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/admin/model-performance/')({
  component: Guarded,
})
