import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { OrgKeysBatchPage } from '@/features/org-keys-batch'

function Guarded() {
  return (
    <RequireAccess requires='tenantAdmin'>
      <OrgKeysBatchPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/org/keys-batch/')({
  component: Guarded,
})
