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
import { Link, useRouterState } from '@tanstack/react-router'
import { ArrowLeft } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  useSidebar,
} from '@/components/ui/sidebar'
import { useLayout } from '@/context/layout-provider'
import { getUserAvatarFallback, getUserAvatarStyle } from '@/lib/avatar'
import { LEGACY_CONSOLE_URL } from '@/lib/constants'
import { getRoleLabel } from '@/lib/roles'
import { currentUserQueryOptions, userAccess } from '@/lib/user'

import { visibleClassicGroup } from './classic-nav'
import { visibleNavGroups } from './nav'
import { activeNavItemId } from './nav-active'
import { ClassicNavGroup, NavGroup } from './nav-group'

function UserCard() {
  const { t } = useTranslation()
  const { data: user } = useQuery(currentUserQueryOptions)
  const { isMobile, setOpenMobile } = useSidebar()
  const name = user?.display_name || user?.username || t('Account')
  const avatarName = user?.username || name
  const style = useMemo(() => getUserAvatarStyle(avatarName), [avatarName])

  return (
    <SidebarMenuButton
      size='lg'
      tooltip={name}
      render={
        <Link
          to='/account'
          onClick={() => {
            if (isMobile) setOpenMobile(false)
          }}
        />
      }
    >
      <Avatar className='size-8 rounded-lg'>
        <AvatarFallback
          className='rounded-lg text-xs font-semibold text-white'
          style={style}
        >
          {getUserAvatarFallback(avatarName)}
        </AvatarFallback>
      </Avatar>
      <span className='grid min-w-0 flex-1 text-start text-sm leading-tight'>
        <span className='truncate font-medium'>{name}</span>
        <span className='text-muted-foreground truncate text-xs'>
          {user?.email || (user ? getRoleLabel(user.role) : '')}
        </span>
      </span>
    </SidebarMenuButton>
  )
}

/**
 * Application sidebar: access-gated route groups, the classic-console links,
 * and a footer with the signed-in user. Layout (variant, collapse mode) comes
 * from the persisted LayoutProvider; mobile renders as a sheet.
 */
export function AppSidebar() {
  const { t } = useTranslation()
  const { collapsible, variant } = useLayout()
  const { data: user } = useQuery(currentUserQueryOptions)
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const groups = useMemo(() => visibleNavGroups(userAccess(user)), [user])
  const classic = useMemo(() => visibleClassicGroup(user), [user])
  const activeId = useMemo(
    () =>
      activeNavItemId(
        pathname,
        groups.flatMap((g) => g.items)
      ),
    [groups, pathname]
  )

  return (
    <Sidebar collapsible={collapsible} variant={variant}>
      <SidebarContent className='py-2'>
        {groups.map((group) => (
          <NavGroup key={group.id} group={group} activeId={activeId} />
        ))}
        {classic != null && <ClassicNavGroup group={classic} />}
      </SidebarContent>
      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              tooltip={t('Back to the previous console')}
              render={<a href={LEGACY_CONSOLE_URL} />}
            >
              <ArrowLeft />
              <span>{t('Back to the previous console')}</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <UserCard />
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}
