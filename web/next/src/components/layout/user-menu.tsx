import { useQuery, useQueryClient } from '@tanstack/react-query'
import { LogOut, User } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { signOut } from '@/lib/session'
import { currentUserQueryOptions } from '@/lib/user'

export function UserMenu() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { data: user } = useQuery(currentUserQueryOptions)
  const name = user?.display_name || user?.username || ''

  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger
        render={<Button variant='ghost' size='icon' className='h-9 w-9' />}
      >
        <User className='size-[1.2rem]' />
        <span className='sr-only'>{t('Account')}</span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end' className='min-w-48'>
        {name !== '' && (
          <>
            <DropdownMenuLabel className='font-normal'>
              <div className='text-sm font-medium'>{name}</div>
              {user?.email ? (
                <div className='text-muted-foreground text-xs'>
                  {user.email}
                </div>
              ) : null}
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
          </>
        )}
        <DropdownMenuItem
          onClick={() => {
            queryClient.clear()
            void signOut()
          }}
        >
          <LogOut className='size-4' />
          {t('Sign out')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
