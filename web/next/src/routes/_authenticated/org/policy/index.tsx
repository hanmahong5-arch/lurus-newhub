import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { OrgPolicyPage } from '@/features/org-policy'

function Guarded() {
  return (
    <RequireAccess requires='tenantAdmin'>
      <OrgPolicyPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/org/policy/')({
  component: Guarded,
})
