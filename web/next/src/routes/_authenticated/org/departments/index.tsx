import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { OrgDepartmentsPage } from '@/features/org-departments'

function Guarded() {
  return (
    <RequireAccess requires='orgLead'>
      <OrgDepartmentsPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/org/departments/')({
  component: Guarded,
})
