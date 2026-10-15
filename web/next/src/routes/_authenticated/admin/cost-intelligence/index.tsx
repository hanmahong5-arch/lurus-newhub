import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { AdminCostPage } from '@/features/admin-cost'

function Guarded() {
  return (
    <RequireAccess requires='platformRoot'>
      <AdminCostPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/admin/cost-intelligence/')({
  component: Guarded,
})
