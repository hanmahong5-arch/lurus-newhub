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
import { useNavigate } from '@tanstack/react-router'
import { Activity as ActivityIcon, RefreshCw } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { PageHeader } from '@/components/layout/page-header'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { fetchQuotaRows } from '@/features/dashboard/lib/api'
import {
  aggregateUsage,
  type UsageMetric,
} from '@/features/dashboard/lib/series'
import { fetchLogStat } from '@/features/usage-logs/api'
import { EMPTY_FILTERS } from '@/features/usage-logs/lib/logs'
import { useUserAccess } from '@/hooks/use-admin'
import { useMoneyConfig, statusQueryOptions } from '@/lib/status'

import { ExploreTab } from './components/explore-tab'
import { OverviewTab } from './components/overview-tab'
import { PeriodControl } from './components/period-control'
import {
  sparseActivitySearch,
  toLogDatetime,
  type ActivityDays,
  type ActivityGranularity,
  type ActivitySearch,
  type ActivityTab,
} from './lib/search'

function defaultGranularity(days: ActivityDays): ActivityGranularity {
  return days <= 1 ? 'hour' : 'day'
}

export function ActivityPage(props: { search: ActivitySearch }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const money = useMoneyConfig()
  const access = useUserAccess()
  const status = useQuery(statusQueryOptions)

  const tab: ActivityTab = props.search.tab ?? 'overview'
  const days: ActivityDays = props.search.days ?? 7
  const metric: UsageMetric = props.search.metric ?? 'quota'
  const granularity: ActivityGranularity =
    props.search.granularity ?? defaultGranularity(days)

  const patchSearch = (patch: ActivitySearch) => {
    void navigate({
      to: '/activity',
      search: sparseActivitySearch({
        tab,
        days,
        metric,
        granularity,
        ...patch,
      }),
      replace: true,
    })
  }

  const exportEnabled = status.data?.enable_data_export === true
  const exportUnknown = status.isError
  const nowSec = Math.floor(Date.now() / 1000)
  const start = nowSec - days * 86_400
  const end = nowSec

  const quota = useQuery({
    queryKey: ['activity', 'quota', days],
    queryFn: () => fetchQuotaRows(start, end),
    retry: false,
    enabled: exportEnabled,
  })

  // Log stat uses the same wall-clock window as Activity.
  const statFilters = useMemo(
    () => ({
      ...EMPTY_FILTERS,
      start: toLogDatetime(start),
      end: toLogDatetime(end),
    }),
    [start, end]
  )

  const stat = useQuery({
    queryKey: ['activity', 'log-stat', days],
    queryFn: () => fetchLogStat(statFilters),
    retry: false,
  })

  const usage = useMemo(
    () =>
      aggregateUsage(
        quota.data ?? [],
        granularity,
        start,
        end
      ),
    [quota.data, granularity, start, end]
  )

  const loading = exportEnabled && quota.isPending
  const failed = exportEnabled && quota.isError
  // Only treat export as off once /api/status succeeded with the flag false.
  const exportDisabled = status.isSuccess && !exportEnabled

  const retry = () => {
    void status.refetch()
    void quota.refetch()
    void stat.refetch()
  }

  return (
    <>
      <PageHeader
        title={t('Activity')}
        description={t(
          'Explore spend, requests and tokens by model, then drill into usage logs.'
        )}
        actions={
          <div className='flex flex-wrap items-center gap-2'>
            <PeriodControl
              days={days}
              onChange={(d) =>
                patchSearch({
                  days: d,
                  granularity: defaultGranularity(d),
                })
              }
              disabled={exportDisabled}
            />
            <Button variant='outline' size='sm' onClick={retry}>
              <RefreshCw className='me-2 size-4' aria-hidden='true' />
              {t('Refresh')}
            </Button>
          </div>
        }
      />

      <Tabs
        value={tab}
        onValueChange={(v) =>
          patchSearch({ tab: v === 'explore' ? 'explore' : 'overview' })
        }
        className='gap-3 sm:gap-4'
      >
        <TabsList aria-label={t('Activity sections')} className='max-w-full justify-start'>
          <TabsTrigger value='overview' data-testid='activity-tab-overview'>
            <ActivityIcon aria-hidden='true' />
            {t('Overview')}
          </TabsTrigger>
          <TabsTrigger value='explore' data-testid='activity-tab-explore'>
            {t('Explore')}
          </TabsTrigger>
        </TabsList>

        {tab === 'overview' ? (
          <OverviewTab
            loading={loading || status.isPending}
            failed={failed || exportUnknown}
            exportDisabled={exportDisabled}
            usage={usage}
            start={start}
            end={end}
            stat={stat.data}
            statFailed={stat.isError}
            money={money}
            isTenantAdmin={access.isTenantAdmin}
            onRetry={retry}
          />
        ) : (
          <ExploreTab
            loading={loading || status.isPending}
            failed={failed || exportUnknown}
            exportDisabled={exportDisabled}
            usage={usage}
            start={start}
            end={end}
            metric={metric}
            granularity={granularity}
            money={money}
            onMetricChange={(m) => patchSearch({ metric: m })}
            onGranularityChange={(g) => patchSearch({ granularity: g })}
            onRetry={retry}
          />
        )}
      </Tabs>
    </>
  )
}
