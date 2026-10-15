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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

/** The phrase that arms the confirm button. */
export const PURGE_ALL_CONFIRM_TEXT = 'PURGE ALL'

/**
 * Typed confirmation for wiping every binding this replica can reach. The
 * confirm button stays disabled until the phrase matches exactly, so a stray
 * Enter or click can never do it.
 */
export function PurgeAllDialog(props: {
  open: boolean
  pending: boolean
  onClose: () => void
  onConfirm: () => void
}) {
  const { t } = useTranslation()
  const [typed, setTyped] = useState('')
  const armed = typed === PURGE_ALL_CONFIRM_TEXT

  // Whoever closes the dialog (cancel, success, Escape), the next opening
  // starts disarmed rather than with the phrase still typed.
  useEffect(() => {
    if (!props.open) setTyped('')
  }, [props.open])

  return (
    <ConfirmDialog
      open={props.open}
      onOpenChange={(open) => {
        if (!open && !props.pending) props.onClose()
      }}
      title={t('Purge every session-affinity binding this replica can reach?')}
      desc={t(
        'Every binding this replica can reach is dropped immediately. This cannot be undone.'
      )}
      confirmText={t('Purge all')}
      destructive
      disabled={!armed}
      isLoading={props.pending}
      handleConfirm={() => {
        if (armed) props.onConfirm()
      }}
    >
      <div className='flex flex-col gap-1.5'>
        <Label htmlFor='diag-purge-all-confirm'>
          {t('Type {{phrase}} to confirm', { phrase: PURGE_ALL_CONFIRM_TEXT })}
        </Label>
        <Input
          id='diag-purge-all-confirm'
          className='font-mono'
          autoComplete='off'
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
        />
      </div>
    </ConfirmDialog>
  )
}
