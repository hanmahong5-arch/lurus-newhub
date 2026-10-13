import { createFileRoute } from '@tanstack/react-router'

import { PublicModelDetailPage } from '@/features/public-site'

function Page() {
  const { modelId } = Route.useParams()
  return <PublicModelDetailPage modelId={modelId} />
}

export const Route = createFileRoute('/_public/pricing/$modelId')({
  component: Page,
})
