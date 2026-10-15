import { createFileRoute, useNavigate } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { ChatPage } from '@/features/chat'

interface ChatSearch {
  session?: number
}

function Guarded() {
  const { session } = Route.useSearch()
  const navigate = useNavigate()
  return (
    <RequireAccess requires='everyone'>
      <ChatPage
        sessionParam={session}
        onSessionChange={(id) =>
          void navigate({
            to: '/chat',
            search: id === null ? {} : { session: id },
            replace: true,
          })
        }
      />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/chat/')({
  // A positive integer or nothing: anything else in the URL is ignored.
  validateSearch: (search: Record<string, unknown>): ChatSearch => {
    const id = Number(search.session)
    return Number.isInteger(id) && id > 0 ? { session: id } : {}
  },
  component: Guarded,
})
