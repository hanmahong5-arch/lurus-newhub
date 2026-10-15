import { createFileRoute } from '@tanstack/react-router'

import { RegisterPage } from '@/features/auth'

// Sign-in flow: no session guard, no app shell.
function Page() {
  return <RegisterPage />
}

export const Route = createFileRoute('/(auth)/register')({
  component: Page,
})
