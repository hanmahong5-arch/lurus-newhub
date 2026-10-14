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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, RefreshCw, Shield } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { StatusBadge } from '@/components/status-badge'
import { useStepUp } from '@/components/step-up'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { toApiError } from '@/lib/api'

import { accountKeys, fetchTotpStatus } from '../api'
import {
  TwoFaBackupDialog,
  TwoFaDisableDialog,
  TwoFaSetupDialog,
} from './two-fa-dialogs'

type DialogKey = 'setup' | 'backup' | 'disable' | null

/**
 * Authenticator-app factor. It is the step-up factor for sensitive actions in
 * this console (channel keys, backup codes, disabling it); the platform still
 * performs the actual sign-in.
 */
export function TwoFaCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { run, dialog } = useStepUp()
  const [open, setOpen] = useState<DialogKey>(null)

  const status = useQuery({
    queryKey: accountKeys.totp,
    queryFn: fetchTotpStatus,
    retry: false,
  })
  const refresh = () =>
    void queryClient.invalidateQueries({ queryKey: accountKeys.totp })
  const closeDialogs = (next: boolean, key: Exclude<DialogKey, null>) =>
    setOpen(next ? key : null)

  let body: React.ReactNode
  if (status.isPending) {
    body = <Skeleton className='h-20 w-full' />
  } else if (status.isError) {
    body = (
      <ErrorState
        className='min-h-0 py-4'
        title={t('Unable to load two-factor status')}
        description={toApiError(status.error).message}
        onRetry={() => void status.refetch()}
      />
    )
  } else {
    const { enrolled, pending, backup_codes_remaining: remaining } = status.data
    body = (
      <div className='space-y-6'>
        <div className='flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between'>
          <div className='flex items-start gap-4'>
            <IconBadge tone='success' size='sm'>
              <Shield />
            </IconBadge>
            <div className='space-y-1'>
              <div className='flex flex-wrap items-center gap-2'>
                <p className='font-medium'>{t('Two-Step Verification')}</p>
                <StatusBadge
                  label={enrolled ? t('Enabled') : t('Disabled')}
                  variant={enrolled ? 'success' : 'neutral'}
                  copyable={false}
                />
                {pending && !enrolled && (
                  <StatusBadge
                    label={t('Setup not finished')}
                    variant='warning'
                    copyable={false}
                  />
                )}
              </div>
              <p className='text-muted-foreground text-sm'>
                {enrolled
                  ? t('Backup codes remaining: {{count}}', { count: remaining })
                  : t('Add an extra layer of security to your account')}
              </p>
            </div>
          </div>
          {!enrolled && (
            <Button
              className='w-full sm:w-auto'
              onClick={() => setOpen('setup')}
            >
              {pending ? t('Continue setup') : t('Enable')}
            </Button>
          )}
        </div>

        {enrolled && remaining === 0 && (
          <Alert variant='destructive'>
            <AlertTriangle className='size-4' />
            <AlertDescription>
              {t(
                'You have no unused backup codes left. Generate new ones before you lose access to your authenticator.'
              )}
            </AlertDescription>
          </Alert>
        )}

        {enrolled && (
          <div className='flex flex-col gap-3 border-t pt-6 sm:flex-row'>
            <Button
              variant='outline'
              className='flex-1'
              onClick={() => setOpen('backup')}
            >
              <RefreshCw className='size-4' />
              {t('Regenerate Backup Codes')}
            </Button>
            <Button
              variant='destructive'
              className='flex-1'
              onClick={() => setOpen('disable')}
            >
              <AlertTriangle className='size-4' />
              {t('Disable 2FA')}
            </Button>
          </div>
        )}
      </div>
    )
  }

  return (
    <>
      <Card data-card-hover='false' className='gap-0 overflow-hidden py-0'>
        <CardHeader className='p-3 sm:p-5'>
          <CardTitle className='text-lg tracking-tight sm:text-xl'>
            {t('Two-Factor Authentication')}
          </CardTitle>
          <CardDescription className='text-xs sm:text-sm'>
            {t(
              'Used to confirm sensitive actions in this console, such as revealing channel keys.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className='p-3 sm:p-5'>{body}</CardContent>
      </Card>

      <TwoFaSetupDialog
        open={open === 'setup'}
        onOpenChange={(next) => closeDialogs(next, 'setup')}
        onEnabled={refresh}
      />
      <TwoFaBackupDialog
        open={open === 'backup'}
        onOpenChange={(next) => closeDialogs(next, 'backup')}
        run={run}
        onDone={refresh}
      />
      <TwoFaDisableDialog
        open={open === 'disable'}
        onOpenChange={(next) => closeDialogs(next, 'disable')}
        run={run}
        onDone={refresh}
      />
      {dialog}
    </>
  )
}
