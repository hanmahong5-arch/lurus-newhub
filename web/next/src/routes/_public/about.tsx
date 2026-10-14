import { createFileRoute } from '@tanstack/react-router'

import { AboutPage } from '@/features/public-site'

function Page() {
  return <AboutPage />
}

export const Route = createFileRoute('/_public/about')({
  component: Page,
})
