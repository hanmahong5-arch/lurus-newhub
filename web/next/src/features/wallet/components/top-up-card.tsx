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
import { Loader2, QrCode, Receipt, WalletCards } from 'lucide-react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { TitledCard } from '@/components/ui/titled-card'
import { cn } from '@/lib/utils'

import { createCheckout, fetchPaymentMethods, walletKeys } from '../api'
import {
  IntentKey,
  PRESET_AMOUNTS_CNY,
  amountError,
  checkoutErrorMessage,
  isQrMethod,
  parseAmount,
} from '../lib/checkout'
import { formatCny } from '../lib/format'
import { goToPayment } from '../lib/navigate'
import type { CheckoutOrder, PaymentMethod, WalletInfo } from '../types'

export function TopUpCard(props: {
  wallet: WalletInfo | undefined
  /** An order that needs a scan; the page shows it in a dialog. */
  onQrOrder: (order: CheckoutOrder, amount: number) => void
}) {
  const { t } = useTranslation()
  const linked = props.wallet?.source === 'platform'
  const [amountText, setAmountText] = useState('')
  const [methodId, setMethodId] = useState<string | null>(null)
  const intent = useRef(new IntentKey())

  const methods = useQuery({
    queryKey: walletKeys.methods,
    queryFn: fetchPaymentMethods,
    enabled: linked,
    retry: false,
  })

  const list = methods.data ?? []
  const selected: PaymentMethod | undefined =
    list.find((m) => m.id === methodId) ?? list[0]
  const amount = parseAmount(amountText)
  const invalid = amountText.trim() !== '' ? amountError(amount, t) : null

  const pay = useMutation({
    mutationFn: async (input: { amount: number; method: PaymentMethod }) => {
      const key = intent.current.keyFor(input.amount, input.method.id)
      const order = await createCheckout(input.amount, input.method.id, key)
      if (!order.pay_url) {
        throw new Error(t('The payment service did not return a payment link.'))
      }
      return { order, ...input }
    },
    onSuccess: ({ order, amount: paid, method }) => {
      // The order exists now; a deliberate second top-up gets a fresh key.
      intent.current.reset()
      if (isQrMethod(method.type)) props.onQrOrder(order, paid)
      else goToPayment(order.pay_url)
    },
    onError: (error) => toast.error(checkoutErrorMessage(error, t)),
  })

  const canPay =
    linked &&
    selected !== undefined &&
    amount !== null &&
    amountError(amount, t) === null &&
    !pay.isPending

  let payLabel = t('Pay')
  if (pay.isPending) payLabel = t('Creating order...')
  else if (amount !== null && amountError(amount, t) === null) {
    payLabel = t('Pay {{amount}}', { amount: formatCny(amount) })
  }

  let methodsBlock: React.ReactNode
  if (!linked) {
    methodsBlock = (
      <Alert>
        <AlertDescription>
          {t(
            'Online top-up needs a platform-linked account. Your current sign-in is not linked to one; you can still redeem a code.'
          )}
        </AlertDescription>
      </Alert>
    )
  } else if (methods.isPending) {
    methodsBlock = <Skeleton className='h-14 w-full' />
  } else if (methods.isError) {
    methodsBlock = (
      <div className='space-y-2'>
        <p role='alert' className='text-destructive text-sm'>
          {t('Payment methods could not be loaded.')}
        </p>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => void methods.refetch()}
        >
          {t('Retry')}
        </Button>
      </div>
    )
  } else if (list.length === 0) {
    methodsBlock = (
      <Alert>
        <AlertDescription>
          {t('No payment method is configured yet. Top-up is unavailable.')}
        </AlertDescription>
      </Alert>
    )
  } else {
    methodsBlock = (
      <div
        role='radiogroup'
        aria-label={t('Payment Method')}
        className='grid grid-cols-2 gap-1.5 sm:gap-3 lg:grid-cols-3'
      >
        {list.map((method) => {
          const active = selected?.id === method.id
          return (
            <Button
              key={method.id}
              type='button'
              role='radio'
              aria-checked={active}
              variant='outline'
              onClick={() => setMethodId(method.id)}
              className={cn(
                'min-h-14 min-w-0 justify-start gap-2 rounded-lg px-3 py-2 text-left',
                active
                  ? 'border-foreground bg-foreground/5 dark:border-foreground dark:bg-foreground/10'
                  : 'border-muted'
              )}
            >
              {isQrMethod(method.type) ? (
                <QrCode className='size-4' />
              ) : (
                <Receipt className='size-4' />
              )}
              <span className='flex min-w-0 flex-col items-start gap-0.5'>
                <span className='max-w-full truncate'>{method.name}</span>
                <span className='text-muted-foreground max-w-full truncate text-[11px] leading-4 font-normal'>
                  {isQrMethod(method.type) ? t('Scan to pay') : t('Pay online')}
                </span>
              </span>
            </Button>
          )
        })}
      </div>
    )
  }

  return (
    <TitledCard
      title={t('Add Funds')}
      description={t('Choose an amount and payment method')}
      icon={<WalletCards className='size-4' />}
      iconTone='success'
      disableHoverEffect
      contentClassName='space-y-4 sm:space-y-6'
    >
      <form
        className='space-y-4 sm:space-y-6'
        onSubmit={(event) => {
          event.preventDefault()
          if (canPay && amount !== null && selected) {
            pay.mutate({ amount, method: selected })
          }
        }}
      >
        <div className='space-y-2.5 sm:space-y-3'>
          <Label className='text-muted-foreground text-xs font-medium tracking-wider uppercase'>
            {t('Amount (CNY)')}
          </Label>
          <div className='grid grid-cols-3 gap-1.5 sm:gap-3 md:grid-cols-6'>
            {PRESET_AMOUNTS_CNY.map((preset) => (
              <Button
                key={preset}
                type='button'
                variant='outline'
                aria-pressed={amount === preset}
                onClick={() => setAmountText(String(preset))}
                className={cn(
                  'h-10 rounded-lg font-semibold',
                  amount === preset
                    ? 'border-foreground bg-foreground/5 dark:border-foreground dark:bg-foreground/10'
                    : 'border-muted'
                )}
              >
                {formatCny(preset).replace(/\.00$/, '')}
              </Button>
            ))}
          </div>
        </div>

        <div className='space-y-2.5 sm:space-y-3'>
          <Label
            htmlFor='topup-amount'
            className='text-muted-foreground text-xs font-medium tracking-wider uppercase'
          >
            {t('Custom Amount')}
          </Label>
          <Input
            id='topup-amount'
            type='number'
            inputMode='decimal'
            min='0'
            step='0.01'
            value={amountText}
            onChange={(e) => setAmountText(e.target.value)}
            placeholder={t('Enter an amount in CNY')}
            aria-invalid={invalid ? true : undefined}
            className='h-9 text-base sm:h-10 sm:text-lg'
          />
          {invalid && (
            <p role='alert' className='text-destructive text-xs'>
              {invalid}
            </p>
          )}
        </div>

        <div className='space-y-2.5 sm:space-y-3'>
          <Label className='text-muted-foreground text-xs font-medium tracking-wider uppercase'>
            {t('Payment Method')}
          </Label>
          {methodsBlock}
        </div>

        <Button type='submit' disabled={!canPay} className='w-full sm:w-auto'>
          {pay.isPending && <Loader2 className='size-4 animate-spin' />}
          {payLabel}
        </Button>
      </form>
    </TitledCard>
  )
}
