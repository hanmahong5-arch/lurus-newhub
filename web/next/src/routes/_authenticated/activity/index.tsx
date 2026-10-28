import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { ActivityPage } from '@/features/activity'
import { validateActivitySearch } from '@/features/activity/lib/search'

function Guarded() {
  const search = Route.useSearch()
  return (
    <RequireAccess requires='everyone'>
      <ActivityPage search={search} />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/activity/')({
  validateSearch: validateActivitySearch,
  component: Guarded,
})
