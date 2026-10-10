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
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from 'recharts'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart'
import { Skeleton } from '@/components/ui/skeleton'
import { currentLocale } from '@/lib/locale'
import { formatQuota, type MoneyConfig } from '@/lib/money'
import type { CurrentUser } from '@/lib/user'

import {
  costByModel,
  dailySeries,
  errorRate,
  formatErrorRate,
  formatLatencyMs,
  formatQps,
  latencyPercentile,
  modelDistribution,
  periodDelta,
  qps,
  TREND_DAYS,
} from '../lib/kpis'
import { formatRemaining, type LoadState } from '../lib/state'
import type { RealtimeLogs, TrendData } from '../lib/types'

export function LoadFailed(props: { message: string; onRetry?: () => void }) {
  const { t } = useTranslation()
  return (
    <div
      role='alert'
      data-testid='dashboard-load-failed'
      className='text-destructive flex items-center justify-between gap-3 text-sm'
    >
      <span>{props.message}</span>
      {props.onRetry != null && (
        <Button variant='outline' size='sm' onClick={props.onRetry}>
          {t('Retry')}
        </Button>
      )}
    </div>
  )
}

function Stat(props: {
  label: string
  value: string
  state: LoadState
  foot?: ReactNode
  testId: string
}) {
  const { t } = useTranslation()
  return (
    <Card size='sm' data-testid={props.testId}>
      <CardHeader>
        <CardTitle className='text-muted-foreground text-sm font-normal'>
          {props.label}
        </CardTitle>
      </CardHeader>
      <CardContent>
        {props.state === 'loading' && <Skeleton className='h-8 w-24' />}
        {props.state === 'error' && (
          <div className='text-destructive text-sm'>{t('Unable to load')}</div>
        )}
        {props.state === 'ok' && (
          <div className='text-2xl font-semibold tabular-nums'>
            {props.value}
          </div>
        )}
        {props.state === 'ok' && props.foot != null && (
          <div className='text-muted-foreground mt-1 text-xs'>{props.foot}</div>
        )}
      </CardContent>
    </Card>
  )
}

function DeltaBadge(props: { pct: number | null }) {
  const { t } = useTranslation()
  if (props.pct == null) return null
  const up = props.pct >= 0
  return (
    <span data-testid='dashboard-delta'>
      {up ? '↑ +' : '↓ '}
      {props.pct.toFixed(0)}% {t('vs previous 7 days')}
    </span>
  )
}

export interface KpiCardsProps {
  me: CurrentUser | undefined
  meState: LoadState
  money: MoneyConfig | null
  trend: TrendData | undefined
}

/** Balance / spend / requests / keys, from user/me plus 7-day deltas. */
export function KpiCards(props: KpiCardsProps) {
  const { t } = useTranslation()
  const { me, meState, money, trend } = props
  const series = trend ? dailySeries(trend.rows, trend.end, TREND_DAYS) : []
  const spendDelta = trend ? periodDelta(series, 'quota') : null
  const reqDelta = trend ? periodDelta(series, 'count') : null
  return (
    <div className='grid gap-4 sm:grid-cols-2 xl:grid-cols-4'>
      <Stat
        testId='kpi-balance'
        label={t('Remaining balance')}
        state={meState}
        value={formatRemaining(me?.remaining_quota, money)}
      />
      <Stat
        testId='kpi-spent'
        label={t('Total spent')}
        state={meState}
        value={formatQuota(me?.used_quota, money)}
        foot={<DeltaBadge pct={spendDelta} />}
      />
      <Stat
        testId='kpi-requests'
        label={t('Requests')}
        state={meState}
        value={
          typeof me?.request_count === 'number'
            ? me.request_count.toLocaleString(currentLocale())
            : '--'
        }
        foot={<DeltaBadge pct={reqDelta} />}
      />
      <Stat
        testId='kpi-keys'
        label={t('API Keys')}
        state={meState}
        value={
          typeof me?.token_count === 'number' ? String(me.token_count) : '--'
        }
      />
    </div>
  )
}

export interface LivePanelProps {
  state: LoadState
  data: RealtimeLogs | undefined
  onRetry?: () => void
}

