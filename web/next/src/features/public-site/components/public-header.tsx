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
import { Link, useRouterState } from '@tanstack/react-router'
import { ArrowRight, Menu } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { LanguageSwitcher } from '@/components/language-switcher'
import { ThemeSwitch } from '@/components/theme-switch'
import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from '@/components/ui/sheet'
import { buildLoginUrl } from '@/lib/auth'
import { cn } from '@/lib/utils'

import { usePublicSession } from '../queries'
import { useSiteInfo } from '../lib/use-site-info'
import { LinkButton } from './link-button'

/** Where the "Console" button leads, and where sign-in returns to. */
export const CONSOLE_PATH = '/dashboard'

/**
 * The call to action that opens the console: a router link when a session
 * exists, otherwise the legacy sign-in page asking to come back to the
 * console. Both read "Console" so the button never changes label under the
 * pointer while the session check is still running.
 */
export function ConsoleButton(props: {
  className?: string
  size?: 'default' | 'sm' | 'lg'
  variant?: 'default' | 'outline'
  withArrow?: boolean
  label?: string
}) {
  const { t } = useTranslation()
  const { signedIn } = usePublicSession()
  const label = props.label ?? t('Console')
  const content: ReactNode = (
    <>
      {label}
      {props.withArrow && (
        <ArrowRight
          aria-hidden
          className='ms-1.5 size-4 transition-transform duration-200 group-hover/button:translate-x-0.5 rtl:rotate-180'
        />
      )}
    </>
  )
  return (
    <LinkButton
      size={props.size}
      variant={props.variant}
      className={props.className}
      render={
        signedIn ? (
          <Link to={CONSOLE_PATH} />
        ) : (
          <a href={buildLoginUrl(CONSOLE_PATH)} />
        )
      }
    >
      {content}
    </LinkButton>
  )
}

interface NavItem {
  key: string
  label: string
  to?: '/pricing' | '/about'
  href?: string
  external?: boolean
}

function useNavItems(): NavItem[] {
  const { t } = useTranslation()
  const info = useSiteInfo()
  const items: NavItem[] = []
  if (info.nav.pricing) {
    items.push({ key: 'pricing', label: t('Pricing'), to: '/pricing' })
  }
  if (info.nav.about) {
    items.push({ key: 'about', label: t('About'), to: '/about' })
  }
  if (info.nav.docs && info.docsLink) {
    items.push({
      key: 'docs',
      label: t('Docs'),
      href: info.docsLink,
      external: /^https?:\/\//.test(info.docsLink),
    })
  }
  return items
}

const LINK_CLASS =
  'rounded-lg px-3 py-1.5 text-sm font-medium transition-colors duration-200 outline-none focus-visible:ring-3 focus-visible:ring-ring/50'

function NavLink(props: {
  item: NavItem
  pathname: string
  onNavigate?: () => void
  className?: string
}) {
  const { item, pathname } = props
  if (item.to) {
    const active = pathname === item.to || pathname.startsWith(`${item.to}/`)
    return (
      <Link
        to={item.to}
        onClick={props.onNavigate}
        aria-current={active ? 'page' : undefined}
        className={cn(
          LINK_CLASS,
          active
            ? 'text-foreground'
            : 'text-muted-foreground hover:text-foreground',
          props.className
        )}
      >
        {item.label}
      </Link>
    )
  }
  return (
    <a
      href={item.href}
      onClick={props.onNavigate}
      {...(item.external
        ? { target: '_blank', rel: 'noopener noreferrer' }
        : {})}
      className={cn(
        LINK_CLASS,
        'text-muted-foreground hover:text-foreground',
        props.className
      )}
    >
      {item.label}
    </a>
  )
}

/**
 * Fixed top bar. Floats as a compact pill once the page has scrolled, like the
 * donor's; the pages below leave their own top padding for it.
 */
export function PublicHeader() {
  const { t } = useTranslation()
  const info = useSiteInfo()
  const items = useNavItems()
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const [scrolled, setScrolled] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 20)
    onScroll()
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  return (
    <header className='pointer-events-none fixed inset-x-0 top-0 z-40'>
      <div
        className={cn(
          'pointer-events-auto mx-auto transition-all duration-500 ease-out',
          scrolled ? 'max-w-4xl px-3 pt-3' : 'max-w-7xl px-4 pt-0 md:px-6'
        )}
      >
        <nav
          aria-label={t('Main')}
          className={cn(
            'flex items-center justify-between gap-2 transition-all duration-500 ease-out',
            scrolled
              ? 'bg-background/70 ring-border/60 h-12 rounded-2xl ps-4 pe-1.5 shadow-sm ring-1 backdrop-blur-xl'
              : 'h-16 px-2'
          )}
        >
          <Link to='/' className='group flex min-w-0 items-center gap-2.5'>
            <img
              src='/next/logo.png'
              alt=''
              width={28}
              height={28}
              className='size-7 shrink-0 rounded-md object-contain transition-transform duration-300 group-hover:scale-105'
            />
            <span
              className='max-w-48 truncate text-sm font-semibold tracking-tight'
              title={info.name}
            >
              {info.name}
            </span>
          </Link>

          <div className='hidden items-center gap-0.5 md:flex'>
            {items.map((item) => (
              <NavLink key={item.key} item={item} pathname={pathname} />
            ))}
            <div aria-hidden className='bg-border/60 mx-2 h-4 w-px' />
            <LanguageSwitcher />
            <ThemeSwitch />
            <ConsoleButton size='sm' className='ms-1 rounded-lg px-3.5' />
          </div>

          <div className='flex items-center gap-1 md:hidden'>
            <ThemeSwitch />
            <Sheet open={menuOpen} onOpenChange={setMenuOpen}>
              <SheetTrigger
                render={
                  <Button
                    variant='ghost'
                    size='icon'
                    className='size-9'
                    aria-label={t('Toggle navigation menu')}
                  />
                }
              >
                <Menu className='size-5' />
              </SheetTrigger>
              <SheetContent side='right' className='w-72 gap-0 p-0'>
                <SheetHeader className='border-b'>
                  <SheetTitle>{info.name}</SheetTitle>
                  <SheetDescription className='sr-only'>
                    {t('Site navigation')}
                  </SheetDescription>
                </SheetHeader>
                <div className='flex flex-1 flex-col justify-between gap-6 p-4'>
                  <div className='flex flex-col'>
                    {items.map((item) => (
                      <NavLink
                        key={item.key}
                        item={item}
                        pathname={pathname}
                        onNavigate={() => setMenuOpen(false)}
                        className='py-3 text-base'
                      />
                    ))}
                  </div>
                  <div className='flex flex-col gap-3'>
                    <div className='flex items-center justify-between'>
                      <span className='text-muted-foreground text-sm'>
                        {t('Language')}
                      </span>
                      <LanguageSwitcher />
                    </div>
                    <ConsoleButton size='lg' withArrow className='w-full' />
                  </div>
                </div>
              </SheetContent>
            </Sheet>
          </div>
        </nav>
      </div>
    </header>
  )
}
