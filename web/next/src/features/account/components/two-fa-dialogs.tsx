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
import { AlertTriangle, Loader2 } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { StepUpCancelledError } from '@/components/step-up'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { toApiError } from '@/lib/api'

import {
  confirmTotp,
  disableTotp,
  enrollTotp,
  regenerateBackupCodes,
} from '../api'

/** The gate wrapper from useStepUp(): runs `action`, prompting when asked. */
export type RunWithStepUp = <T>(action: () => Promise<T>) => Promise<T>

function reportFailure(error: unknown, fallback: string) {
  if (error instanceof StepUpCancelledError) return
  toast.error(toApiError(error).message || fallback)
}

export function BackupCodesList(props: { codes: string[] }) {
  const { t } = useTranslation()
  return (
    <div className='space-y-3'>
      <div className='grid grid-cols-2 gap-2 rounded-lg border p-4'>
        {props.codes.map((code) => (
          <div
            key={code}
            className='bg-muted rounded-md p-2 text-center font-mono text-sm'
          >
            {code}
          </div>
        ))}
      </div>
      <CopyButton
        value={props.codes.join('\n')}
        variant='outline'
        size='default'
        className='w-full'
        iconClassName='mr-2 size-4'
        tooltip={t('Copy all backup codes')}
        aria-label={t('Copy all backup codes')}
      >
        {t('Copy All Codes')}
      </CopyButton>
    </div>
  )
}

type SetupStep = 'scan' | 'verify' | 'codes'

interface SetupDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Two-factor became active (status should be re-read). */
  onEnabled: () => void
}

/** Enrol an authenticator app: scan, prove one code, then keep the backup codes. */
export function TwoFaSetupDialog(props: SetupDialogProps) {
  const { t } = useTranslation()
  const { open, onEnabled } = props
  const [step, setStep] = useState<SetupStep>('scan')
  const [code, setCode] = useState('')
  const [codeError, setCodeError] = useState<string | null>(null)
  const [backupCodes, setBackupCodes] = useState<string[]>([])

  const enroll = useMutation({ mutationFn: enrollTotp })
  const startEnroll = enroll.mutate
  const confirm = useMutation({
    mutationFn: (value: string) => confirmTotp(value),
    onSuccess: (data) => {
      setBackupCodes(data.backup_codes)
      setStep('codes')
      onEnabled()
      toast.success(t('Two-factor authentication enabled successfully!'))
    },
    onError: (error) => setCodeError(toApiError(error).message),
  })

  useEffect(() => {
    if (!open) return
    setStep('scan')
    setCode('')
    setCodeError(null)
    setBackupCodes([])
    startEnroll()
  }, [open, startEnroll])

  const stepLabels: Record<SetupStep, string> = {
    scan: t('Scan QR Code'),
    verify: t('Verify Setup'),
    codes: t('Save Backup Codes'),
  }
  const stepNumber = { scan: 1, verify: 2, codes: 3 }[step]
  const setup = enroll.data

  const handleOpenChange = (next: boolean) => {
    // The backup codes are shown once; only "Done" may close that step.
    if (!next && step === 'codes') return
    props.onOpenChange(next)
  }

  const submitCode = () => {
    const value = code.trim()
    if (value === '' || confirm.isPending) return
    setCodeError(null)
    confirm.mutate(value)
  }

  let footer: React.ReactNode
  if (step === 'scan') {
    footer = (
      <>
        <Button variant='outline' onClick={() => props.onOpenChange(false)}>
          {t('Cancel')}
        </Button>
        <Button onClick={() => setStep('verify')} disabled={!setup}>
          {t('Next')}
        </Button>
      </>
    )
  } else if (step === 'verify') {
    footer = (
      <>
        <Button
          variant='outline'
          onClick={() => setStep('scan')}
          disabled={confirm.isPending}
        >
          {t('Back')}
        </Button>
        <Button
          onClick={submitCode}
          disabled={confirm.isPending || !code.trim()}
        >
          {confirm.isPending && <Loader2 className='size-4 animate-spin' />}
          {confirm.isPending ? t('Enabling...') : t('Enable 2FA')}
        </Button>
      </>
    )
  } else {
    footer = (
      <Button onClick={() => props.onOpenChange(false)}>{t('Done')}</Button>
    )
  }

  return (
    <Dialog
      open={open}
      onOpenChange={handleOpenChange}
      title={t('Setup Two-Factor Authentication')}
      description={t('Step {{current}} of {{total}}: {{label}}', {
        current: stepNumber,
        total: 3,
        label: stepLabels[step],
      })}
      contentClassName='sm:max-w-lg'
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={footer}
    >
      <div className='space-y-4 py-2'>
        {enroll.isPending && step !== 'codes' && (
          <div
            role='status'
            className='flex flex-col items-center justify-center gap-3 py-8'
          >
            <Loader2 className='text-muted-foreground size-6 animate-spin' />
            <div className='text-muted-foreground text-sm'>
              {t('Setting up 2FA...')}
            </div>
          </div>
        )}

        {enroll.isError && (
          <div className='space-y-3'>
            <p role='alert' className='text-destructive text-sm'>
              {toApiError(enroll.error).message ||
                t('Failed to load setup data')}
            </p>
            <Button variant='outline' onClick={() => startEnroll()}>
              {t('Retry')}
            </Button>
          </div>
        )}

        {step === 'scan' && setup && (
          <div className='space-y-4'>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Scan this QR code with your authenticator app (Google Authenticator, Microsoft Authenticator, etc.)'
              )}
            </p>
            <div className='flex justify-center rounded-lg bg-white p-4'>
              <QRCodeSVG value={setup.otpauth_url} size={200} />
            </div>
            <div className='bg-muted flex items-center justify-between gap-2 rounded-lg p-3'>
              <div className='min-w-0'>
                <p className='text-muted-foreground text-xs'>
                  {t('Or enter this key manually:')}
                </p>
                <code className='font-mono text-sm break-all'>
                  {setup.secret}
                </code>
              </div>
              <CopyButton
                value={setup.secret}
                variant='ghost'
                tooltip={t('Copy secret key')}
                aria-label={t('Copy secret key')}
              />
            </div>
          </div>
        )}

        {step === 'verify' && (
          <form
            className='space-y-2'
            onSubmit={(event) => {
              event.preventDefault()
              submitCode()
            }}
          >
            <Label htmlFor='totp-setup-code'>{t('Verification Code')}</Label>
            <Input
              id='totp-setup-code'
              value={code}
              onChange={(e) => setCode(e.target.value)}
              placeholder={t('Enter 6-digit code')}
              inputMode='numeric'
              autoComplete='one-time-code'
              maxLength={6}
              disabled={confirm.isPending}
              autoFocus
              aria-invalid={codeError ? true : undefined}
            />
            {codeError ? (
              <p role='alert' className='text-destructive text-xs'>
                {codeError}
              </p>
            ) : (
              <p className='text-muted-foreground text-xs'>
                {t('Enter the 6-digit code from your authenticator app')}
              </p>
            )}
          </form>
        )}

        {step === 'codes' && (
          <div className='space-y-4'>
            <Alert>
              <AlertDescription>
                {t(
                  'Save these backup codes in a safe place. Each code can only be used once, and they are not shown again.'
                )}
              </AlertDescription>
            </Alert>
            <BackupCodesList codes={backupCodes} />
          </div>
        )}
      </div>
    </Dialog>
  )
}

