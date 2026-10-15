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
import { useNavigate } from '@tanstack/react-router'
import { LogOut, ShieldCheck, Wallet } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ThemeQuickSwitcher } from '@/components/theme-quick-switcher'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { getUserAvatarFallback, getUserAvatarStyle } from '@/lib/avatar'
import { getRoleLabel } from '@/lib/roles'
import { signOut } from '@/lib/session'
import { currentUserQueryOptions } from '@/lib/user'

const avatarFallbackClassName = 'font-semibold text-white'

/** Header account menu: who is signed in, account shortcuts, theme, sign out. */
export function ProfileDropdown() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { data: user } = useQuery(currentUserQueryOptions)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [signingOut, setSigningOut] = useState(false)

  const displayName = user?.display_name || user?.username || t('Account')
  const avatarName = user?.username || displayName
  const avatarFallback = getUserAvatarFallback(avatarName)
  const avatarFallbackStyle = useMemo(
    () => getUserAvatarStyle(avatarName),
    [avatarName]
  )
  const roleLabel = user ? getRoleLabel(user.role) : ''

  const handleSignOut = () => {
    setSigningOut(true)
    queryClient.clear()
    void signOut()
  }

  return (
    <>
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger
          render={
            <Button
              variant='ghost'
              className='relative size-8 rounded-full p-0'
              aria-label={t('Account')}
            />
          }
        >
          <Avatar className='size-7'>
            <AvatarFallback
              className={`${avatarFallbackClassName} text-[11px]`}
              style={avatarFallbackStyle}
            >
              {avatarFallback}
            </AvatarFallback>
          </Avatar>
        </DropdownMenuTrigger>
        <DropdownMenuContent align='end' sideOffset={8} className='w-60'>
          <div className='flex items-center gap-2 px-1.5 py-1.5'>
            <Avatar className='size-8'>
              <AvatarFallback
                className={`${avatarFallbackClassName} text-xs`}
                style={avatarFallbackStyle}
              >
                {avatarFallback}
              </AvatarFallback>
            </Avatar>
            <div className='flex min-w-0 flex-1 flex-col gap-0.5'>
              <p className='text-foreground truncate text-sm font-medium'>
                {displayName}
              </p>
              {user?.email ? (
                <p className='text-muted-foreground truncate text-xs'>
                  {user.email}
                </p>
              ) : null}
              <div className='text-muted-foreground flex items-center gap-1.5 text-xs'>
                <span>{roleLabel}</span>
                {user?.group ? (
                  <>
                    <span>·</span>
                    <span className='truncate'>{user.group}</span>
                  </>
                ) : null}
              </div>
            </div>
          </div>

          <DropdownMenuSeparator />

          <DropdownMenuItem onClick={() => void navigate({ to: '/account' })}>
            <ShieldCheck className='size-4' />
            {t('Account & Security')}
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => void navigate({ to: '/wallet' })}>
            <Wallet className='size-4' />
            {t('Wallet')}
          </DropdownMenuItem>

          <DropdownMenuSeparator />
          <ThemeQuickSwitcher />
          <DropdownMenuSeparator />

          <DropdownMenuItem
            variant='destructive'
            onClick={() => setConfirmOpen(true)}
          >
            <LogOut className='size-4' />
            {t('Sign out')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Sign out')}
        desc={t('Are you sure you want to sign out?')}
        confirmText={t('Sign out')}
        destructive
        isLoading={signingOut}
        handleConfirm={handleSignOut}
      />
    </>
  )
}
