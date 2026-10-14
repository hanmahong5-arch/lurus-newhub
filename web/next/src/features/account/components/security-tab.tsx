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
import { LogOut } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { TitledCard } from '@/components/ui/titled-card'
import { signOut } from '@/lib/session'

import { SessionsCard } from './sessions-card'
import { TwoFaCard } from './two-fa-card'

function SignOutCard() {
  const { t } = useTranslation()
  const [busy, setBusy] = useState(false)
  return (
    <TitledCard
      title={t('Sign out')}
      description={t('End the session in this browser.')}
      icon={<LogOut className='size-4' />}
      iconTone='destructive'
      disableHoverEffect
    >
      <Button
        type='button'
        variant='outline'
        disabled={busy}
        onClick={() => {
          setBusy(true)
          void signOut()
        }}
      >
        <LogOut className='size-4' />
        {t('Sign out')}
      </Button>
    </TitledCard>
  )
}

export function SecurityTab() {
  const { t } = useTranslation()
  return (
    <div className='grid gap-4 sm:gap-5 xl:grid-cols-[minmax(0,1fr)_minmax(360px,0.46fr)] xl:items-start'>
      <section aria-labelledby='account-sessions' className='min-w-0 space-y-3'>
        <h3 id='account-sessions' className='text-sm font-semibold'>
          {t('Sessions & Access')}
        </h3>
        <SessionsCard />
        <SignOutCard />
      </section>
      <aside
        aria-labelledby='account-verification'
        className='min-w-0 space-y-3 xl:sticky xl:top-0'
      >
        <h3 id='account-verification' className='text-sm font-semibold'>
          {t('Security verification')}
        </h3>
        <TwoFaCard />
      </aside>
    </div>
  )
}
