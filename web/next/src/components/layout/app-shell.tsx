import { Outlet } from '@tanstack/react-router'
import { useState } from 'react'

import { CommandMenu } from '@/components/command-menu'
import { SkipToMain } from '@/components/skip-to-main'
import { SidebarInset, SidebarProvider } from '@/components/ui/sidebar'
import { LayoutProvider } from '@/context/layout-provider'
import { SearchProvider } from '@/context/search-provider'
import { ThemeCustomizationProvider } from '@/context/theme-customization-provider'
import { getCookie } from '@/lib/cookies'
import { cn } from '@/lib/utils'

import { AppHeader } from './app-header'
import { AppSidebar } from './app-sidebar'
import { PageFooterProvider } from './page-footer'

/**
 * Authenticated layout: top bar over a sidebar + content row. The providers
 * the shell owns (layout, theme customization, command palette) are mounted
 * here because the app root is not part of this module.
 *
 * The content area is a fixed-height column that scrolls on its own, which is
 * what SectionPageLayout (and the data-table pagination footer) expect.
 */
export function AppShell() {
  // Pages (data tables) portal their pagination into this footer slot.
  const [footer, setFooter] = useState<HTMLDivElement | null>(null)
  const defaultOpen = getCookie('sidebar_state') !== 'false'

  return (
    <ThemeCustomizationProvider>
      <LayoutProvider>
        <SidebarProvider defaultOpen={defaultOpen} className='flex-col'>
          <SearchProvider>
            <SkipToMain />
            <CommandMenu />
            <AppHeader />
            <div className='flex min-h-0 w-full flex-1'>
              <AppSidebar />
              <SidebarInset
                className={cn(
                  '@container/content',
                  'h-[calc(100svh-var(--app-header-height,0px))]',
                  'min-h-0 min-w-0 overflow-hidden',
                  'peer-data-[variant=inset]:h-[calc(100svh-var(--app-header-height,0px)-(var(--spacing)*4))]'
                )}
              >
                <div
                  id='content'
                  tabIndex={-1}
                  className='flex min-h-0 flex-1 flex-col overflow-y-auto outline-none'
                >
                  <PageFooterProvider container={footer}>
                    <Outlet />
                  </PageFooterProvider>
                </div>
                <div ref={setFooter} className='sticky bottom-0 empty:hidden' />
              </SidebarInset>
            </div>
          </SearchProvider>
        </SidebarProvider>
      </LayoutProvider>
    </ThemeCustomizationProvider>
  )
}
