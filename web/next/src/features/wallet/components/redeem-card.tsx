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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Gift, Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { TitledCard } from '@/components/ui/titled-card'
import { useQuota } from '@/hooks/use-quota'
import { currentUserQueryKey } from '@/lib/user'

import { redeemCode } from '../../org-billing/api'
import {
  REDEEM_CODE_LENGTH,
  redeemErrorMessage,
} from '../../org-billing/lib/redeem'

/** Redemption credits quota to the signed-in user (POST /api/v2/~/redeem). */
export function RedeemCard() {
  const { t } = useTranslation()
  const { formatQuota } = useQuota()
  const queryClient = useQueryClient()
  const [code, setCode] = useState('')
  const [added, setAdded] = useState<number | null>(null)

  const redeem = useMutation({
    mutationFn: (key: string) => redeemCode(key),
    onSuccess: async (res) => {
      setAdded(res.quota_added)
      setCode('')
      // Re-read the server's numbers instead of doing local arithmetic.
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: currentUserQueryKey }),
        queryClient.invalidateQueries({ queryKey: ['wallet'] }),
      ])
    },
    onError: (error) => {
      setAdded(null)
      toast.error(redeemErrorMessage(error, t))
    },
  })

  const trimmed = code.trim()
  const ready = trimmed.length === REDEEM_CODE_LENGTH && !redeem.isPending

  return (
    <TitledCard
      title={t('Redeem a code')}
      description={t('Enter a {{n}}-character redemption code to add quota.', {
        n: REDEEM_CODE_LENGTH,
      })}
      icon={<Gift className='size-4' />}
      iconTone='warning'
      disableHoverEffect
    >
      <form
        className='space-y-3'
        onSubmit={(event) => {
          event.preventDefault()
          if (ready) redeem.mutate(trimmed)
        }}
      >
        <Label
          htmlFor='redemption-code'
          className='text-muted-foreground text-xs font-medium tracking-wider uppercase'
        >
          {t('Redemption code')}
        </Label>
        <div className='grid grid-cols-[minmax(0,1fr)_auto] gap-2'>
          <Input
            id='redemption-code'
            value={code}
            onChange={(e) => {
              setCode(e.target.value)
              setAdded(null)
            }}
            maxLength={REDEEM_CODE_LENGTH}
            placeholder={t('Enter your redemption code')}
            autoComplete='off'
            spellCheck={false}
            className='h-9 min-w-0 font-mono'
          />
          <Button type='submit' variant='outline' disabled={!ready}>
            {redeem.isPending && <Loader2 className='size-4 animate-spin' />}
            {redeem.isPending ? t('Redeeming...') : t('Redeem')}
          </Button>
        </div>
        {added !== null && (
          <p role='status' className='text-success text-sm'>
            {t('Redeemed. Quota added')}: {formatQuota(added)}
          </p>
        )}
      </form>
    </TitledCard>
  )
}
