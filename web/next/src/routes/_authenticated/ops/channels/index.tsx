import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { OpsChannelsPage } from '@/features/ops-channels'

function Guarded() {
  return (
    <RequireAccess requires='platformStaff'>
      <OpsChannelsPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/ops/channels/')({
  component: Guarded,
})
