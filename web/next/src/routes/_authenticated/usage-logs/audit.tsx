import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { AuditLogPage } from '@/features/usage-logs/audit'

function Guarded() {
  return (
    <RequireAccess requires='tenantAdmin'>
      <AuditLogPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/usage-logs/audit')({
  component: Guarded,
})
