import type { ReactNode } from 'react'

export function PageHeader(props: {
  title: string
  description?: string
  actions?: ReactNode
}) {
  return (
    <div className='flex flex-wrap items-start justify-between gap-3'>
      <div className='space-y-1'>
        <h1 className='text-2xl font-semibold tracking-tight'>{props.title}</h1>
        {props.description != null && (
          <p className='text-muted-foreground text-sm'>{props.description}</p>
        )}
      </div>
      {props.actions != null && <div>{props.actions}</div>}
    </div>
  )
}
