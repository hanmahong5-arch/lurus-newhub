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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  XAxis,
  YAxis,
} from 'recharts'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart'
import { useModelLabel } from '@/features/dashboard/lib/model-name'
import type { Usage } from '@/features/dashboard/lib/series'
import type { LogStat } from '@/features/usage-logs/lib/logs'
import { formatQuota, type MoneyConfig } from '@/lib/money'
import { cn } from '@/lib/utils'

import { rankModels } from '../lib/explore'
import { usageLogsSearchFromDrilldown } from '../lib/search'

import { KpiRow } from './kpi-row'

interface OverviewTabProps {
  loading: boolean
  failed: boolean
  exportDisabled: boolean
  usage: Usage
  start: number
  end: number
  stat: LogStat | undefined
  statFailed: boolean
  money: MoneyConfig | null
  isTenantAdmin: boolean
  onRetry: () => void
}

export function OverviewTab(props: OverviewTabProps) {
  const { t } = useTranslation()
  const label = useModelLabel()
  const rows = useMemo(
    () => rankModels(props.usage, 'quota', 8),
    [props.usage]
  )
  const trend = useMemo(
    () =>
      props.usage.buckets.map((b) => ({
        ts: b.ts,
        label: new Date(b.ts * 1000).toLocaleDateString(undefined, {
          month: 'short',
          day: 'numeric',
        }),
        quota: b.quota,
        count: b.count,
      })),
    [props.usage.buckets]
  )

  const shareConfig: ChartConfig = {
    quota: { label: t('Spend'), color: 'var(--chart-1)' },
  }
  const trendConfig: ChartConfig = {
    quota: { label: t('Spend'), color: 'var(--chart-2)' },
  }

  if (props.exportDisabled) {
    return (
      <EmptyState
        title={t('Usage export is off')}
        description={t(
          'The operator has disabled data export, so Activity has nothing to chart. Usage logs still work.'
        )}
        action={
          <Button render={<Link to='/usage-logs' />}>{t('Open usage logs')}</Button>
        }
      />
    )
  }

  if (props.failed) {
    return (
      <ErrorState
        title={t('Failed to load activity')}
        description={t('The usage series could not be loaded.')}
        onRetry={props.onRetry}
      />
    )
  }

  const empty =
    !props.loading &&
    props.usage.totals.count === 0 &&
    props.usage.totals.quota === 0

  return (
    <div className='flex flex-col gap-4' data-testid='activity-overview'>
      <KpiRow
        loading={props.loading}
        failed={props.statFailed}
        totals={props.usage.totals}
        stat={props.stat}
        money={props.money}
      />

      {empty ? (
        <EmptyState
          title={t('No usage in this period')}
          description={t(
            'Make a few API calls, then refresh. Explore can slice spend by model once traffic lands.'
          )}
        />
      ) : (
        <div className='grid gap-4 lg:grid-cols-2'>
          <section
            className='bg-card rounded-xl border p-4'
            data-testid='activity-model-share'
          >
            <header className='mb-3'>
              <h2 className='text-sm font-medium'>{t('Spend by model')}</h2>
              <p className='text-muted-foreground text-xs'>
                {t('Top models in the selected period')}
              </p>
            </header>
            {props.loading ? (
              <div className='bg-muted/40 h-56 animate-pulse rounded-lg' />
            ) : (
              <ChartContainer config={shareConfig} className='aspect-auto h-56 w-full'>
                <BarChart
                  data={rows.map((r) => ({
                    name: label(r.model),
                    model: r.model,
                    quota: r.quota,
                  }))}
                  layout='vertical'
                  margin={{ left: 8, right: 8, top: 4, bottom: 4 }}
                >
                  <CartesianGrid horizontal={false} />
                  <XAxis type='number' hide />
                  <YAxis
                    type='category'
                    dataKey='name'
                    width={96}
                    tickLine={false}
                    axisLine={false}
                    className='text-xs'
                  />
                  <ChartTooltip
                    content={
                      <ChartTooltipContent
                        formatter={(v) => formatQuota(Number(v), props.money)}
                      />
                    }
                  />
                  <Bar
                    dataKey='quota'
                    fill='var(--color-quota)'
                    radius={4}
                    cursor='pointer'
                    onClick={(data) => {
                      const model =
                        data &&
                        typeof data === 'object' &&
                        'model' in data &&
                        typeof (data as { model?: unknown }).model === 'string'
                          ? (data as { model: string }).model
                          : ''
                      // Navigation is handled by the row links below; bar click
                      // is a nicety via the table.
                      void model
                    }}
                  />
                </BarChart>
              </ChartContainer>
            )}
            <ul className='mt-3 space-y-1'>
              {rows.map((r) => (
                <li key={r.model || '__blank'}>
                  <Link
                    to='/usage-logs'
                    search={usageLogsSearchFromDrilldown({
                      model: r.model,
                      startSec: props.start,
                      endSec: props.end,
                    })}
                    className={cn(
                      'hover:bg-muted/60 flex items-center justify-between rounded-md px-2 py-1.5 text-sm'
                    )}
                    data-testid={`activity-model-link-${r.model || 'unknown'}`}
                  >
                    <span className='truncate'>{label(r.model)}</span>
                    <span className='text-muted-foreground tabular-nums'>
                      {formatQuota(r.quota, props.money)}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          </section>

          <section
            className='bg-card rounded-xl border p-4'
            data-testid='activity-trend'
          >
            <header className='mb-3'>
              <h2 className='text-sm font-medium'>{t('Spend trend')}</h2>
              <p className='text-muted-foreground text-xs'>
                {t('Quota over the selected period')}
              </p>
            </header>
            {props.loading ? (
              <div className='bg-muted/40 h-56 animate-pulse rounded-lg' />
            ) : (
              <ChartContainer config={trendConfig} className='aspect-auto h-56 w-full'>
                <AreaChart data={trend} margin={{ left: 4, right: 4, top: 4 }}>
                  <CartesianGrid vertical={false} />
                  <XAxis
                    dataKey='label'
                    tickLine={false}
                    axisLine={false}
                    className='text-xs'
                  />
                  <YAxis hide />
                  <ChartTooltip
                    content={
                      <ChartTooltipContent
                        formatter={(v) => formatQuota(Number(v), props.money)}
                      />
                    }
                  />
                  <Area
                    type='monotone'
                    dataKey='quota'
                    stroke='var(--color-quota)'
                    fill='var(--color-quota)'
                    fillOpacity={0.2}
                  />
                </AreaChart>
              </ChartContainer>
            )}
            {props.isTenantAdmin && (
              <p className='text-muted-foreground mt-3 text-xs'>
                {t('Need key or member rankings?')}{' '}
                <Link to='/rankings' className='text-primary underline-offset-2 hover:underline'>
                  {t('Open Rankings')}
                </Link>
              </p>
            )}
          </section>
        </div>
      )}
    </div>
  )
}
