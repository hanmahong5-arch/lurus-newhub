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
import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { StepUpCancelledError, useStepUp } from '@/components/step-up'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'

import { forceDisableTotp } from '../api'
import { REASON_MAX_LENGTH } from '../constants'
import type { AdminUser } from '../types'
import { useUsers } from './users-provider'

interface UserReset2faDialogProps {
  user: AdminUser | null
}

/**
 * Root-only escape hatch for a person locked out of their authenticator. The
 * server asks for a fresh verification of the acting operator, keeps the
 * reason in the audit trail and tells the affected person when it can.
 */
export function UserReset2faDialog({ user }: UserReset2faDialogProps) {
  const { t } = useTranslation()
  const { setDialog, refresh } = useUsers()
  const { run, dialog: stepUpDialog } = useStepUp()
  const [reason, setReason] = useState('')
  const trimmed = reason.trim()
  const length = [...trimmed].length
  const valid = length > 0 && length <= REASON_MAX_LENGTH

  const close = () => {
    setReason('')
    setDialog(null)
  }

  const disable = useMutation({
    mutationFn: (target: AdminUser) =>
      run(() => forceDisableTotp(target.id, trimmed)),
    onSuccess: (data) => {
      toast.success(
        data.notified
          ? t('Two-factor authentication reset. The user was notified.')
          : t(
              'Two-factor authentication reset. The user could not be notified.'
            )
      )
      close()
      refresh()
    },
    onError: (error) => {
      if (error instanceof StepUpCancelledError) return
      handleServerError(error, t('Failed to reset 2FA'))
    },
  })

  return (
    <>
      <Dialog
        open={user !== null}
        onOpenChange={(open) => {
          if (!open && !disable.isPending) close()
        }}
        title={t('Reset Two-Factor Authentication')}
        description={
          user
            ? t(
                'Reset 2FA for {{username}}? The user must set up 2FA again to continue using it.',
                { username: user.username }
              )
            : undefined
        }
        contentHeight='auto'
        contentClassName='sm:max-w-md'
        bodyClassName='space-y-2'
        footer={
          <>
            <Button
              variant='outline'
              onClick={close}
              disabled={disable.isPending}
            >
              {t('Cancel')}
            </Button>
            <Button
              variant='destructive'
              disabled={!valid || disable.isPending}
              onClick={() => user && disable.mutate(user)}
            >
              {disable.isPending ? t('Processing...') : t('Reset 2FA')}
            </Button>
          </>
        }
      >
        <Label htmlFor='user-reset-2fa-reason'>{t('Reason')}</Label>
        <Textarea
          id='user-reset-2fa-reason'
          rows={3}
          value={reason}
          maxLength={REASON_MAX_LENGTH}
          placeholder={t('Why is two-factor being reset? Kept in the audit log.')}
          onChange={(e) => setReason(e.target.value)}
        />
        <p className='text-muted-foreground text-xs tabular-nums'>
          {length}/{REASON_MAX_LENGTH}
        </p>
      </Dialog>
      {stepUpDialog}
    </>
  )
}
