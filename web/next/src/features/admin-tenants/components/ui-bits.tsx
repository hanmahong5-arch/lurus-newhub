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
import type { ReactNode } from 'react'

import { sideDrawerSwitchItemClassName } from '@/components/drawer-layout'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'

/** A labelled form row with optional helper and error text. */
export function Field(props: {
  label: string
  htmlFor?: string
  hint?: string
  error?: string
  children: ReactNode
}) {
  return (
    <div className='grid gap-1.5'>
      <Label htmlFor={props.htmlFor}>{props.label}</Label>
      {props.children}
      {props.error != null ? (
        <p className='text-destructive text-xs' role='alert'>
          {props.error}
        </p>
      ) : (
        props.hint != null && (
          <p className='text-muted-foreground text-xs'>{props.hint}</p>
        )
      )}
    </div>
  )
}

export function SwitchRow(props: {
  label: string
  description?: string
  checked: boolean
  disabled?: boolean
  onChange: (value: boolean) => void
}) {
  return (
    <div className={sideDrawerSwitchItemClassName()}>
      <div className='flex flex-col gap-0.5'>
        <Label className='text-sm'>{props.label}</Label>
        {props.description != null && (
          <p className='text-muted-foreground text-xs'>{props.description}</p>
        )}
      </div>
      <Switch
        checked={props.checked}
        disabled={props.disabled}
        onCheckedChange={props.onChange}
        aria-label={props.label}
      />
    </div>
  )
}

/** A per-minute or seat limit; zero or less is "no limit". */
export function Limit({ value }: { value: number }) {
  return value > 0 ? (
    <span className='tabular-nums'>{value.toLocaleString()}</span>
  ) : (
    <span className='text-muted-foreground'>∞</span>
  )
}
