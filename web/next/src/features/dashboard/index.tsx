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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { LayoutDashboard, PieChart, RefreshCw } from 'lucide-react'
import { lazy, Suspense, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { PageHeader } from '@/components/layout/page-header'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useMoneyConfig, statusQueryOptions } from '@/lib/status'
import { currentUserQueryOptions, userAccess } from '@/lib/user'

import { AnnouncementsPanel, FaqPanel, NoticePanel } from './components/content-panels'
import {
  DistributionPanel,
  KpiCards,
  LivePanel,
  RecentCostPanel,
  TrendPanel,
} from './components/panels'
import { RankingsPanel } from './components/rankings-panel'
import { UptimePanel } from './components/uptime-panel'
import { fetchRealtimeLogs, fetchTrend } from './lib/api'
import { useConsolePanels } from './lib/status-data'
import { loadState, type LoadState } from './lib/state'

// The models section pulls in the chart code; keep it off the first paint.
const ModelsTab = lazy(() =>
  import('./components/models/models-tab').then((m) => ({
    default: m.ModelsTab,
  }))
)

type Section = 'overview' | 'models'

function ModelsFallback() {
  return (
    <div className='flex flex-col gap-3' aria-hidden='true'>
      <Skeleton className='h-20 w-full' />
      <Skeleton className='h-28 w-full' />
      <Skeleton className='h-96 w-full' />
    </div>
  )
}

/**
 * Overview section: account KPIs (user/me), realtime 5-minute window (logs),
 * and the 29-day trend / model split (/api/data/self/). Each read reports its
 * own failure; a failed read is never rendered as an empty result.
 */
function OverviewSection() {
  const money = useMoneyConfig()
  const me = useQuery(currentUserQueryOptions)
  const live = useQuery({
    queryKey: ['dashboard', 'realtime-logs'],
    queryFn: () => fetchRealtimeLogs(),
    retry: false,
    refetchInterval: 30_000,
  })
  const status = useQuery(statusQueryOptions)
  const trendEnabled = status.data?.enable_data_export === true
  const trend = useQuery({
    queryKey: ['dashboard', 'trend'],
    queryFn: () => fetchTrend(),
    retry: false,
    enabled: trendEnabled,
  })
  const panels = useConsolePanels()

  // When data export is off the server never records quota_data, so the
  // trend panels would claim "no usage" about data it is not collecting:
  // hide them instead. If /api/status itself failed we cannot tell, so the
  // panels show the error rather than silently vanishing.
  const trendState: LoadState = status.isError
    ? 'error'
    : loadState(status.isPending ? status : trend)
  const retryTrend = () => {
    void status.refetch()
    if (trendEnabled) void trend.refetch()
  }

  const access = userAccess(me.data)
  const showRankings = access.isTenantAdmin || access.isDeptLead
  const showTrend = status.isPending || status.isError || trendEnabled
  const showContent =
    panels.loading || panels.announcementsEnabled || panels.faqEnabled

  return (
    <div className='flex flex-col gap-4'>
      <NoticePanel />
      <KpiCards
        me={me.data}
        meState={loadState(me)}
        money={money}
        trend={trend.data}
      />
      <LivePanel
        state={loadState(live)}
        data={live.data}
        onRetry={() => void live.refetch()}
      />
      <UptimePanel />
      {showRankings && <RankingsPanel />}
      <div className='grid gap-4 lg:grid-cols-3'>
        {showTrend && (
          <div className='min-w-0 lg:col-span-2'>
            <TrendPanel
              state={trendState}
              data={trend.data}
              money={money}
              onRetry={retryTrend}
            />
          </div>
        )}
        {showTrend && (
          <DistributionPanel
            state={trendState}
            data={trend.data}
            money={money}
            onRetry={retryTrend}
          />
        )}
        <div className='min-w-0 lg:col-span-3'>
          <RecentCostPanel
            state={loadState(live)}
            data={live.data}
            money={money}
            onRetry={() => void live.refetch()}
          />
        </div>
      </div>
      {showContent && (
        <div className='grid gap-4 lg:grid-cols-2'>
          <AnnouncementsPanel />
          <FaqPanel />
        </div>
      )}
    </div>
  )
}

export function DashboardPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [section, setSection] = useState<Section>('overview')

  const refreshAll = () => {
    // Page reads key under 'dashboard'; the account and status reads are
    // shared with the rest of the console, so refresh them by their own keys.
    void queryClient.invalidateQueries({ queryKey: ['dashboard'] })
    void queryClient.invalidateQueries({
      queryKey: currentUserQueryOptions.queryKey,
    })
    void queryClient.invalidateQueries({ queryKey: statusQueryOptions.queryKey })
  }

  return (
    <>
      <PageHeader
        title={t('Dashboard')}
        actions={
          <Button variant='outline' size='sm' onClick={refreshAll}>
            <RefreshCw className='me-2 size-4' aria-hidden='true' />
            {t('Refresh')}
          </Button>
        }
      />
      <Tabs
        value={section}
        onValueChange={(v) => setSection(v as Section)}
        className='gap-3 sm:gap-4'
      >
        <TabsList
          aria-label={t('Dashboard sections')}
          className='max-w-full justify-start'
        >
          <TabsTrigger value='overview' data-testid='dashboard-tab-overview'>
            <LayoutDashboard aria-hidden='true' />
            {t('Overview')}
          </TabsTrigger>
          <TabsTrigger value='models' data-testid='dashboard-tab-models'>
            <PieChart aria-hidden='true' />
            {t('Model Call Analytics')}
          </TabsTrigger>
        </TabsList>
        {section === 'overview' ? (
          <OverviewSection />
        ) : (
          <Suspense fallback={<ModelsFallback />}>
            <ModelsTab />
          </Suspense>
        )}
      </Tabs>
    </>
  )
}
