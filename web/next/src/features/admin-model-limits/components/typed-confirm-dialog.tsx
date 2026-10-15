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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

interface TypedConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  desc: string
  /** The operator must type exactly this before the action unlocks. */
  expected: string
  confirmText: string
  isLoading?: boolean
  handleConfirm: () => void
}

/**
 * A destructive confirmation that cannot be clicked through: the action stays
 * locked until the operator types the name of the thing being changed. Used
 * for the writes that can silently lock a tenant out of every model.
 */
export function TypedConfirmDialog(props: TypedConfirmDialogProps) {
  const { t } = useTranslation()
  const [typed, setTyped] = useState('')
  const [wasOpen, setWasOpen] = useState(props.open)
  // Forget the previous attempt whenever the dialog closes.
  if (props.open !== wasOpen) {
    setWasOpen(props.open)
    if (!props.open) setTyped('')
  }

  return (
    <ConfirmDialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={props.title}
      desc={props.desc}
      confirmText={props.confirmText}
      destructive
      isLoading={props.isLoading}
      disabled={typed.trim() !== props.expected}
      handleConfirm={props.handleConfirm}
    >
      <div className='grid gap-1.5'>
        <Label htmlFor='typed-confirm-input'>
          {t('Type {{value}} to confirm', { value: props.expected })}
        </Label>
        <Input
          id='typed-confirm-input'
          value={typed}
          autoComplete='off'
          placeholder={props.expected}
          onChange={(e) => setTyped(e.target.value)}
        />
      </div>
    </ConfirmDialog>
  )
}
