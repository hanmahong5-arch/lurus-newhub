import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { SystemSettingsPage } from '@/features/system-settings'

function Guarded() {
  const { section } = Route.useParams()
  return (
    <RequireAccess requires='platformRoot'>
      <SystemSettingsPage section={section} />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/admin/settings/$section')({
  component: Guarded,
})
