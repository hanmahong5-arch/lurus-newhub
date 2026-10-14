import { createFileRoute } from '@tanstack/react-router'

import { PublicModelDetailPage } from '@/features/public-site'
import { validatePricingSearch } from '@/features/public-site/pricing/constants'

function Page() {
  const { modelId } = Route.useParams()
  const search = Route.useSearch()
  return <PublicModelDetailPage modelId={modelId} search={search} />
}

// The detail page carries the list's filters so "Back" returns to them.
export const Route = createFileRoute('/_public/pricing/$modelId')({
  validateSearch: validatePricingSearch,
  component: Page,
})
