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
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import {
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from '@/components/ui/sidebar'

import type { NavGroup as NavGroupData } from './nav'

/**
 * A sidebar section of in-app routes. `activeId` is resolved once across all
 * groups (longest match wins), so a sub-route never lights its parent entry
 * in another group.
 */
export function NavGroup(props: {
  group: NavGroupData
  activeId: string | null
}) {
  const { t } = useTranslation()
  const { isMobile, setOpenMobile } = useSidebar()
  const activeId = props.activeId

  return (
    <SidebarGroup>
      <SidebarGroupLabel>{t(props.group.label)}</SidebarGroupLabel>
      <SidebarGroupContent>
        <SidebarMenu>
          {props.group.items.map((item) => (
            <SidebarMenuItem key={item.id}>
              <SidebarMenuButton
                isActive={activeId === item.id}
                tooltip={t(item.label)}
                render={
                  <Link
                    to={item.to}
                    aria-current={activeId === item.id ? 'page' : undefined}
                    onClick={() => {
                      if (isMobile) setOpenMobile(false)
                    }}
                  />
                }
              >
                <item.icon />
                <span>{t(item.label)}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ))}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  )
}
