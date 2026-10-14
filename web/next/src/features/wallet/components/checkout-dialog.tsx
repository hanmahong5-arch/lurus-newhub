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
import { CheckCircle2, ExternalLink, Loader2, XCircle } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button, buttonVariants } from '@/components/ui/button'
import { currentUserQueryKey } from '@/lib/user'

import { fetchCheckoutStatus, walletKeys } from '../api'
import { msUntil, orderPhase } from '../lib/checkout'
import { formatCny } from '../lib/format'
import type { CheckoutOrder } from '../types'

const POLL_MS = 3000

/** Shows a payment QR code and watches the order until it settles. */
export function CheckoutDialog(props: {
  order: CheckoutOrder | null
  amount: number | null
  onClose: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { order } = props
  const orderNo = order?.order_no ?? ''
  const [expired, setExpired] = useState(false)

  const status = useQuery({
    queryKey: walletKeys.order(orderNo),
    queryFn: () => fetchCheckoutStatus(orderNo),
    enabled: orderNo !== '',
    retry: false,
    gcTime: 0,
    refetchInterval: (query) => {
      const phase = orderPhase(query.state.data?.status)
      return phase === 'pending' && !expired ? POLL_MS : false
    },
  })

  const phase = orderPhase(status.data?.status ?? order?.status)

  // Stop asking once the platform's own expiry has passed.
  const expiresAt = status.data?.expires_at ?? order?.expires_at ?? null
  useEffect(() => {
    setExpired(false)
    const wait = msUntil(expiresAt)
    if (wait === null) return
    if (wait <= 0) {
      setExpired(true)
      return
    }
    const timer = window.setTimeout(() => setExpired(true), wait)
    return () => window.clearTimeout(timer)
  }, [orderNo, expiresAt])

  useEffect(() => {
    if (phase !== 'paid') return
    void queryClient.invalidateQueries({ queryKey: ['wallet'] })
    void queryClient.invalidateQueries({ queryKey: currentUserQueryKey })
  }, [phase, queryClient])

  const failed = phase === 'failed' || (phase === 'pending' && expired)
  const payUrl = status.data?.pay_url || order?.pay_url || ''

  return (
    <Dialog
      open={order !== null}
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Complete your payment')}
      description={
        props.amount !== null
          ? t('Order {{order}} · {{amount}}', {
              order: orderNo,
              amount: formatCny(props.amount),
            })
          : orderNo
      }
      contentClassName='sm:max-w-md'
      contentHeight='auto'
      footer={
        <Button
          variant={phase === 'paid' ? 'default' : 'outline'}
          onClick={props.onClose}
        >
          {phase === 'paid' ? t('Done') : t('Close')}
        </Button>
      }
    >
      <div className='flex flex-col items-center gap-4 py-2 text-center'>
        {phase === 'paid' && (
          <div role='status' className='flex flex-col items-center gap-2'>
            <CheckCircle2 className='text-success size-10' />
            <p className='font-medium'>{t('Payment received')}</p>
            <p className='text-muted-foreground text-sm'>
              {t('Your wallet balance has been updated.')}
            </p>
          </div>
        )}

        {failed && (
          <div role='alert' className='flex flex-col items-center gap-2'>
            <XCircle className='text-destructive size-10' />
            <p className='font-medium'>
              {phase === 'failed'
                ? t('This order was not paid')
                : t('This order has expired')}
            </p>
            <p className='text-muted-foreground text-sm'>
              {t('Close this window and start a new top-up.')}
            </p>
          </div>
        )}

        {phase === 'pending' && !failed && (
          <>
            <p className='text-muted-foreground text-sm'>
              {t('Scan the code with your payment app to pay.')}
            </p>
            {payUrl && (
              <div className='rounded-lg bg-white p-4'>
                <QRCodeSVG value={payUrl} size={200} />
              </div>
            )}
            <div
              role='status'
              className='text-muted-foreground flex items-center gap-2 text-sm'
            >
              <Loader2 className='size-4 animate-spin' />
              {t('Waiting for payment...')}
            </div>
            {status.isError && (
              <p className='text-warning text-xs'>
                {t('Could not check the order just now. Still trying.')}
              </p>
            )}
            {payUrl && (
              <a
                href={payUrl}
                target='_blank'
                rel='noopener noreferrer'
                className={buttonVariants({ variant: 'outline', size: 'sm' })}
              >
                <ExternalLink className='size-4' />
                {t('Open payment page')}
              </a>
            )}
          </>
        )}
      </div>
    </Dialog>
  )
}
