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
import {
  Activity,
  AlertTriangle,
  Coins,
  Gauge,
  KeyRound,
  Receipt,
  Timer,
  TrendingUp,
  Wallet,
  type LucideIcon,
} from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from 'recharts'

import { Button } from '@/components/ui/button'
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart'
import { IconBadge, type IconBadgeTone } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { currentLocale } from '@/lib/locale'
import { formatQuota, type MoneyConfig } from '@/lib/money'
import type { CurrentUser } from '@/lib/user'
import { cn } from '@/lib/utils'

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
  type DailyPoint,
} from '../lib/kpis'
import { isBlankModel, useModelLabel } from '../lib/model-name'
import { formatRemaining, type LoadState } from '../lib/state'
import type { RealtimeLogs, TrendData } from '../lib/types'

import { Panel, Segmented } from './panel'

export function LoadFailed(props: { message: string; onRetry?: () => void }) {
  const { t } = useTranslation()
  return (
    <div
      role='alert'
      data-testid='dashboard-load-failed'
      className='text-destructive flex items-center justify-between gap-3 text-sm'
    >
      <span className='flex items-center gap-2'>
        <AlertTriangle className='size-4 shrink-0' aria-hidden='true' />
        {props.message}
      </span>
      {props.onRetry != null && (
        <Button variant='outline' size='sm' onClick={props.onRetry}>
          {t('Retry')}
        </Button>
      )}
    </div>
  )
}

function Sparkline(props: { values: number[]; color: string }) {
  if (props.values.length < 2 || props.values.every((v) => v === 0)) {
    return null
  }
  const config: ChartConfig = { v: { color: props.color } }
  const data = props.values.map((v, i) => ({ i, v }))
  return (
    <ChartContainer
      config={config}
      className='pointer-events-none mt-2 aspect-auto h-8 w-full'
      aria-hidden='true'
    >
      <AreaChart data={data} margin={{ top: 2, right: 0, bottom: 0, left: 0 }}>
        <Area
          dataKey='v'
          type='monotone'
          stroke='var(--color-v)'
          fill='var(--color-v)'
          fillOpacity={0.15}
          strokeWidth={1.5}
          dot={false}
          isAnimationActive={false}
        />
      </AreaChart>
    </ChartContainer>
  )
}

function Stat(props: {
  label: string
  value: string
  state: LoadState
  foot?: ReactNode
  testId: string
  icon: LucideIcon
  tone: IconBadgeTone
  spark?: number[]
  sparkColor?: string
}) {
  const { t } = useTranslation()
  const Icon = props.icon
  return (
    <div
      data-testid={props.testId}
      className='bg-card flex min-w-0 flex-col px-3 py-3 sm:px-5 sm:py-4'
    >
      <div className='flex min-w-0 items-center gap-2'>
        <IconBadge tone={props.tone} size='stat'>
          <Icon />
        </IconBadge>
        <div className='text-muted-foreground truncate text-[11px] leading-4 font-medium tracking-wide uppercase sm:text-xs'>
          {props.label}
        </div>
      </div>
      {props.state === 'loading' && <Skeleton className='mt-2 h-7 w-24' />}
      {props.state === 'error' && (
        <div className='text-destructive mt-2 text-sm'>
          {t('Unable to load')}
        </div>
      )}
      {props.state === 'ok' && (
        <div
          className='mt-1 max-w-full truncate font-mono text-lg font-bold tracking-tight tabular-nums sm:mt-2 sm:text-2xl'
          title={props.value}
        >
          {props.value}
        </div>
      )}
      {props.state === 'ok' && props.foot != null && (
        <div className='text-muted-foreground mt-1 text-xs'>{props.foot}</div>
      )}
      {props.state === 'ok' && props.spark != null && (
        <Sparkline
          values={props.spark}
          color={props.sparkColor ?? 'var(--chart-1)'}
        />
      )}
    </div>
  )
}

function DeltaBadge(props: { pct: number | null }) {
  const { t } = useTranslation()
  if (props.pct == null) return null
  const up = props.pct >= 0
  return (
    <span
      data-testid='dashboard-delta'
      className={cn(up ? 'text-success' : 'text-destructive')}
    >
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
  const series: DailyPoint[] = trend
    ? dailySeries(trend.rows, trend.end, TREND_DAYS)
    : []
  const spendDelta = trend ? periodDelta(series, 'quota') : null
  const reqDelta = trend ? periodDelta(series, 'count') : null
  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='bg-border grid grid-cols-2 gap-px lg:grid-cols-4'>
        <Stat
          testId='kpi-balance'
          label={t('Remaining balance')}
          state={meState}
          icon={Wallet}
          tone='success'
          value={formatRemaining(me?.remaining_quota, money)}
        />
        <Stat
          testId='kpi-spent'
          label={t('Total spent')}
          state={meState}
          icon={Coins}
          tone='warning'
          value={formatQuota(me?.used_quota, money)}
          foot={<DeltaBadge pct={spendDelta} />}
          spark={trend ? series.map((p) => p.quota) : undefined}
          sparkColor='var(--chart-3)'
        />
        <Stat
          testId='kpi-requests'
          label={t('Requests')}
          state={meState}
          icon={Activity}
          tone='info'
          value={
            typeof me?.request_count === 'number'
              ? me.request_count.toLocaleString(currentLocale())
              : '--'
          }
          foot={<DeltaBadge pct={reqDelta} />}
          spark={trend ? series.map((p) => p.count) : undefined}
          sparkColor='var(--chart-2)'
        />
        <Stat
          testId='kpi-keys'
          label={t('API Keys')}
          state={meState}
          icon={KeyRound}
          tone='chart-4'
          value={
            typeof me?.token_count === 'number' ? String(me.token_count) : '--'
          }
        />
      </div>
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
    <Panel
      testId='dashboard-live'
      title={t('Last 5 minutes')}
      icon={<Gauge />}
      tone='info'
      loadingHeight={state === 'loading' ? 'h-12' : undefined}
    >
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
                <div className='font-mono text-lg font-semibold tabular-nums'>
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
    </Panel>
  )
}

