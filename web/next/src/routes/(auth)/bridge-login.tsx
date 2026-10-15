import { createFileRoute } from '@tanstack/react-router'

import { BridgeLoginPage } from '@/features/auth'

// Sign-in flow: no session guard, no app shell.
function Page() {
  return <BridgeLoginPage />
}

export const Route = createFileRoute('/(auth)/bridge-login')({
  component: Page,
})
