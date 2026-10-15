import { createFileRoute } from '@tanstack/react-router'

import { LoginPage } from '@/features/auth'

// Sign-in flow: no session guard, no app shell.
function Page() {
  return <LoginPage />
}

export const Route = createFileRoute('/(auth)/login/')({
  component: Page,
})
