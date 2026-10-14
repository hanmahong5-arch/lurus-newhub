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
import { useCallback, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { useTheme } from '@/context/theme-provider'

import { OperatorContent } from '../components/operator-content'
import { homeContentQuery } from '../queries'
import { CatalogueStrip } from './catalogue-strip'
import { Cta } from './cta'
import { Features } from './features'
import { Hero } from './hero'
import { HowItWorks } from './how-it-works'

function BuiltInLanding() {
  return (
    <>
      <Hero />
      <CatalogueStrip />
      <Features />
      <HowItWorks />
      <Cta />
    </>
  )
}

/** Shown while the operator's home page is being looked up. */
function LandingPending() {
  const { t } = useTranslation()
  return (
    <div
      role='status'
      aria-busy='true'
      className='text-muted-foreground flex min-h-svh items-center justify-center text-sm'
    >
      {t('Loading...')}
    </div>
  )
}

/**
 * Landing page. If the operator wrote a home page (markdown, HTML or a URL)
 * that replaces the built-in landing; the lookup failing falls back to the
 * built-in one, which carries no data of its own that could be wrong.
 * Signed-in visitors see it too: the console button says so.
 */
export function HomePage() {
  const { t, i18n } = useTranslation()
  const { resolvedTheme } = useTheme()
  const frame = useRef<HTMLIFrameElement>(null)
  const query = useQuery(homeContentQuery)

  // An embedded home page can follow the console's theme and language.
  const syncFrame = useCallback(() => {
    try {
      frame.current?.contentWindow?.postMessage(
        { themeMode: resolvedTheme },
        '*'
      )
      frame.current?.contentWindow?.postMessage({ lang: i18n.language }, '*')
    } catch {
      // A frame that is mid-navigation may refuse; the next load syncs again.
    }
  }, [i18n.language, resolvedTheme])

  useEffect(() => {
    syncFrame()
  }, [syncFrame])

  if (query.isLoading) return <LandingPending />
  const content = query.data ?? ''
  if (!content) return <BuiltInLanding />
  return (
    <OperatorContent
      content={content}
      title={t('Custom Home Page')}
      frameRef={frame}
      onFrameLoad={syncFrame}
    />
  )
}
