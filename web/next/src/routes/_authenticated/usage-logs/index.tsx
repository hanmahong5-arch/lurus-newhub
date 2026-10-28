import { createFileRoute } from '@tanstack/react-router'

import { UsageLogsPage } from '@/features/usage-logs'
import { validateUsageLogsSearch } from '@/features/usage-logs/lib/url-search'

function Page() {
  const search = Route.useSearch()
  return <UsageLogsPage search={search} />
}

export const Route = createFileRoute('/_authenticated/usage-logs/')({
  validateSearch: validateUsageLogsSearch,
  component: Page,
})
