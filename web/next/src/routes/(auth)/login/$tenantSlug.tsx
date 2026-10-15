import { createFileRoute } from '@tanstack/react-router'

import { LoginPage } from '@/features/auth'

// Sign-in flow: no session guard, no app shell.
function Page() {
  const { tenantSlug } = Route.useParams()
  return <LoginPage tenantSlug={tenantSlug} />
}

export const Route = createFileRoute('/(auth)/login/$tenantSlug')({
  component: Page,
})