export interface TrendPanelProps {
  state: LoadState
  data: TrendData | undefined
  money: MoneyConfig | null
  onRetry?: () => void
}

type Metric = 'quota' | 'count'

/** Daily spend / request trend over the last TREND_DAYS days. */
export function TrendPanel(props: TrendPanelProps) {
  const { t } = useTranslation()
  const [metric, setMetric] = useState<Metric>('quota')
  const { state, data, money } = props
  const series = data ? dailySeries(data.rows, data.end, TREND_DAYS) : []
  // Plotted in raw quota (or raw count); ticks and tooltips go through
  // formatQuota, so the unit price is never re-derived here.
  const points = series.map((p) => ({
    label: new Date(p.day * 1000).toLocaleDateString(currentLocale(), {
      month: '2-digit',
      day: '2-digit',
    }),
    value: metric === 'quota' ? p.quota : p.count,
  }))
  const fmt = (v: number, digits = 4) =>
    metric === 'quota'
      ? formatQuota(v, money, digits)
      : v.toLocaleString(currentLocale())
  const config: ChartConfig = {
    value: {
      label: metric === 'quota' ? t('Spend') : t('Requests'),
      color: 'var(--chart-1)',
    },
  }
  const empty = state === 'ok' && (data?.rows.length ?? 0) === 0
  return (
    <Panel
      testId='dashboard-trend'
      title={t('Usage trend')}
      description={t('last {{days}} days', { days: TREND_DAYS })}
      icon={<TrendingUp />}
      tone='chart-1'
      loadingHeight={state === 'loading' ? 'h-56' : undefined}
      actions={
        <Segmented<Metric>
          label={t('Metric')}
          value={metric}
          onChange={setMetric}
          options={[
            { value: 'quota', label: t('Spend') },
            { value: 'count', label: t('Requests') },
          ]}
        />
      }
    >
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
        <ChartContainer config={config} className='aspect-auto h-56 w-full'>
          <AreaChart data={points} margin={{ left: 4, right: 8, top: 8 }}>
            <defs>
              <linearGradient
                id='dashboard-trend-fill'
                x1='0'
                y1='0'
                x2='0'
                y2='1'
              >
                <stop
                  offset='5%'
                  stopColor='var(--color-value)'
                  stopOpacity={0.35}
                />
                <stop
                  offset='95%'
                  stopColor='var(--color-value)'
                  stopOpacity={0.02}
                />
              </linearGradient>
            </defs>
            <CartesianGrid vertical={false} />
            <XAxis
              dataKey='label'
              tickLine={false}
              axisLine={false}
              minTickGap={16}
            />
            <YAxis
              tickLine={false}
              axisLine={false}
              width={64}
              tickFormatter={(v: number) => fmt(v, 2)}
              allowDecimals={false}
            />
            <ChartTooltip
              content={
                <ChartTooltipContent formatter={(v) => fmt(Number(v))} />
              }
            />
            <Area
              dataKey='value'
              type='monotone'
              stroke='var(--color-value)'
              fill='url(#dashboard-trend-fill)'
              strokeWidth={2}
            />
          </AreaChart>
        </ChartContainer>
      )}
    </Panel>
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
  const max = Math.max(1, ...rows.map((r) => r.quota))
  const modelLabel = useModelLabel()
  return (
    <Panel
      testId='dashboard-distribution'
      title={t('Spend by model')}
      icon={<Receipt />}
      tone='chart-4'
      loadingHeight={state === 'loading' ? 'h-40' : undefined}
    >
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
                <span
                  className={cn(
                    'truncate',
                    isBlankModel(r.model) ? 'text-muted-foreground' : 'font-mono'
                  )}
                >
                  {modelLabel(r.model)}
                </span>
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
    </Panel>
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
  const modelLabel = useModelLabel()
  return (
    <Panel
      testId='dashboard-recent-cost'
      title={t('Cost by model, last 5 minutes')}
      icon={<Timer />}
      tone='warning'
      loadingHeight={state === 'loading' ? 'h-24' : undefined}
    >
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
              <span
                className={cn(
                  'truncate',
                  isBlankModel(r.model) ? 'text-muted-foreground' : 'font-mono'
                )}
              >
                {modelLabel(r.model)}
              </span>
              <span className='text-muted-foreground tabular-nums'>
                {formatQuota(r.totalQuota, money, 4)} ·{' '}
                {t('{{n}} req', { n: r.requestCount })}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  )
}
