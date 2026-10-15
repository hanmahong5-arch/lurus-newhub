import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { OrgFlowsPage } from '@/features/org-flows'

function Guarded() {
  return (
    <RequireAccess requires='tenantAdmin'>
      <OrgFlowsPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/org/flows/')({
  component: Guarded,
})
