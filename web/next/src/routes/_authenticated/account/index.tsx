import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { AccountPage } from '@/features/account'

function Guarded() {
  return (
    <RequireAccess requires='everyone'>
      <AccountPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/account/')({
  component: Guarded,
})
