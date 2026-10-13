import { createFileRoute } from '@tanstack/react-router'

import { RequireAccess } from '@/components/layout/require-access'
import { TasksPage } from '@/features/tasks'

function Guarded() {
  return (
    <RequireAccess requires='everyone'>
      <TasksPage />
    </RequireAccess>
  )
}

export const Route = createFileRoute('/_authenticated/tasks/')({
  component: Guarded,
})
