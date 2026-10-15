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
import { Layers, RefreshCw, Search } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { formatTimestampToDate } from '@/components/format'
import { LoadingState } from '@/components/loading-state'
import { SectionPageLayout } from '@/components/section-page-layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { isApiError, toApiError } from '@/lib/api'

import { modelPoolsQueryOptions } from './api'
import { PoolCard } from './components/pool-card'
import {
  PoolsError,
  PoolsForbidden,
  PoolsSignedOut,
} from './components/pools-state'
import { channelTypeLabel } from './lib/channel-type'
import { filterPools, summarize } from './lib/health'

type FailureKind = 'forbidden' | 'signed-out' | 'error'

// 403 / 401 / everything else are three different statements to an operator,
// not one blank table.
function classify(error: unknown): FailureKind {
  if (isApiError(error)) {
    if (error.status === 403 || error.code === 'PERMISSION_DENIED') {
      return 'forbidden'
    }
    if (error.status === 401) return 'signed-out'
  }
  return 'error'
}

export function AdminModelPoolsPage() {
  const { t } = useTranslation()
  const overview = useQuery(modelPoolsQueryOptions)
  const [query, setQuery] = useState('')
  const [onlyUnhealthy, setOnlyUnhealthy] = useState(false)

  const pools = useMemo(() => overview.data?.pools ?? [], [overview.data])
  const visible = useMemo(
    () => filterPools(pools, query, onlyUnhealthy, channelTypeLabel),
    [pools, query, onlyUnhealthy]
  )
  const summary = useMemo(() => summarize(pools), [pools])

  let body
  if (overview.isPending) {
    body = <LoadingState />
  } else if (overview.isError) {
    const kind = classify(overview.error)
    if (kind === 'forbidden') {
      body = <PoolsForbidden />
    } else if (kind === 'signed-out') {
      body = <PoolsSignedOut />
    } else {
      body = (
        <PoolsError
          message={toApiError(overview.error).message}
          onRetry={() => void overview.refetch()}
        />
      )
    }
  } else if (pools.length === 0) {
    body = (
      <div data-testid='pools-empty'>
        <EmptyState
          icon={Layers}
          title={t('No routing pools found')}
          description={t(
            'Pools appear once a channel is assigned to a routing group.'
          )}
          bordered
        />
      </div>
    )
  } else {
    body = (
      <div className='mx-auto grid w-full max-w-6xl gap-4'>
        <div className='flex flex-wrap items-center gap-x-4 gap-y-2'>
          <div className='relative w-full sm:w-72'>
            <Search
              className='text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2'
              aria-hidden='true'
            />
            <Input
              aria-label={t('Filter pools, channels or models')}
              placeholder={t('Filter pools, channels or models')}
              className='pl-8'
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          <div className='flex items-center gap-2'>
            <Switch
              id='pools-only-unhealthy'
              checked={onlyUnhealthy}
              onCheckedChange={setOnlyUnhealthy}
            />
            <Label htmlFor='pools-only-unhealthy' className='text-sm'>
              {t('Only unhealthy models')}
            </Label>
          </div>
          <p
            className='text-muted-foreground text-xs sm:ml-auto'
            data-testid='pools-summary'
          >
            {t(
              '{{pools}} pools · {{channels}} channels · {{models}} models · {{unhealthy}} unhealthy',
              {
                pools: summary.pools,
                channels: summary.channels,
                models: summary.models,
                unhealthy: summary.unhealthy,
              }
            )}
          </p>
        </div>

        {visible.length === 0 ? (
          <div data-testid='pools-no-match'>
            <EmptyState
              icon={Search}
              title={t('Nothing matches this filter')}
              description={t('Clear the filter to see every pool again.')}
              bordered
            />
          </div>
        ) : (
          visible.map((pool) => <PoolCard key={pool.group} pool={pool} />)
        )}
      </div>
    )
  }

  const generatedAt = overview.data?.generated_at ?? 0

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Model pools')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        {generatedAt > 0 && (
          <span className='text-muted-foreground hidden text-xs sm:inline'>
            {t('Updated {{time}}', {
              time: formatTimestampToDate(generatedAt),
            })}
          </span>
        )}
        <Button
          size='sm'
          variant='outline'
          disabled={overview.isFetching}
          onClick={() => void overview.refetch()}
        >
          <RefreshCw
            className={overview.isFetching ? 'animate-spin' : undefined}
          />
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>{body}</SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
