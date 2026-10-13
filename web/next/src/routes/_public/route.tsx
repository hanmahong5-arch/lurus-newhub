import { createFileRoute } from '@tanstack/react-router'

import { PublicLayout } from '@/features/public-site/layout'

// Pages anyone may open without a session: landing, public price list,
// about and the legal texts. No auth guard here on purpose.
export const Route = createFileRoute('/_public')({
  component: PublicLayout,
})
