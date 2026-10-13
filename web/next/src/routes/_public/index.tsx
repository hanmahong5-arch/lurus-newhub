import { createFileRoute } from '@tanstack/react-router'

import { HomePage } from '@/features/public-site'

function Page() {
  return <HomePage />
}

export const Route = createFileRoute('/_public/')({
  component: Page,
})
