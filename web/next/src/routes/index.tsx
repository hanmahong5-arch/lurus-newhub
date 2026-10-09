import { createFileRoute, redirect } from '@tanstack/react-router'

// /next/ -> /next/dashboard
export const Route = createFileRoute('/')({
  beforeLoad: () => {
    throw redirect({ to: '/dashboard', replace: true })
  },
})
