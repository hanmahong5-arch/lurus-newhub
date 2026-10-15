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
import { useState, type ReactNode } from 'react'

import { LanguageSwitcher } from '@/components/language-switcher'
import { useStatus } from '@/hooks/use-status'
import { BRAND_NAME } from '@/lib/constants'
import { cn } from '@/lib/utils'

const FALLBACK_LOGO = '/next/logo.png'

/** Logo and system name from GET /api/status; the product brand until it answers. */
function Brand() {
  const { status } = useStatus()
  const [logoBroken, setLogoBroken] = useState(false)
  const name =
    typeof status?.system_name === 'string' && status.system_name.trim() !== ''
      ? status.system_name.trim()
      : BRAND_NAME
  const logo =
    typeof status?.logo === 'string' && status.logo.trim() !== ''
      ? status.logo.trim()
      : FALLBACK_LOGO

  return (
    <div className='flex items-center justify-center gap-3'>
      {!logoBroken && (
        <img
          src={logo}
          alt=''
          className='size-9 shrink-0 rounded-lg object-contain'
          onError={() => setLogoBroken(true)}
        />
      )}
      <span
        className='text-xl font-semibold tracking-tight'
        data-testid='auth-brand-name'
      >
        {name}
      </span>
    </div>
  )
}

/**
 * Centered, branded frame shared by every sign-in screen. No navigation, no
 * tenant switcher: whoever lands here is not (or no longer) allowed into the
 * console.
 */
export function AuthLayout(props: { children: ReactNode; wide?: boolean }) {
  return (
    <div className='bg-background relative flex min-h-svh flex-col items-center justify-center overflow-hidden px-4 py-10'>
      <div
        aria-hidden='true'
        className='pointer-events-none absolute inset-0 bg-[radial-gradient(60rem_30rem_at_50%_-10%,color-mix(in_oklab,var(--primary)_14%,transparent),transparent)]'
      />
      <div className='absolute top-3 right-3 z-10'>
        <LanguageSwitcher />
      </div>
      <main
        className={cn(
          'relative z-10 flex w-full flex-col gap-6',
          props.wide ? 'max-w-3xl' : 'max-w-md'
        )}
      >
        <Brand />
        {props.children}
      </main>
    </div>
  )
}
