import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { SystemSettingsPage } from '@/features/system-settings'

function Guarded() {
  return (
    <RequireAccess requires='platformStaff'>
      <SystemSettingsPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/admin/settings/')({
  component: Guarded,
})
