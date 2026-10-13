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
import { BookOpen } from 'lucide-react'
import { useTranslation } from 'react-i18next'


import { ConsoleButton } from '../components/public-header'
import { useSiteInfo } from '../lib/use-site-info'
import { usePublicSession } from '../queries'
import { ApiDemo } from './api-demo'
import { LinkButton } from '../components/link-button'

// Capabilities of the product, as short facts; nothing numeric.
const CAPABILITY_CHIPS = [
  'OpenAI-compatible',
  'Embeddings and rerank',
  'Per-tenant keys and quotas',
  'Audit trail',
  'Single sign-on',
] as const

export function Hero() {
  const { t } = useTranslation()
  const info = useSiteInfo()
  const { signedIn } = usePublicSession()

  return (
    <section className='relative z-10 overflow-hidden px-6 pt-24 pb-16 md:pt-32 md:pb-24 lg:pt-36 lg:pb-28'>
      <div
        aria-hidden
        className='pointer-events-none absolute inset-0 -z-10 opacity-25 dark:opacity-[0.12]'
        style={{
          background: [
            'radial-gradient(ellipse 60% 50% at 20% 20%, oklch(0.72 0.18 250 / 80%) 0%, transparent 70%)',
            'radial-gradient(ellipse 50% 40% at 80% 15%, oklch(0.65 0.15 200 / 60%) 0%, transparent 70%)',
            'radial-gradient(ellipse 40% 35% at 40% 80%, oklch(0.70 0.12 280 / 40%) 0%, transparent 70%)',
          ].join(', '),
        }}
      />
      <div
        aria-hidden
        className='absolute inset-0 -z-10 bg-[linear-gradient(to_right,var(--border)_1px,transparent_1px),linear-gradient(to_bottom,var(--border)_1px,transparent_1px)] [mask-image:radial-gradient(ellipse_60%_50%_at_50%_30%,black_20%,transparent_100%)] bg-[size:4rem_4rem] opacity-[0.08]'
      />

      <div className='mx-auto grid max-w-6xl grid-cols-1 items-start gap-12 lg:grid-cols-12 lg:gap-8'>
        <div className='flex flex-col items-start text-left lg:col-span-6'>
          <div className='landing-animate-fade-up mb-5 inline-flex items-center gap-1.5 rounded-full border border-blue-500/20 bg-blue-500/5 px-3 py-1.5 text-[11px] font-medium text-blue-600 shadow-xs dark:border-blue-400/20 dark:bg-blue-400/5 dark:text-blue-400'>
            <span className='relative flex size-1.5'>
              <span className='absolute inline-flex h-full w-full animate-ping rounded-full bg-blue-400 opacity-75 motion-reduce:animate-none' />
              <span className='relative inline-flex size-1.5 rounded-full bg-blue-500 dark:bg-blue-400' />
            </span>
            <span>{t('Enterprise AI gateway')}</span>
          </div>

          <h1
            className='landing-animate-fade-up text-[clamp(2.25rem,4.5vw,3.25rem)] leading-[1.15] font-bold tracking-tight'
            style={{ animationDelay: '60ms' }}
          >
            {t('One OpenAI-compatible endpoint')}
            <br />
            <span className='bg-gradient-to-r from-blue-400 via-violet-400 to-purple-500 bg-clip-text text-transparent'>
              {t('for every model provider')}
            </span>
          </h1>
          <p
            className='landing-animate-fade-up text-muted-foreground/80 mt-5 max-w-xl text-base leading-relaxed md:text-[15px]'
            style={{ animationDelay: '120ms' }}
          >
            {t(
              'Route requests to many providers through a single API. Give each tenant its own keys, quotas and model allowlist, and keep an exact cost record for every request.'
            )}
          </p>

          <div
            className='landing-animate-fade-up mt-8 flex flex-wrap items-center gap-3'
            style={{ animationDelay: '180ms' }}
          >
            <ConsoleButton
              size='lg'
              withArrow
              className='h-11 rounded-lg px-5 text-sm'
              label={signedIn ? t('Go to console') : t('Console')}
            />
            {info.nav.pricing && (
              <LinkButton
                variant='outline'
                className='border-border/50 hover:border-border hover:bg-muted/50 h-11 rounded-lg px-5 text-sm font-medium'
                render={<Link to='/pricing' />}
              >
                {t('View pricing')}
              </LinkButton>
            )}
            {info.nav.docs && info.docsLink && (
              <LinkButton
                variant='outline'
                className='border-border/50 hover:border-border hover:bg-muted/50 h-11 rounded-lg px-5 text-sm font-medium'
                render={
                  <a
                    href={info.docsLink}
                    {...(/^https?:\/\//.test(info.docsLink)
                      ? { target: '_blank', rel: 'noopener noreferrer' }
                      : {})}
                  />
                }
              >
                <BookOpen className='text-muted-foreground size-4' />
                {t('Docs')}
              </LinkButton>
            )}
          </div>

          <ul
            className='landing-animate-fade-up mt-10 flex max-w-xl flex-wrap gap-2'
            style={{ animationDelay: '240ms' }}
            aria-label={t('Capabilities')}
          >
            {CAPABILITY_CHIPS.map((chip) => (
              <li
                key={chip}
                className='border-border/40 bg-muted/15 text-foreground/75 rounded-full border px-3.5 py-1.5 text-xs font-medium'
              >
                {t(chip)}
              </li>
            ))}
          </ul>
        </div>

        <div
          className='landing-animate-fade-up flex w-full justify-center lg:col-span-6'
          style={{ animationDelay: '320ms' }}
        >
          <ApiDemo className='mt-8 lg:mt-0' />
        </div>
      </div>
    </section>
  )
}
