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
import { useQuery } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  InputOTP,
  InputOTPGroup,
  InputOTPSlot,
} from '@/components/ui/input-otp'
import { toApiError } from '@/lib/api'

import { stepUpApi, type StepUpMethod } from './api'

type StepUpDialogProps = {
  open: boolean
  /** The session now holds a fresh step-up grant. */
  onVerified: () => void
  /** Closed without verifying. */
  onCancel: () => void
}

/**
 * Re-verification prompt for routes behind SecureVerificationRequired.
 * Enrolled accounts type a TOTP (or backup) code; an unenrolled account is
 * verified by the session alone when the server policy allows it, otherwise it
 * is told to enrol first. The 5-minute grant lives server-side in the session.
 */
export function StepUpDialog(props: StepUpDialogProps) {
  const { t } = useTranslation()
  const [useBackup, setUseBackup] = useState(false)
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const autoTried = useRef(false)

  const status = useQuery({
    queryKey: ['step-up-status'],
    queryFn: stepUpApi.status,
    enabled: props.open,
    gcTime: 0,
    staleTime: 0,
    retry: false,
  })
  const enrolled = status.data?.totp_enrolled === true
  const enrollmentRequired = status.data?.enrollment_required === true

  async function submit(method: StepUpMethod, value?: string) {
    setBusy(true)
    setError(null)
    try {
      await stepUpApi.verify(method, value)
      props.onVerified()
    } catch (err) {
      setError(toApiError(err).message)
      setCode('')
    } finally {
      setBusy(false)
    }
  }

  // Reset between uses.
  useEffect(() => {
    if (!props.open) {
      autoTried.current = false
      setUseBackup(false)
      setCode('')
      setError(null)
    }
  }, [props.open])

  // No second factor to ask for: let the session vouch for itself once.
  useEffect(() => {
    if (!props.open || !status.data || autoTried.current) return
    if (status.data.totp_enrolled || status.data.enrollment_required) return
    autoTried.current = true
    void submit('session')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.open, status.data])

  const method: StepUpMethod = useBackup ? 'totp_backup' : 'totp'
  const complete = useBackup ? code.trim().length > 0 : code.length === 6

  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => {
        if (!open && !busy) props.onCancel()
      }}
      title={t('Verify your identity')}
      description={t(
        'This action needs a fresh verification. It stays valid for a few minutes.'
      )}
      contentClassName='sm:max-w-md'
      footer={
        enrolled ? (
          <>
            <Button variant='outline' onClick={props.onCancel} disabled={busy}>
              {t('Cancel')}
            </Button>
            <Button
              onClick={() => void submit(method, code.trim())}
              disabled={busy || !complete}
            >
              {busy ? t('Verifying...') : t('Verify')}
            </Button>
          </>
        ) : (
          <Button variant='outline' onClick={props.onCancel} disabled={busy}>
            {t('Cancel')}
          </Button>
        )
      }
    >
      <div className='space-y-4' data-testid='step-up-body'>
        {status.isLoading && (
          <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
        )}
        {status.isError && (
          <p role='alert' className='text-destructive text-sm'>
            {toApiError(status.error).message}
          </p>
        )}
        {status.data && !enrolled && enrollmentRequired && (
          <p role='alert' className='text-sm'>
            {t(
              'Two-factor authentication must be set up before this action. Enable it in your account security settings, then try again.'
            )}
          </p>
        )}
        {enrolled && (
          <form
            className='space-y-3'
            onSubmit={(event) => {
              event.preventDefault()
              if (complete && !busy) void submit(method, code.trim())
            }}
          >
            <p className='text-sm'>
              {useBackup
                ? t('Enter one of your unused backup codes.')
                : t('Enter the 6-digit code from your authenticator app.')}
            </p>
            {useBackup ? (
              <Input
                autoFocus
                autoComplete='one-time-code'
                aria-label={t('Backup code')}
                value={code}
                onChange={(event) => setCode(event.target.value)}
                disabled={busy}
              />
            ) : (
              <InputOTP
                autoFocus
                maxLength={6}
                value={code}
                onChange={setCode}
                disabled={busy}
                aria-label={t('Verification code')}
              >
                <InputOTPGroup>
                  {[0, 1, 2, 3, 4, 5].map((i) => (
                    <InputOTPSlot key={i} index={i} />
                  ))}
                </InputOTPGroup>
              </InputOTP>
            )}
            <button
              type='button'
              className='text-muted-foreground hover:text-foreground text-xs underline underline-offset-4'
              onClick={() => {
                setUseBackup((v) => !v)
                setCode('')
                setError(null)
              }}
            >
              {useBackup
                ? t('Use authenticator code instead')
                : t('Use a backup code instead')}
            </button>
          </form>
        )}
        {error && (
          <p role='alert' className='text-destructive text-sm'>
            {error}
          </p>
        )}
      </div>
    </Dialog>
  )
}
