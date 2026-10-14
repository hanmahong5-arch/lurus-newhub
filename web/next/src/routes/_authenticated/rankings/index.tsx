import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { RankingsPage } from '@/features/rankings'

function Guarded() {
  return (
    <RequireAccess requires='tenantAdmin'>
      <RankingsPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/rankings/')({
  component: Guarded,
})
