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
import { useTranslation } from 'react-i18next'

import { PageHeader } from '@/components/layout/page-header'
import { Button } from '@/components/ui/button'
import { useMoneyConfig, statusQueryOptions } from '@/lib/status'
import { currentUserQueryOptions } from '@/lib/user'

import {
  DistributionPanel,
  KpiCards,
  LivePanel,
  RecentCostPanel,
  TrendPanel,
} from './components/panels'
import { fetchRealtimeLogs, fetchTrend } from './lib/api'
import { loadState, type LoadState } from './lib/state'

/**
 * Dashboard: account KPIs (user/me), realtime 5-minute window (logs), and the
 * 29-day trend / model split (/api/data/self/). Each read reports its own
 * failure; a failed read is never rendered as an empty result.
 */
export function DashboardPage() {
  const { t } = useTranslation()
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

  // When data export is off the server never records quota_data, so the
  // trend panels would claim "no usage" about data it is not collecting:
  // hide them instead. If /api/status itself failed we cannot tell, so the
  // panels show the error rather than silently vanishing.
  const trendState: LoadState = status.isError
    ? 'error'
    : loadState(status.isPending ? status : trend)

  const refreshAll = () => {
    void me.refetch()
    void live.refetch()
    void status.refetch()
    if (trendEnabled) void trend.refetch()
  }

  const showTrend = status.isPending || status.isError || trendEnabled

  return (
    <>
      <PageHeader
        title={t('Dashboard')}
        actions={
          <Button variant='outline' size='sm' onClick={refreshAll}>
            {t('Refresh')}
          </Button>
        }
      />
      <div className='flex flex-col gap-4'>
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
        <div className='grid gap-4 lg:grid-cols-3'>
          {showTrend && (
            <div className='lg:col-span-2'>
              <TrendPanel
                state={trendState}
                data={trend.data}
                money={money}
                onRetry={refreshAll}
              />
            </div>
          )}
          {showTrend && (
            <DistributionPanel
              state={trendState}
              data={trend.data}
              money={money}
              onRetry={refreshAll}
            />
          )}
          <div className='lg:col-span-3'>
            <RecentCostPanel
              state={loadState(live)}
              data={live.data}
              money={money}
              onRetry={() => void live.refetch()}
            />
          </div>
        </div>
      </div>
    </>
  )
}
