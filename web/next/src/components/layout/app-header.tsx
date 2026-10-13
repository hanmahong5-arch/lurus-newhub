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
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitcher } from '@/components/language-switcher'
import { NotificationPopover } from '@/components/notification-popover'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'

import { Header } from './header'
import { SystemBrand } from './system-brand'
import { useNotifications } from './use-notifications'

/**
 * Top bar: sidebar trigger, brand, then (right) search palette, notices,
 * language, theme, appearance drawer and the account menu.
 */
export function AppHeader() {
  const notifications = useNotifications()

  return (
    <Header>
      <div className='flex min-w-0 flex-1 items-center gap-1'>
        <SystemBrand />
      </div>
      <div className='ms-auto flex shrink-0 items-center gap-1 sm:gap-2'>
        <Search className='w-8 flex-none [&>span]:hidden sm:[&>span]:inline' />
        <NotificationPopover
          open={notifications.popoverOpen}
          onOpenChange={notifications.setPopoverOpen}
          unreadCount={notifications.unreadCount}
          activeTab={notifications.activeTab}
          onTabChange={notifications.setActiveTab}
          notice={notifications.notice}
          announcements={notifications.announcements}
          loading={notifications.loading}
          failed={notifications.failed}
        />
        <LanguageSwitcher />
        <ThemeSwitch className='max-sm:hidden' />
        <ConfigDrawer />
        <ProfileDropdown />
      </div>
    </Header>
  )
}
