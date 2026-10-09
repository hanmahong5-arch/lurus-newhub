import { createFileRoute } from '@tanstack/react-router'

import { AppShell } from '@/components/layout/app-shell'
import { currentUserQueryOptions } from '@/lib/user'

export const Route = createFileRoute('/_authenticated')({
  // Resolving the current user is the session check. A 401 sends the browser
  // to the legacy /login?redirect=<this page> (lib/api.ts), so this loader
  // either yields a live session or the page is already being left.
  beforeLoad: async ({ context }) => {
    await context.queryClient.ensureQueryData(currentUserQueryOptions)
  },
  component: AppShell,
})
