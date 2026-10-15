import { createFileRoute } from '@tanstack/react-router'

import { SetupPage } from '@/features/auth'

// Sign-in flow: no session guard, no app shell.
function Page() {
  return <SetupPage />
}

export const Route = createFileRoute('/(auth)/setup')({
  component: Page,
})
