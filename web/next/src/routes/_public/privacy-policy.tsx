import { createFileRoute } from '@tanstack/react-router'

import { LegalPage } from '@/features/public-site'

function Page() {
  return <LegalPage kind='privacy-policy' />
}

export const Route = createFileRoute('/_public/privacy-policy')({
  component: Page,
})
