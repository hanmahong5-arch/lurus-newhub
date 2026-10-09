import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { OrgMembersPage } from '@/features/org-members'

function Guarded() {
  return (
    <RequireAccess requires='tenantAdmin'>
      <OrgMembersPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/org/members/')({
  component: Guarded,
})
