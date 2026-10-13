import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { RedemptionCodesPage } from '@/features/redemption-codes'

function Guarded() {
  return (
    <RequireAccess requires='tenantAdmin'>
      <RedemptionCodesPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/admin/redemption-codes/')({
  component: Guarded,
})
