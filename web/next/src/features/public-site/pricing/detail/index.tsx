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
import { ArrowLeft, Code2, HeartPulse, Info } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toApiError } from '@/lib/api'

import { PageShell } from '../../components/page-shell'
import { relayOrigin, useSiteInfo } from '../../lib/use-site-info'
import type { PricingSearch } from '../constants'
import { ModelAvatar, TypeBadge } from '../components/price-cells'
import { PricingGate } from '../gate'
import { usePublicModels } from '../use-public-models'
import { ApiTab } from './api-tab'
import { Overview } from './overview'
import { StatusTab } from './status-tab'
import { LinkButton } from '../../components/link-button'

function DetailSkeleton() {
  return (
    <PageShell narrow>
      <div aria-busy='true' role='status'>
        <Skeleton className='mb-4 h-5 w-16' />
        <div className='space-y-2'>
          <Skeleton className='h-7 w-64' />
          <Skeleton className='h-4 w-40' />
          <Skeleton className='h-4 w-full max-w-md' />
        </div>
        <div className='mt-6 grid grid-cols-2 gap-2 sm:grid-cols-4'>
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className='h-16 w-full' />
          ))}
        </div>
        <Skeleton className='mt-6 h-40 w-full' />
      </div>
    </PageShell>
  )
}

function BackLink(props: { search: PricingSearch }) {
  const { t } = useTranslation()
  return (
    <LinkButton
      variant='ghost'
      size='sm'
      className='text-muted-foreground hover:text-foreground mb-4 h-auto gap-1 px-0 py-1 text-xs'
      render={<Link to='/pricing' search={props.search} />}
    >
      <ArrowLeft aria-hidden className='size-3.5 rtl:rotate-180' />
      {t('Back to Models')}
    </LinkButton>
  )
}

function Detail(props: { modelId: string; search: PricingSearch }) {
  const { t } = useTranslation()
  const data = usePublicModels()
  const info = useSiteInfo()
  const unit = props.search.unit ?? 'M'

  if (data.isLoading) return <DetailSkeleton />
  if (data.isError) {
    return (
      <PageShell narrow>
        <BackLink search={props.search} />
        <ErrorState
          title={t('Failed to load the price list')}
          description={toApiError(data.error).message}
          onRetry={() => void data.refetch()}
        />
      </PageShell>
    )
  }

  const model = data.models.find((m) => m.id === props.modelId)
  if (!model) {
    return (
      <PageShell narrow>
        <BackLink search={props.search} />
        <EmptyState
          bordered
          title={t('Model not found')}
          description={t(
            "This model is not on the public price list. It may have been renamed or removed."
          )}
        />
      </PageShell>
    )
  }

  return (
    <PageShell narrow className='max-w-5xl'>
      <BackLink search={props.search} />

      <header className='pb-4'>
        <div className='flex items-center gap-2.5'>
          <ModelAvatar model={model} size={22} />
          <h1 className='font-mono text-xl font-bold tracking-tight [overflow-wrap:anywhere] sm:text-2xl'>
            {model.id}
          </h1>
          <CopyButton
            value={model.id}
            className='size-6'
            iconClassName='size-3'
            tooltip={t('Copy model name')}
            successTooltip={t('Copied!')}
          />
        </div>
        <div className='mt-1.5 flex flex-wrap items-center gap-2 text-xs'>
          {model.vendor && (
            <span className='text-muted-foreground'>{model.vendor}</span>
          )}
          <TypeBadge model={model} />
        </div>
        {model.description && (
          <p className='text-muted-foreground mt-2 text-sm leading-relaxed'>
            {model.description}
          </p>
        )}
      </header>

      <Tabs defaultValue='overview' className='gap-4'>
        <TabsList className='bg-muted/60 grid w-full grid-cols-3 gap-1 rounded-lg p-1 group-data-horizontal/tabs:h-auto'>
          <TabsTrigger value='overview' className='h-8 gap-1.5 text-xs sm:text-sm'>
            <Info aria-hidden className='size-3.5' />
            <span className='truncate'>{t('Overview')}</span>
          </TabsTrigger>
          <TabsTrigger value='status' className='h-8 gap-1.5 text-xs sm:text-sm'>
            <HeartPulse aria-hidden className='size-3.5' />
            <span className='truncate'>{t('Status')}</span>
          </TabsTrigger>
          <TabsTrigger value='api' className='h-8 gap-1.5 text-xs sm:text-sm'>
            <Code2 aria-hidden className='size-3.5' />
            <span className='truncate'>{t('API')}</span>
          </TabsTrigger>
        </TabsList>

        <TabsContent value='overview' className='outline-none'>
          <Overview model={model} unit={unit} />
        </TabsContent>
        <TabsContent value='status' className='outline-none'>
          <StatusTab model={model} />
        </TabsContent>
        <TabsContent value='api' className='outline-none'>
          <ApiTab
            model={model}
            endpoints={data.payload?.endpoints ?? {}}
            baseUrl={relayOrigin(info)}
          />
        </TabsContent>
      </Tabs>
    </PageShell>
  )
}

/** Public detail page of one model: price by group, status, how to call it. */
export function PublicModelDetailPage(props: {
  modelId: string
  search: PricingSearch
}) {
  return (
    <PricingGate
      returnTo={`/pricing/${encodeURIComponent(props.modelId)}`}
      fallback={<DetailSkeleton />}
    >
      <Detail modelId={props.modelId} search={props.search} />
    </PricingGate>
  )
}
