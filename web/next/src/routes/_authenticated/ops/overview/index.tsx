import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { OpsOverviewPage } from '@/features/ops-overview'

function Guarded() {
  return (
    <RequireAccess requires='platformStaff'>
      <OpsOverviewPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/ops/overview/')({
  component: Guarded,
})
