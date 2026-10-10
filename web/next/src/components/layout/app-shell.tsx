import { useQuery } from '@tanstack/react-query'
import { Link, Outlet, useRouterState } from '@tanstack/react-router'
import { ArrowLeft } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { LanguageSwitcher } from '@/components/language-switcher'
import { SkipToMain } from '@/components/skip-to-main'
import { ThemeSwitch } from '@/components/theme-switch'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarRail,
  SidebarTrigger,
} from '@/components/ui/sidebar'
import { BRAND_NAME, LEGACY_CONSOLE_URL, PRODUCT_NAME } from '@/lib/constants'
import { currentUserQueryOptions } from '@/lib/user'

import { visibleNavItems } from './nav'
import { PageFooterProvider } from './page-footer'
import { UserMenu } from './user-menu'

function Brand() {
  return (
    <div className='flex items-center gap-2 px-2 py-1.5'>
      <img src='/next/logo.png' alt='' className='size-7 rounded-md' />
      <div className='grid leading-tight group-data-[collapsible=icon]:hidden'>
        <span className='text-sm font-semibold'>{BRAND_NAME}</span>
        <span className='text-muted-foreground text-xs'>{PRODUCT_NAME}</span>
      </div>
    </div>
  )
}

function AppSidebar() {
  const { t } = useTranslation()
  const { data: user } = useQuery(currentUserQueryOptions)
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const items = visibleNavItems(user?.role)

  return (
    <Sidebar collapsible='icon' variant='inset'>
      <SidebarHeader>
        <Brand />
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupContent>
            <SidebarMenu>
              {items.map((item) => (
                <SidebarMenuItem key={item.id}>
                  <SidebarMenuButton
                    isActive={pathname.startsWith(item.to)}
                    tooltip={t(item.label)}
                    render={<Link to={item.to} />}
                  >
                    <item.icon />
                    <span>{t(item.label)}</span>
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
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
        </SidebarMenu>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}

/** Authenticated layout: sidebar, top bar, routed page. */
export function AppShell() {
  // Pages (data tables) portal their pagination into this footer slot.
  const [footer, setFooter] = useState<HTMLDivElement | null>(null)
  return (
    <SidebarProvider>
      <SkipToMain />
      <AppSidebar />
      <SidebarInset>
        <header className='flex h-14 shrink-0 items-center gap-2 px-4'>
          <SidebarTrigger className='-ms-1' />
          <div className='ms-auto flex items-center gap-1'>
            <LanguageSwitcher />
            <ThemeSwitch />
            <UserMenu />
          </div>
        </header>
        <main
          id='content'
          className='flex flex-1 flex-col gap-6 p-4 pt-0 sm:p-6 sm:pt-0'
        >
          <PageFooterProvider container={footer}>
            <Outlet />
          </PageFooterProvider>
        </main>
        <div ref={setFooter} className='sticky bottom-0 empty:hidden' />
      </SidebarInset>
    </SidebarProvider>
  )
}
