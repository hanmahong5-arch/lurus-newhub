import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { OrgBillingPage } from '@/features/org-billing'

function Guarded() {
  return (
    <RequireAccess requires='orgLead'>
      <OrgBillingPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/org/billing/')({
  component: Guarded,
})
