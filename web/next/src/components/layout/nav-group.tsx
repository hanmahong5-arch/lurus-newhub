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
import { ChevronRight, ExternalLink } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from '@/components/ui/sidebar'

import type { ClassicNavGroup as ClassicNavGroupData } from './classic-nav'
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

/**
 * Links to legacy pages with no equivalent here yet. Plain anchors: following
 * one leaves the SPA. Collapsed by default; in icon mode the entries are shown
 * as icons so they stay reachable.
 */
export function ClassicNavGroup(props: { group: ClassicNavGroupData }) {
  const { t } = useTranslation()
  const { state, isMobile } = useSidebar()
  const [open, setOpen] = useState(false)
  const iconMode = state === 'collapsed' && !isMobile

  return (
    <SidebarGroup>
      <Collapsible open={iconMode || open} onOpenChange={setOpen}>
        <SidebarGroupLabel
          render={<CollapsibleTrigger className='w-full cursor-pointer' />}
        >
          {t(props.group.label)}
          <ChevronRight
            aria-hidden='true'
            className={
              open
                ? 'ms-auto rotate-90 transition-transform duration-200'
                : 'ms-auto transition-transform duration-200'
            }
          />
        </SidebarGroupLabel>
        <CollapsibleContent>
          <SidebarGroupContent>
            <SidebarMenu>
              {props.group.items.map((item) => (
                <SidebarMenuItem key={item.id}>
                  <SidebarMenuButton
                    tooltip={t(item.label)}
                    render={<a href={item.href} />}
                  >
                    <item.icon />
                    <span>{t(item.label)}</span>
                    <ExternalLink
                      aria-hidden='true'
                      className='ms-auto size-3! opacity-60'
                    />
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </CollapsibleContent>
      </Collapsible>
    </SidebarGroup>
  )
}
