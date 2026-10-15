import { createFileRoute } from '@tanstack/react-router'

import { AccountDisabledPage } from '@/features/auth'

// Sign-in flow: no session guard, no app shell.
function Page() {
  return <AccountDisabledPage />
}

export const Route = createFileRoute('/(auth)/account-disabled')({
  component: Page,
})
