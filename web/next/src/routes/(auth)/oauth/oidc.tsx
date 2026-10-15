import { createFileRoute } from '@tanstack/react-router'

import { OidcCallbackPage } from '@/features/auth'

// Sign-in flow: no session guard, no app shell.
function Page() {
  return <OidcCallbackPage />
}

export const Route = createFileRoute('/(auth)/oauth/oidc')({
  component: Page,
})
