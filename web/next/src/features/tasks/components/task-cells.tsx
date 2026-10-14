/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { ExternalLink, Eye } from 'lucide-react'

import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'

export function Dash() {
  return <span className='text-muted-foreground/60 text-xs'>-</span>
}

/** Pill with the elapsed time; slow jobs read as a warning. */
export function DurationPill(props: { seconds: number | null; warnAfterSec: number }) {
  if (props.seconds === null) return <Dash />
  const slow = props.seconds > props.warnAfterSec
  return (
    <StatusBadge
      label={`${props.seconds.toFixed(1)}s`}
      variant={slow ? 'danger' : 'success'}
      size='sm'
      copyable={false}
      className='rounded-md font-mono'
    />
  )
}

export function ProgressPill(props: { value: string }) {
  if (!props.value) return <Dash />
  return (
    <span className='border-border/60 bg-muted/30 inline-flex items-center rounded-md border px-1.5 py-0.5 font-mono text-xs'>
      {props.value}
    </span>
  )
}

export function ExternalLinkButton(props: { href: string; label: string }) {
  return (
    <a
      href={props.href}
      target='_blank'
      rel='noopener noreferrer'
      onClick={(e) => e.stopPropagation()}
      className='text-primary inline-flex items-center gap-1 text-xs font-medium hover:underline'
    >
      <ExternalLink className='size-3' aria-hidden='true' />
      {props.label}
    </a>
  )
}

export function DetailsCell(props: {
  failReason: string
  onOpen: () => void
  label: string
}) {
  return (
    <div className='flex max-w-[220px] flex-col items-start gap-1'>
      <Button
        type='button'
        variant='ghost'
        size='xs'
        className='-ml-2 gap-1'
        onClick={(e) => {
          e.stopPropagation()
          props.onOpen()
        }}
      >
        <Eye className='size-3' aria-hidden='true' />
        {props.label}
      </Button>
      {props.failReason && (
        <span className='max-w-full truncate text-xs text-red-600 dark:text-red-400'>
          {props.failReason}
        </span>
      )}
    </div>
  )
}
