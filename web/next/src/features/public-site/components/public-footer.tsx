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

import { HtmlContent } from '@/components/html-content'

import { useSiteInfo } from '../lib/use-site-info'

export const NEW_API_URL = 'https://github.com/QuantumNous/new-api'

const FOOTER_LINK =
  'hover:text-foreground rounded-sm transition-colors duration-200 outline-none focus-visible:ring-3 focus-visible:ring-ring/50'

export function PublicFooter() {
  const { t } = useTranslation()
  const info = useSiteInfo()
  const year = new Date().getFullYear()

  const productLinks = [
    info.nav.pricing && (
      <Link key='pricing' to='/pricing' className={FOOTER_LINK}>
        {t('Pricing')}
      </Link>
    ),
    info.nav.about && (
      <Link key='about' to='/about' className={FOOTER_LINK}>
        {t('About')}
      </Link>
    ),
    info.nav.docs && info.docsLink && (
      <a
        key='docs'
        href={info.docsLink}
        className={FOOTER_LINK}
        {...(/^https?:\/\//.test(info.docsLink)
          ? { target: '_blank', rel: 'noopener noreferrer' }
          : {})}
      >
        {t('Docs')}
      </a>
    ),
  ].filter(Boolean)

  // The links exist only when the operator has written the document: a link
  // to an empty page is worse than no link.
  const legalLinks = [
    info.agreementEnabled && (
      <Link key='agreement' to='/user-agreement' className={FOOTER_LINK}>
        {t('User Agreement')}
      </Link>
    ),
    info.privacyEnabled && (
      <Link key='privacy' to='/privacy-policy' className={FOOTER_LINK}>
        {t('Privacy Policy')}
      </Link>
    ),
  ].filter(Boolean)

  return (
    <footer className='border-border/40 relative z-10 border-t'>
      <div className='mx-auto w-full max-w-6xl px-6 py-10 md:py-12'>
        <div className='flex flex-col justify-between gap-8 md:flex-row'>
          <div className='max-w-xs'>
            <Link to='/' className='flex items-center gap-2.5'>
              <img
                src='/next/logo.png'
                alt=''
                width={28}
                height={28}
                className='size-7 rounded-md object-contain'
              />
              <span className='text-sm font-semibold tracking-tight'>
                {info.name}
              </span>
            </Link>
            <p className='text-muted-foreground mt-3 text-xs leading-relaxed'>
              {t(
                'An enterprise AI gateway: one endpoint for many providers, with per-tenant keys and exact cost records.'
              )}
            </p>
          </div>

          {(productLinks.length > 0 || legalLinks.length > 0) && (
            <nav
              aria-label={t('Footer')}
              className='text-muted-foreground grid grid-cols-2 gap-x-12 gap-y-6 text-sm'
            >
              {productLinks.length > 0 && (
                <div className='flex flex-col gap-2.5'>
                  <p className='text-foreground/70 text-xs font-medium tracking-wider uppercase'>
                    {t('Product')}
                  </p>
                  {productLinks}
                </div>
              )}
              {legalLinks.length > 0 && (
                <div className='flex flex-col gap-2.5'>
                  <p className='text-foreground/70 text-xs font-medium tracking-wider uppercase'>
                    {t('Legal')}
                  </p>
                  {legalLinks}
                </div>
              )}
            </nav>
          )}
        </div>

        {info.footerHtml && (
          <HtmlContent
            content={info.footerHtml}
            className='text-muted-foreground mt-8 text-sm'
          />
        )}

        <div className='border-border/40 text-muted-foreground mt-8 flex flex-col items-start justify-between gap-2 border-t pt-6 text-xs sm:flex-row sm:items-center'>
          <span>
            &copy; {year} {info.name}
          </span>
          <span>
            <a
              href={NEW_API_URL}
              target='_blank'
              rel='noopener noreferrer'
              className='text-foreground/70 hover:text-foreground font-medium underline-offset-4 transition-colors hover:underline'
            >
              {t('Based on New API (AGPL-3.0)')}
            </a>
          </span>
        </div>
      </div>
    </footer>
  )
}
