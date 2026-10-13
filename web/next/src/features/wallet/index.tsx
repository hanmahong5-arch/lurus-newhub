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
import { ExternalLink } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/section-page-layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button, buttonVariants } from '@/components/ui/button'
import { toApiError } from '@/lib/api'
import { currentUserQueryOptions } from '@/lib/user'

import { fetchWalletInfo, walletKeys } from './api'
import { CheckoutDialog } from './components/checkout-dialog'
import { HistoryCard } from './components/history-card'
import { RedeemCard } from './components/redeem-card'
import { TopUpCard } from './components/top-up-card'
import { WalletStats } from './components/wallet-stats'
import type { CheckoutOrder } from './types'

export function WalletPage() {
  const { t } = useTranslation()
  const [order, setOrder] = useState<{
    order: CheckoutOrder
    amount: number
  } | null>(null)

  const wallet = useQuery({
    queryKey: walletKeys.info,
    queryFn: fetchWalletInfo,
    retry: false,
  })
  const user = useQuery(currentUserQueryOptions)

  const walletError = wallet.isError ? toApiError(wallet.error).message : null
  const topupUrl = wallet.data?.topup_url

  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Wallet')}</SectionPageLayout.Title>
        {topupUrl && (
          <SectionPageLayout.Actions>
            <a
              href={topupUrl}
              target='_blank'
              rel='noopener noreferrer'
              className={buttonVariants({ variant: 'outline', size: 'sm' })}
            >
              <ExternalLink className='size-4' />
              {t('Open platform wallet')}
            </a>
          </SectionPageLayout.Actions>
        )}
        <SectionPageLayout.Content>
          <div className='mx-auto flex w-full max-w-7xl flex-col gap-4 sm:gap-5'>
            {walletError && (
              <Alert variant='destructive' role='alert'>
                <AlertDescription className='flex flex-wrap items-center gap-3'>
                  <span>
                    {t('Wallet unavailable')}: {walletError}
                  </span>
                  <Button
                    size='sm'
                    variant='outline'
                    onClick={() => void wallet.refetch()}
                  >
                    {t('Retry')}
                  </Button>
                </AlertDescription>
              </Alert>
            )}

            {wallet.data?.source === 'internal' && (
              <Alert>
                <AlertDescription>
                  {t(
                    'This sign-in is not linked to a platform wallet, so the balance below is your console quota. Online top-up needs a linked platform account; redemption codes work either way.'
                  )}
                </AlertDescription>
              </Alert>
            )}

            <WalletStats
              wallet={wallet.data}
              walletLoading={wallet.isPending}
              walletError={walletError}
              user={user.data}
              userLoading={user.isPending}
            />

            <div className='grid gap-4 xl:grid-cols-[minmax(0,1.05fr)_minmax(360px,0.95fr)] xl:items-start'>
              <TopUpCard
                wallet={wallet.data}
                onQrOrder={(next, amount) => setOrder({ order: next, amount })}
              />
              <RedeemCard />
            </div>

            <HistoryCard wallet={wallet.data} />
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      <CheckoutDialog
        order={order?.order ?? null}
        amount={order?.amount ?? null}
        onClose={() => setOrder(null)}
      />
    </>
  )
}
