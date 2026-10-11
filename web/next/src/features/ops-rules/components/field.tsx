import type { ReactNode } from 'react'

import { Label } from '@/components/ui/label'

export function Field(props: {
  label: string
  hint?: string
  children: ReactNode
}) {
  return (
    <div className='grid gap-1.5'>
      <Label>{props.label}</Label>
      {props.children}
      {props.hint != null && (
        <p className='text-muted-foreground text-xs'>{props.hint}</p>
      )}
    </div>
  )
}
