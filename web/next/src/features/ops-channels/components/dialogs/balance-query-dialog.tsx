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
import { useQueryClient } from '@tanstack/react-query'
import { DollarSign, Loader2, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { formatTimestampToDate } from '@/components/format'
import { Button } from '@/components/ui/button'
import { IconBadge } from '@/components/ui/icon-badge'
import { handleServerError } from '@/lib/handle-server-error'

import { updateChannelBalance } from '../../api'
import { channelsQueryKeys, formatUsdBalance } from '../../lib'
import { useChannels } from '../channels-provider'

type BalanceQueryDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function BalanceQueryDialog(props: BalanceQueryDialogProps) {
  const { t } = useTranslation()
  const { currentRow, setCurrentRow } = useChannels()
  const queryClient = useQueryClient()
  const [isQuerying, setIsQuerying] = useState(false)
  const [balance, setBalance] = useState<number | null>(null)
  const [balanceUpdatedTime, setBalanceUpdatedTime] = useState<number | null>(
    null
  )

  if (!currentRow) return null

  const handleQueryBalance = async () => {
    setIsQuerying(true)
    try {
      const response = await updateChannelBalance(currentRow.id)
      if (response.success && response.balance !== undefined) {
        const newBalance = response.balance
        const now = Math.floor(Date.now() / 1000)
        setBalance(newBalance)
        setBalanceUpdatedTime(now)
        toast.success(t('Balance updated successfully'))
        setCurrentRow({
          ...currentRow,
          balance: newBalance,
          balance_updated_time: now,
        })
        await queryClient.invalidateQueries({
          queryKey: channelsQueryKeys.lists(),
        })
      } else {
        handleServerError(response, t('Failed to query balance'))
      }
    } catch (error: unknown) {
      handleServerError(error, t('Failed to query balance'))
    } finally {
      setIsQuerying(false)
    }
  }

  const handleClose = () => {
    setBalance(null)
    setBalanceUpdatedTime(null)
    props.onOpenChange(false)
  }

  const updatedAt = balanceUpdatedTime ?? currentRow.balance_updated_time

  return (
    <Dialog
      open={props.open}
      onOpenChange={handleClose}
      title={t('Query Balance')}
      description={
        <>
          {t('Update balance for:')}
          <strong>{currentRow.name}</strong>
        </>
      }
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <Button variant='outline' onClick={handleClose} disabled={isQuerying}>
          {t('Close')}
        </Button>
      }
    >
      <div className='space-y-4 py-4'>
        <div className='bg-muted/50 rounded-lg border p-4'>
          <div className='text-muted-foreground mb-2 flex items-center gap-2 text-sm'>
            <IconBadge tone='success' size='xs'>
              <DollarSign />
            </IconBadge>
            <span>{t('Current Balance')}</span>
          </div>
          <div className='text-2xl font-bold' data-testid='channel-balance'>
            {formatUsdBalance(balance ?? currentRow.balance)}
          </div>
          <div className='text-muted-foreground mt-2 text-xs'>
            {t('Last updated:')}{' '}
            {updatedAt ? formatTimestampToDate(updatedAt) : t('Never')}
          </div>
        </div>
        {currentRow.channel_info?.is_multi_key && (
          <p className='text-muted-foreground text-sm'>
            {t('Multi-key channels do not support balance queries.')}
          </p>
        )}
        <Button
          className='w-full'
          onClick={handleQueryBalance}
          disabled={isQuerying}
        >
          {isQuerying ? (
            <Loader2 className='mr-2 h-4 w-4 animate-spin' />
          ) : (
            <RefreshCw className='mr-2 h-4 w-4' />
          )}
          {isQuerying ? t('Querying...') : t('Update Balance')}
        </Button>
      </div>
    </Dialog>
  )
}