interface BackupDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  run: RunWithStepUp
  onDone: () => void
}

/** Replace every backup code (needs a fresh step-up). */
export function TwoFaBackupDialog(props: BackupDialogProps) {
  const { t } = useTranslation()
  const { open, run } = props
  const [codes, setCodes] = useState<string[]>([])

  useEffect(() => {
    if (open) setCodes([])
  }, [open])

  const regenerate = useMutation({
    mutationFn: () => run(regenerateBackupCodes),
    onSuccess: (data) => {
      setCodes(data.backup_codes)
      toast.success(t('Backup codes regenerated successfully'))
    },
    onError: (error) =>
      reportFailure(error, t('Failed to regenerate backup codes')),
  })

  const showCodes = codes.length > 0
  const close = () => {
    props.onOpenChange(false)
    if (showCodes) props.onDone()
  }

  return (
    <>
      <ConfirmDialog
        open={open && !showCodes && !regenerate.isPending}
        onOpenChange={(next) => {
          if (!next) props.onOpenChange(false)
        }}
        title={t('Regenerate Backup Codes')}
        desc={t(
          'Generating new codes will invalidate all existing backup codes.'
        )}
        confirmText={t('Generate New Codes')}
        isLoading={regenerate.isPending}
        handleConfirm={() => regenerate.mutate()}
      />
      <Dialog
        open={open && showCodes}
        onOpenChange={(next) => {
          if (!next) close()
        }}
        title={t('Regenerate Backup Codes')}
        description={t('Your new backup codes are ready')}
        contentClassName='sm:max-w-md'
        contentHeight='auto'
        bodyClassName='space-y-4'
        footer={<Button onClick={close}>{t('Done')}</Button>}
      >
        <Alert>
          <AlertDescription>
            {t(
              'Save these codes in a safe place. Each code can only be used once.'
            )}
          </AlertDescription>
        </Alert>
        <BackupCodesList codes={codes} />
      </Dialog>
    </>
  )
}

interface DisableDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  run: RunWithStepUp
  onDone: () => void
}

/** Turn two-factor off (needs a fresh step-up and an explicit acknowledgement). */
export function TwoFaDisableDialog(props: DisableDialogProps) {
  const { t } = useTranslation()
  const { open, run } = props
  const confirmId = useId()
  const [confirmed, setConfirmed] = useState(false)

  useEffect(() => {
    if (open) setConfirmed(false)
  }, [open])

  const disable = useMutation({
    mutationFn: () => run(disableTotp),
    onSuccess: () => {
      toast.success(t('Two-factor authentication disabled'))
      props.onOpenChange(false)
      props.onDone()
    },
    onError: (error) =>
      reportFailure(error, t('Failed to disable two-factor authentication')),
  })

  return (
    <ConfirmDialog
      // Hidden while the step-up prompt owns the screen.
      open={open && !disable.isPending}
      onOpenChange={props.onOpenChange}
      title={t('Disable Two-Factor Authentication')}
      desc={t(
        'This action will permanently remove 2FA protection from your account.'
      )}
      confirmText={t('Disable 2FA')}
      destructive
      disabled={!confirmed}
      isLoading={disable.isPending}
      handleConfirm={() => disable.mutate()}
    >
      <Alert variant='destructive'>
        <AlertTriangle className='size-4' />
        <AlertDescription>
          {t('Warning: Disabling 2FA will make your account less secure.')}
        </AlertDescription>
      </Alert>
      <div className='flex items-start gap-2'>
        <Checkbox
          id={confirmId}
          checked={confirmed}
          disabled={disable.isPending}
          onCheckedChange={(checked) => setConfirmed(checked === true)}
        />
        <Label
          htmlFor={confirmId}
          className='text-sm leading-tight font-normal'
        >
          {t(
            'I understand that disabling 2FA removes its authenticator and backup codes.'
          )}
        </Label>
      </div>
    </ConfirmDialog>
  )
}
