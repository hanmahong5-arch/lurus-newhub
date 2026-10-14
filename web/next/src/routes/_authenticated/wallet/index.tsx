import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { WalletPage } from '@/features/wallet'

function Guarded() {
  return (
    <RequireAccess requires='everyone'>
      <WalletPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/wallet/')({
  component: Guarded,
})
