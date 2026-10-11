import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { OpsRulesPage } from '@/features/ops-rules'

function Guarded() {
  return (
    <RequireAccess requires='platformStaff'>
      <OpsRulesPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/ops/rules/')({
  component: Guarded,
})
