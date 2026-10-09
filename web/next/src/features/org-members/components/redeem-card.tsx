import { useMutation } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

import { redeemInvite } from '../api'
import { redeemErrorMessage } from '../lib/errors'

/** Any signed-in user can redeem a code of their own organization. */
export function RedeemCard(props: { onRedeemed: () => void }) {
  const { t } = useTranslation()
  const [code, setCode] = useState('')
  const [done, setDone] = useState(false)

  const redeem = useMutation({
    mutationFn: (c: string) => redeemInvite(c),
    onSuccess: () => {
      setDone(true)
      setCode('')
      props.onRedeemed()
    },
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (redeem.isPending || code.trim() === '') return
    setDone(false)
    redeem.mutate(code.trim())
  }

  return (
    <form onSubmit={submit} className='grid max-w-md gap-3 rounded-lg border p-4'>
      <div className='space-y-1'>
        <h2 className='font-medium'>{t('Redeem an invitation code')}</h2>
        <p className='text-muted-foreground text-sm'>
          {t('Paste a code your administrator gave you to receive its role and department.')}
        </p>
      </div>
      <div className='grid gap-1.5'>
        <Label htmlFor='redeem-code'>{t('Invitation code')}</Label>
        <Input
          id='redeem-code'
          value={code}
          autoComplete='off'
          onChange={(e) => setCode(e.target.value)}
        />
      </div>
      {redeem.isError && (
        <p role='alert' className='text-destructive text-sm'>
          {redeemErrorMessage(redeem.error, t)}
        </p>
      )}
      {done && (
        <p role='status' className='text-success text-sm'>
          {t('Invitation redeemed. Your role and department were updated.')}
        </p>
      )}
      <div>
        <Button type='submit' disabled={redeem.isPending || code.trim() === ''}>
          {t('Redeem')}
        </Button>
      </div>
    </form>
  )
}
