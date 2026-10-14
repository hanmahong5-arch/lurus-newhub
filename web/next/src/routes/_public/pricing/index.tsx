import { createFileRoute } from '@tanstack/react-router'

import { PublicPricingPage } from '@/features/public-site'
import { validatePricingSearch } from '@/features/public-site/pricing/constants'

function Page() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <PublicPricingPage
      search={search}
      // Filters live in the URL so a filtered list can be shared; a keystroke
      // or a chip click replaces the entry instead of stacking history.
      onSearchChange={(next) => void navigate({ search: next, replace: true })}
      onOpenModel={(model) =>
        void navigate({
          to: '/pricing/$modelId',
          params: { modelId: model.id },
          search,
        })
      }
    />
  )
}

export const Route = createFileRoute('/_public/pricing/')({
  validateSearch: validatePricingSearch,
  component: Page,
})
