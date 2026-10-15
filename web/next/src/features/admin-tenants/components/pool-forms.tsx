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
import { useRef, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { useQuota } from '@/hooks/use-quota'
import { ApiError, isApiError } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { currentUserQueryOptions } from '@/lib/user'

import { createCreditPool, topupCreditPool } from '../api'
import {
  POOL_DEFAULT_ALERT_PCT,
  POOL_DEFAULT_RESET_PERIOD,
  POOL_RESET_PERIODS,
} from '../constants'
import { resetPeriodLabel } from '../lib/labels'
import {
  parseAlertPct,
  parseCeiling,
  parseTopup,
  type CeilingParse,
  type TopupParse,
} from '../lib/pool'
import {
  failureIsDefinitive,
  intentFor,
  type TopupIntent,
} from '../lib/topup-intent'
import { useFormDraft } from '../lib/use-form-draft'
import type { CreditPool, TopupResult } from '../types'
import { Field, SwitchRow } from './ui-bits'

function useDraftOwner(): string | number {
  const { data } = useQuery(currentUserQueryOptions)
  return data?.id ?? 'anon'
}

function useCurrencyUnit(): string {
  const { t } = useTranslation()
  const { isTokens, symbol } = useQuota()
  return isTokens ? t('Tokens') : symbol
}

function DraftBanner(props: {
  testId: string
  discardTestId: string
  onDiscard: () => void
}) {
  const { t } = useTranslation()
  return (
    <div
      className='border-warning/50 bg-warning/10 text-muted-foreground flex items-center justify-between gap-2 rounded-md border-l-2 px-3 py-1.5 text-xs'
      data-testid={props.testId}
    >
      <span>{t('Restored from a saved draft.')}</span>
      <Button
        type='button'
        size='sm'
        variant='ghost'
        onClick={props.onDiscard}
        data-testid={props.discardTestId}
      >
        {t('Discard')}
      </Button>
    </div>
  )
}

// ─── Ceiling (create the pool) ────────────────────────────────────────────

interface CeilingDraft {
  unlimited: boolean
  amount: string
  resetPeriod: string
  alertPct: string
}

const CEILING_DEFAULTS: CeilingDraft = {
  unlimited: false,
  amount: '',
  resetPeriod: POOL_DEFAULT_RESET_PERIOD,
  alertPct: String(POOL_DEFAULT_ALERT_PCT),
}

export function CeilingForm(props: {
  tenantId: string
  onCreated: () => void
  /** The server says the pool already exists: show it. */
  onAlreadyExists: () => void
  onBusyChange: (busy: boolean) => void
}) {
  const { t } = useTranslation()
  const { fromDisplayAmount, ready } = useQuota()
  const unit = useCurrencyUnit()
  const owner = useDraftOwner()
  const { draft, setDraft, clear, restored } = useFormDraft<CeilingDraft>(
    `credit-pool-ceiling:${props.tenantId}`,
    CEILING_DEFAULTS,
    { userId: owner }
  )
  const [errors, setErrors] = useState<{ amount?: string; alertPct?: string }>(
    {}
  )

  const create = useMutation({
    mutationFn: (body: Parameters<typeof createCreditPool>[1]) =>
      createCreditPool(props.tenantId, body),
    onMutate: () => props.onBusyChange(true),
    onSettled: () => props.onBusyChange(false),
    onSuccess: () => {
      toast.success(t('Credit pool created'))
      clear()
      props.onCreated()
    },
    onError: (error) => {
      if (isApiError(error) && error.code === 'POOL_ALREADY_EXISTS') {
        toast.error(t('A credit pool already exists; use top-up instead.'))
        props.onAlreadyExists()
        return
      }
      handleServerError(error, t('Failed to create the credit pool'))
    },
  })

  const ceilingError = (parsed: CeilingParse): string | undefined => {
    if (parsed.ok) return undefined
    if (parsed.error === 'currency') {
      return t('The currency settings have not loaded yet.')
    }
    return t('Enter a valid amount')
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (create.isPending) return
    const ceiling = parseCeiling(
      { unlimited: draft.unlimited, amount: draft.amount },
      fromDisplayAmount
    )
    const pct = parseAlertPct(draft.alertPct)
    const next = {
      amount: ceilingError(ceiling),
      alertPct:
        pct === null ? t('Enter a whole number from 1 to 100.') : undefined,
    }
    setErrors(next)
    if (!ceiling.ok || pct === null) return
    create.mutate({
      max_balance: ceiling.value,
      reset_period: draft.resetPeriod,
      alert_threshold_pct: pct,
    })
  }

  return (
    <form
      onSubmit={submit}
      noValidate
      className='grid gap-4'
      data-testid='ceiling-form'
    >
      <div className='grid gap-1'>
        <h3 className='text-sm font-semibold'>{t('Set the ceiling')}</h3>
        <p className='text-muted-foreground text-xs'>
          {t('The most the pool can hold. A top-up cannot go past it.')}
        </p>
      </div>
      {restored && (
        <DraftBanner
          testId='ceiling-restored-banner'
          discardTestId='ceiling-discard-draft'
          onDiscard={clear}
        />
      )}
      <SwitchRow
        label={t('Unlimited ceiling')}
        description={t(
          'The relay does not check this pool’s balance at all.'
        )}
        checked={draft.unlimited}
        onChange={(unlimited) => setDraft((d) => ({ ...d, unlimited }))}
      />
      {!draft.unlimited && (
        <Field
          label={
            unit
              ? t('Ceiling ({{currency}})', { currency: unit })
              : t('Ceiling')
          }
          htmlFor='ceiling-max-balance'
          error={errors.amount}
        >
          <Input
            id='ceiling-max-balance'
            data-testid='ceiling-max-balance'
            inputMode='decimal'
            autoComplete='off'
            value={draft.amount}
            disabled={!ready}
            placeholder='100'
            onChange={(e) => {
              setDraft((d) => ({ ...d, amount: e.target.value }))
              setErrors((x) => ({ ...x, amount: undefined }))
            }}
          />
        </Field>
      )}
      <div className='grid gap-4 sm:grid-cols-2'>
        <Field label={t('Reset period')} htmlFor='ceiling-reset-period'>
          <NativeSelect
            id='ceiling-reset-period'
            data-testid='ceiling-reset-period'
            className='w-full'
            value={draft.resetPeriod}
            onChange={(e) =>
              setDraft((d) => ({ ...d, resetPeriod: e.target.value }))
            }
          >
            {POOL_RESET_PERIODS.map((period) => (
              <NativeSelectOption key={period} value={period}>
                {resetPeriodLabel(period, t)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </Field>
        <Field
          label={t('Alert threshold (%)')}
          htmlFor='ceiling-alert-pct'
          error={errors.alertPct}
        >
          <Input
            id='ceiling-alert-pct'
            data-testid='ceiling-alert-pct'
            inputMode='numeric'
            value={draft.alertPct}
            onChange={(e) => {
              setDraft((d) => ({ ...d, alertPct: e.target.value }))
              setErrors((x) => ({ ...x, alertPct: undefined }))
            }}
          />
        </Field>
      </div>
      <Button
        type='submit'
        disabled={create.isPending}
        data-testid='ceiling-submit'
      >
        {create.isPending ? t('Creating...') : t('Create pool')}
      </Button>
    </form>
  )
}

// ─── Top-up ───────────────────────────────────────────────────────────────

interface TopupDraft {
  amount: string
  reason: string
}

const TOPUP_DEFAULTS: TopupDraft = { amount: '', reason: '' }

export function TopupForm(props: {
  tenantId: string
  pool: CreditPool | null
  onToppedUp: (result: TopupResult) => void
  onBusyChange: (busy: boolean) => void
}) {
  const { t } = useTranslation()
  const { fromDisplayAmount, formatQuota, ready } = useQuota()
  const unit = useCurrencyUnit()
  const owner = useDraftOwner()
  const { draft, setDraft, clear, restored } = useFormDraft<TopupDraft>(
    `credit-pool-topup:${props.tenantId}`,
    TOPUP_DEFAULTS,
    { userId: owner }
  )
  const [amountError, setAmountError] = useState<string | undefined>()
  // The Idempotency-Key of the attempt in flight or last left unresolved.
  const intent = useRef<TopupIntent | null>(null)
  const disabled = props.pool === null

  const amountMessage = (parsed: TopupParse): string | undefined => {
    if (parsed.ok) return undefined
    switch (parsed.error) {
      case 'currency':
        return t('The currency settings have not loaded yet.')
      case 'not-positive':
        return t('The amount must be greater than zero.')
      case 'not-integer':
        return t('The amount is too small to be a whole number of units.')
      default:
        return t('Enter a valid amount')
    }
  }

  const topup = useMutation({
    mutationFn: (input: { amount: number; reason: string }) => {
      const current = intentFor(intent.current, input.amount, input.reason)
      intent.current = current
      return topupCreditPool(
        props.tenantId,
        {
          amount: input.amount,
          reason: input.reason === '' ? undefined : input.reason,
        },
        current.key
      )
    },
    onMutate: () => props.onBusyChange(true),
    onSettled: () => props.onBusyChange(false),
    onSuccess: (result) => {
      intent.current = null
      toast.success(
        result.replayed
          ? t('This top-up was already applied. Balance: {{balance}}', {
              balance: formatQuota(result.new_balance),
            })
          : t('Top-up successful. New balance: {{balance}}', {
              balance: formatQuota(result.new_balance),
            })
      )
      clear()
      props.onToppedUp(result)
    },
    onError: (error) => {
      // A refusal spends the key; an unanswered request keeps it so a retry
      // is settled once by the server instead of debiting the wallet again.
      if (failureIsDefinitive(error)) intent.current = null
      const status = error instanceof ApiError ? error.status : undefined
      const code = error instanceof ApiError ? error.code : undefined
      if (status === 402 || code === 'WALLET_DEBIT_FAILED') {
        toast.error(t('Wallet debit failed — insufficient platform balance'))
      } else if (code === 'POOL_CEILING_EXCEEDED') {
        toast.error(t('Top-up would exceed the pool ceiling'))
      } else {
        handleServerError(error, t('Top-up failed'))
      }
    },
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (topup.isPending || disabled) return
    const parsed = parseTopup(draft.amount, fromDisplayAmount)
    setAmountError(amountMessage(parsed))
    if (!parsed.ok) return
    topup.mutate({ amount: parsed.amount, reason: draft.reason.trim() })
  }

  return (
    <form
      onSubmit={submit}
      noValidate
      className='grid gap-4'
      data-testid='topup-form'
    >
      <div className='grid gap-1'>
        <h3 className='text-sm font-semibold'>{t('Top up from wallet')}</h3>
        <p className='text-muted-foreground text-xs'>
          {disabled
            ? t('Create the pool first; a top-up needs a pool to land in.')
            : t('The amount is debited from your platform wallet and added to the pool.')}
        </p>
      </div>
      {restored && (
        <DraftBanner
          testId='topup-restored-banner'
          discardTestId='topup-discard-draft'
          onDiscard={clear}
        />
      )}
      <Field
        label={
          unit ? t('Amount ({{currency}})', { currency: unit }) : t('Amount')
        }
        htmlFor='topup-amount'
        error={amountError}
      >
        <Input
          id='topup-amount'
          data-testid='topup-amount'
          inputMode='decimal'
          autoComplete='off'
          value={draft.amount}
          disabled={disabled || !ready}
          onChange={(e) => {
            setDraft((d) => ({ ...d, amount: e.target.value }))
            setAmountError(undefined)
          }}
        />
      </Field>
      <Field label={t('Reason (optional)')} htmlFor='topup-reason'>
        <Input
          id='topup-reason'
          data-testid='topup-reason'
          value={draft.reason}
          disabled={disabled}
          onChange={(e) => setDraft((d) => ({ ...d, reason: e.target.value }))}
        />
      </Field>
      <Button
        type='submit'
        disabled={topup.isPending || disabled}
        data-testid='topup-submit'
      >
        {topup.isPending ? t('Topping up...') : t('Top up')}
      </Button>
    </form>
  )
}
