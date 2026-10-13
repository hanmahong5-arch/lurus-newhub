import { createFileRoute } from '@tanstack/react-router'

import { PublicPricingPage } from '@/features/public-site'

function Page() {
  return <PublicPricingPage />
}

export const Route = createFileRoute('/_public/pricing/')({
  component: Page,
})
