import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { OrgPricingPage } from '@/features/org-pricing'

function Guarded() {
  return (
    <RequireAccess requires='tenantAdmin'>
      <OrgPricingPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/org/pricing/')({
  component: Guarded,
})
