import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { ChatPage } from '@/features/chat'

function Guarded() {
  return (
    <RequireAccess requires='everyone'>
      <ChatPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/chat/')({
  component: Guarded,
})
