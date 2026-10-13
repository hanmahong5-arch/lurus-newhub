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
import { FileWarning } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { toApiError } from '@/lib/api'

import type { LegalKind } from './api'
import { OperatorContent } from './components/operator-content'
import { DocumentSkeleton, PageShell } from './components/page-shell'
import { classifyContent } from './lib/content'
import { legalQuery } from './queries'

/** User agreement and privacy policy, exactly as the operator wrote them. */
export function LegalPage(props: { kind: LegalKind }) {
  const { t } = useTranslation()
  const query = useQuery(legalQuery(props.kind))
  const title =
    props.kind === 'user-agreement' ? t('User Agreement') : t('Privacy Policy')

  if (query.isLoading) return <DocumentSkeleton />
  if (query.isError) {
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
  if (!content) {
    return (
      <PageShell narrow>
        <EmptyState
          bordered
          icon={FileWarning}
          title={title}
          description={
            props.kind === 'user-agreement'
              ? t('The administrator has not configured a user agreement yet.')
              : t('The administrator has not configured a privacy policy yet.')
          }
        />
      </PageShell>
    )
  }

  // Markdown gets a visible page title; HTML and links bring their own.
  if (classifyContent(content) === 'markdown') {
    return (
      <PageShell narrow>
        <h1 className='mb-6 text-3xl font-semibold tracking-tight'>{title}</h1>
        <OperatorContent
          content={content}
          title={title}
          urlMode='link'
          className='max-w-none px-0 pt-0 pb-0'
        />
      </PageShell>
    )
  }
  return <OperatorContent content={content} title={title} urlMode='link' />
}
