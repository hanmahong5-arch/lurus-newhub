import { createFileRoute } from '@tanstack/react-router'

import { ForbiddenPage } from '@/features/auth'

// Sign-in flow: no session guard, no app shell.
function Page() {
  return <ForbiddenPage />
}

export const Route = createFileRoute('/(auth)/forbidden')({
  component: Page,
})
