import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { PlaygroundPage } from '@/features/playground'

function Guarded() {
  return (
    <RequireAccess requires='everyone'>
      <PlaygroundPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/playground/')({
  component: Guarded,
})
