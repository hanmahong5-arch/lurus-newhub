import { createFileRoute } from '@tanstack/react-router'

import { UsageLogsPage } from '@/features/usage-logs'

export const Route = createFileRoute('/_authenticated/usage-logs/')({
  component: UsageLogsPage,
})
