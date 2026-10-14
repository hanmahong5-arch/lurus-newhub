import { createFileRoute } from '@tanstack/react-router'

import { LegalPage } from '@/features/public-site'

function Page() {
  return <LegalPage kind='user-agreement' />
}

export const Route = createFileRoute('/_public/user-agreement')({
  component: Page,
})