/** Rate / latency / error KPIs over the last 5 minutes of logs. */
export function LivePanel(props: LivePanelProps) {
  const { t } = useTranslation()
  const { state, data } = props
  const logs = data?.logs ?? []
  const idle = state === 'ok' && logs.length === 0
  const cells: Array<[string, string]> =
    state === 'ok'
      ? [
          [t('Requests / sec'), formatQps(qps(data?.total ?? null, logs.length))],
          ['P50', formatLatencyMs(latencyPercentile(logs, 50))],
          ['P95', formatLatencyMs(latencyPercentile(logs, 95))],
          ['P99', formatLatencyMs(latencyPercentile(logs, 99))],
          [t('Error rate'), formatErrorRate(errorRate(logs))],
        ]
      : []
  return (
    <Card size='sm' data-testid='dashboard-live'>
      <CardHeader>
        <CardTitle>{t('Last 5 minutes')}</CardTitle>
      </CardHeader>
      <CardContent>
        {state === 'loading' && <Skeleton className='h-12 w-full' />}
        {state === 'error' && (
          <LoadFailed
            message={t('Could not load recent requests.')}
            onRetry={props.onRetry}
          />
        )}
        {state === 'ok' && (
          <>
            <div className='grid grid-cols-2 gap-4 sm:grid-cols-5'>
              {cells.map(([label, value]) => (
                <div key={label}>
                  <div className='text-muted-foreground text-xs'>{label}</div>
                  <div className='text-lg font-semibold tabular-nums'>
                    {value}
                  </div>
                </div>
              ))}
            </div>
            {idle && (
              <div
                data-testid='dashboard-live-idle'
                className='text-muted-foreground mt-3 text-xs'
              >
                {t('No requests in the last 5 minutes.')}
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}

export interface TrendPanelProps {
  state: LoadState
  data: TrendData | undefined
  money: MoneyConfig | null
  onRetry?: () => void
}

type Metric = 'quota' | 'count'

function chartValue(
  metric: Metric,
  p: { quota: number; count: number },
  money: MoneyConfig | null
): number {
  if (metric === 'count') return p.count
  if (money != null && money.displayType !== 'TOKENS') {
    return (p.quota / money.quotaPerUnit) * money.rate
  }
  return p.quota
}

/** Daily spend / request trend over the last TREND_DAYS days. */
export function TrendPanel(props: TrendPanelProps) {
  const { t } = useTranslation()
  const [metric, setMetric] = useState<Metric>('quota')
  const { state, data, money } = props
  const series = data ? dailySeries(data.rows, data.end, TREND_DAYS) : []
  const points = series.map((p) => ({
    label: new Date(p.day * 1000).toLocaleDateString(currentLocale(), {
      month: '2-digit',
      day: '2-digit',
    }),
    value: chartValue(metric, p, money),
    quota: p.quota,
  }))
  const config: ChartConfig = {
    value: {
      label: metric === 'quota' ? t('Spend') : t('Requests'),
      color: 'var(--chart-1)',
    },
  }
  const empty = state === 'ok' && (data?.rows.length ?? 0) === 0
  return (
    <Card size='sm' data-testid='dashboard-trend'>
      <CardHeader>
        <CardTitle>
          {t('Usage trend')} ·{' '}
          <span className='text-muted-foreground font-normal'>
            {t('last {{days}} days', { days: TREND_DAYS })}
          </span>
        </CardTitle>
        <div className='flex gap-2'>
          <Button
            size='sm'
            variant={metric === 'quota' ? 'default' : 'outline'}
            onClick={() => setMetric('quota')}
          >
            {t('Spend')}
          </Button>
          <Button
            size='sm'
            variant={metric === 'count' ? 'default' : 'outline'}
            onClick={() => setMetric('count')}
          >
            {t('Requests')}
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {state === 'loading' && <Skeleton className='h-56 w-full' />}
        {state === 'error' && (
          <LoadFailed
            message={t('Could not load usage data.')}
            onRetry={props.onRetry}
          />
        )}
        {empty && (
          <div
            data-testid='dashboard-trend-empty'
            className='text-muted-foreground py-10 text-center text-sm'
          >
            {t('No usage recorded yet.')}
          </div>
        )}
        {state === 'ok' && !empty && (
          <ChartContainer config={config} className='h-56 w-full'>
            <AreaChart data={points}>
              <CartesianGrid vertical={false} />
              <XAxis dataKey='label' tickLine={false} axisLine={false} />
              <YAxis hide />
              <ChartTooltip
                content={
                  <ChartTooltipContent
                    formatter={(v, _n, item) => {
                      const raw = (item?.payload as { quota?: number })?.quota
                      return metric === 'quota'
                        ? formatQuota(raw, money, 4)
                        : String(v)
                    }}
                  />
                }
              />
              <Area
                dataKey='value'
                type='monotone'
                stroke='var(--color-value)'
                fill='var(--color-value)'
                fillOpacity={0.2}
              />
            </AreaChart>
          </ChartContainer>
        )}
      </CardContent>
    </Card>
  )
}

export interface DistributionPanelProps {
  state: LoadState
  data: TrendData | undefined
  money: MoneyConfig | null
  onRetry?: () => void
}

/** Spend share per model over the trend window. */
export function DistributionPanel(props: DistributionPanelProps) {
  const { t } = useTranslation()
  const { state, data, money } = props
  const rows = data ? modelDistribution(data.rows) : []
  const max = rows[0]?.quota || 1
  return (
    <Card size='sm' data-testid='dashboard-distribution'>
      <CardHeader>
        <CardTitle>{t('Spend by model')}</CardTitle>
      </CardHeader>
      <CardContent>
        {state === 'loading' && <Skeleton className='h-40 w-full' />}
        {state === 'error' && (
          <LoadFailed
            message={t('Could not load usage data.')}
            onRetry={props.onRetry}
          />
        )}
        {state === 'ok' && rows.length === 0 && (
          <div
            data-testid='dashboard-distribution-empty'
            className='text-muted-foreground py-6 text-center text-sm'
          >
            {t('No usage recorded yet.')}
          </div>
        )}
        {state === 'ok' && rows.length > 0 && (
          <ul className='flex flex-col gap-3'>
            {rows.map((r) => (
              <li key={r.model} data-testid='distribution-row'>
                <div className='mb-1 flex justify-between gap-2 text-xs'>
                  <span className='truncate font-mono'>{r.model}</span>
                  <span className='text-muted-foreground tabular-nums'>
                    {formatQuota(r.quota, money)}
                  </span>
                </div>
                <div className='bg-muted h-1.5 overflow-hidden rounded'>
                  <div
                    className='bg-primary h-full'
                    style={{ width: `${(r.quota / max) * 100}%` }}
                  />
                </div>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

export interface RecentCostPanelProps {
  state: LoadState
  data: RealtimeLogs | undefined
  money: MoneyConfig | null
  onRetry?: () => void
}

/** Spend per model over the last 5 minutes (derived from the logs page). */
export function RecentCostPanel(props: RecentCostPanelProps) {
  const { t } = useTranslation()
  const { state, data, money } = props
  const rows = data ? costByModel(data.logs).slice(0, 6) : []
  return (
    <Card size='sm' data-testid='dashboard-recent-cost'>
      <CardHeader>
        <CardTitle>{t('Cost by model, last 5 minutes')}</CardTitle>
      </CardHeader>
      <CardContent>
        {state === 'loading' && <Skeleton className='h-24 w-full' />}
        {state === 'error' && (
          <LoadFailed
            message={t('Could not load recent requests.')}
            onRetry={props.onRetry}
          />
        )}
        {state === 'ok' && rows.length === 0 && (
          <div
            data-testid='dashboard-recent-cost-empty'
            className='text-muted-foreground py-4 text-center text-sm'
          >
            {t('No spend in the last 5 minutes.')}
          </div>
        )}
        {state === 'ok' && rows.length > 0 && (
          <ul className='flex flex-col gap-2 text-sm'>
            {rows.map((r) => (
              <li key={r.model} className='flex justify-between gap-2'>
                <span className='truncate font-mono'>{r.model}</span>
                <span className='text-muted-foreground tabular-nums'>
                  {formatQuota(r.totalQuota, money, 4)} ·{' '}
                  {t('{{n}} req', { n: r.requestCount })}
                </span>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}
