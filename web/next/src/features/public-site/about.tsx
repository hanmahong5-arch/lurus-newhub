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
import { Link } from '@tanstack/react-router'
import {
  FileText,
  KeyRound,
  Layers,
  ReceiptText,
  ScrollText,
  ShieldCheck,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { toApiError } from '@/lib/api'

import { ConsoleButton } from './components/public-header'
import { NEW_API_URL } from './components/public-footer'
import { OperatorContent } from './components/operator-content'
import { DocumentSkeleton, PageShell } from './components/page-shell'
import { aboutContentQuery } from './queries'
import { useSiteInfo } from './lib/use-site-info'
import { LinkButton } from './components/link-button'

const ONE_API_URL = 'https://github.com/songquanpeng/one-api'
const AGPL_URL = 'https://www.gnu.org/licenses/agpl-3.0.html'

function ExternalLinkText(props: { href: string; children: React.ReactNode }) {
  return (
    <a
      href={props.href}
      target='_blank'
      rel='noopener noreferrer'
      className='text-primary underline-offset-4 hover:underline'
    >
      {props.children}
    </a>
  )
}

/** Shown when the operator has not written an About page. */
function BuiltInAbout() {
  const { t } = useTranslation()
  const info = useSiteInfo()
  const points = [
    {
      icon: Layers,
      title: t('One endpoint, many providers'),
      body: t(
        'Send OpenAI-compatible requests and let the gateway route them to the provider that serves the model.'
      ),
    },
    {
      icon: KeyRound,
      title: t('Per-tenant control'),
      body: t(
        'Each tenant gets its own keys, quotas and model allowlist, kept apart from every other tenant.'
      ),
    },
    {
      icon: ReceiptText,
      title: t('Exact cost records'),
      body: t(
        'Every request is metered and recorded with its token counts and cost, including embeddings and rerank.'
      ),
    },
    {
      icon: ShieldCheck,
      title: t('Audit trail and SSO'),
      body: t(
        'Administrative changes are written to an audit log, and people sign in through your single sign-on provider.'
      ),
    },
  ]

  return (
    <PageShell narrow>
      <header className='mb-8'>
        <h1 className='text-3xl font-semibold tracking-tight'>
          {t('About {{name}}', { name: info.name })}
        </h1>
        <p className='text-muted-foreground mt-3 text-sm leading-relaxed'>
          {t(
            '{{name}} is an enterprise AI gateway. It puts many model providers behind one OpenAI-compatible endpoint and adds what a company needs on top: tenants, keys, quotas, unified billing and an audit trail.',
            { name: info.name }
          )}
        </p>
      </header>

      <ul className='grid gap-3 sm:grid-cols-2'>
        {points.map((point) => (
          <li key={point.title} className='bg-card rounded-xl border p-4'>
            <point.icon className='text-muted-foreground mb-2 size-5' />
            <h2 className='text-sm font-semibold'>{point.title}</h2>
            <p className='text-muted-foreground mt-1 text-sm leading-relaxed'>
              {point.body}
            </p>
          </li>
        ))}
      </ul>

      <div className='mt-8 flex flex-wrap gap-3'>
        {info.nav.pricing && (
          <LinkButton variant='outline' render={<Link to='/pricing' />}>
            <ScrollText className='me-1.5 size-4' />
            {t('View pricing')}
          </LinkButton>
        )}
        <ConsoleButton withArrow />
      </div>

      <section className='border-border/60 mt-12 border-t pt-6'>
        <h2 className='flex items-center gap-2 text-sm font-semibold'>
          <FileText className='text-muted-foreground size-4' />
          {t('Open source')}
        </h2>
        <p className='text-muted-foreground mt-2 text-sm leading-relaxed'>
          {t('Built on')}{' '}
          <ExternalLinkText href={NEW_API_URL}>New API</ExternalLinkText>{' '}
          {t('by QuantumNous, which is based on')}{' '}
          <ExternalLinkText href={ONE_API_URL}>One API</ExternalLinkText>{' '}
          {t('by JustSong. Released under the')}{' '}
          <ExternalLinkText href={AGPL_URL}>
            {t('GNU AGPL v3.0')}
          </ExternalLinkText>
          .
        </p>
      </section>
    </PageShell>
  )
}

export function AboutPage() {
  const { t } = useTranslation()
  const query = useQuery(aboutContentQuery)

  if (query.isLoading) return <DocumentSkeleton />
  if (query.isError) {
    // An operator page may exist: do not show the built-in text as if it did
    // not. Say the request failed and offer a retry.
    return (
      <PageShell narrow>
        <ErrorState
          title={t('Failed to load this page')}
          description={toApiError(query.error).message}
          onRetry={() => void query.refetch()}
        />
      </PageShell>
    )
  }
  const content = query.data ?? ''
  if (!content) return <BuiltInAbout />
  return <OperatorContent content={content} title={t('About')} />
}
