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
import { useMutation, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useQuota } from '@/hooks/use-quota'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'

import { getUser, updateUser, usersQueryKey } from '../api'
import { applyQuotaAdjust, parseAmount, type QuotaResult } from '../lib/quota'
import type { AdminUser, QuotaAdjustMode } from '../types'
import { useUsers } from './users-provider'

interface UserQuotaDialogProps {
  user: AdminUser | null
}

const MODES: QuotaAdjustMode[] = ['add', 'subtract', 'override']

export function UserQuotaDialog({ user }: UserQuotaDialogProps) {
  const { t } = useTranslation()
  const { setQuotaUser, refresh } = useUsers()
  const { formatQuota, fromDisplayAmount, ready, symbol, isTokens } =
    useQuota()
  const [mode, setMode] = useState<QuotaAdjustMode>('add')
  const [amount, setAmount] = useState('')
  const open = user !== null
  const unit = isTokens ? t('Tokens') : symbol

  // The balance moves with every request the user makes: preview against the
  // live row, never the one the table loaded earlier.
  const live = useQuery({
    queryKey: [...usersQueryKey, 'quota', user?.id],
    queryFn: () => getUser((user as AdminUser).id),
    enabled: open,
    staleTime: 0,
    gcTime: 0,
  })
  const current = live.data?.quota

  const typed = parseAmount(amount)
  const amountQuota = typed === null ? null : fromDisplayAmount(typed)
  let result: QuotaResult | null = null
  if (current !== undefined && amountQuota !== null) {
    result = applyQuotaAdjust(current, mode, amountQuota)
  }

  const reset = () => {
    setAmount('')
    setMode('add')
  }
  const close = () => {
    reset()
    setQuotaUser(null)
  }

  const adjust = useMutation({
    mutationFn: () =>
      updateUser((user as AdminUser).id, (row) => {
        const next = applyQuotaAdjust(row.quota, mode, amountQuota ?? Number.NaN)
        if (!next.ok) throw new Error(reasonMessage(next.reason))
        return { quota: next.quota }
      }),
    onSuccess: () => {
      toast.success(t('Quota adjusted successfully'))
      close()
      refresh()
    },
    onError: (error) => handleServerError(error, t('Failed to adjust quota')),
  })

  function reasonMessage(reason: 'invalid' | 'negative' | 'too-large') {
    if (reason === 'negative') return t('The balance cannot go below zero')
    if (reason === 'too-large') return t('The balance is above the maximum')
    return t('Enter a valid amount')
  }

  const preview = (() => {
    if (current === undefined) return live.isError ? t('Failed to load') : ''
    const before = `${t('Current quota')}: ${formatQuota(current)}`
    if (result?.ok) return `${before} → ${formatQuota(result.quota)}`
    return before
  })()

  const canSubmit = result?.ok === true && !adjust.isPending
  const hint = result && !result.ok ? reasonMessage(result.reason) : null

  const modeLabels: Record<QuotaAdjustMode, string> = {
    add: t('Add'),
    subtract: t('Subtract'),
    override: t('Override'),
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) close()
      }}
      title={t('Adjust Quota')}
      description={
        user
          ? t('Change the balance of {{username}}', { username: user.username })
          : undefined
      }
      contentHeight='auto'
      contentClassName='sm:max-w-md'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button variant='outline' onClick={close}>
            {t('Cancel')}
          </Button>
          <Button onClick={() => adjust.mutate()} disabled={!canSubmit}>
            {adjust.isPending ? t('Processing...') : t('Confirm')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <p
          className='text-muted-foreground text-sm tabular-nums'
          aria-live='polite'
        >
          {preview}
        </p>

        <div className='space-y-2'>
          <Label>{t('Mode')}</Label>
          <div className='flex gap-1' role='group' aria-label={t('Mode')}>
            {MODES.map((m) => (
              <Button
                key={m}
                type='button'
                variant='outline'
                size='sm'
                aria-pressed={mode === m}
                className={cn(
                  mode === m &&
                    'bg-primary text-primary-foreground hover:bg-primary/90 hover:text-primary-foreground'
                )}
                onClick={() => {
                  setMode(m)
                  setAmount('')
                }}
              >
                {modeLabels[m]}
              </Button>
            ))}
          </div>
        </div>

        <div className='space-y-2'>
          <Label htmlFor='user-quota-amount'>
            {unit ? `${t('Amount')} (${unit})` : t('Amount')}
          </Label>
          <Input
            id='user-quota-amount'
            inputMode='decimal'
            autoComplete='off'
            placeholder={t('Enter amount')}
            value={amount}
            disabled={!ready}
            aria-invalid={hint !== null || undefined}
            onChange={(e) => setAmount(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && canSubmit) adjust.mutate()
            }}
          />
          {hint && (
            <p role='alert' className='text-destructive text-xs'>
              {hint}
            </p>
          )}
          {!ready && (
            <p className='text-muted-foreground text-xs'>
              {t('The currency settings have not loaded yet.')}
            </p>
          )}
        </div>
      </div>
    </Dialog>
  )
}
