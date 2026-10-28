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
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  XAxis,
  YAxis,
} from 'recharts'

import { formatCompactNumber, formatNumber } from '@/components/format'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from '@/components/ui/chart'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useModelLabel } from '@/features/dashboard/lib/model-name'
import {
  buildSeriesConfig,
  useSeriesName,
} from '@/features/dashboard/lib/series-style'
import type { Usage, UsageMetric } from '@/features/dashboard/lib/series'
import { currentLocale } from '@/lib/locale'
import { formatQuota, type MoneyConfig } from '@/lib/money'

import { exploreChart, rankModels } from '../lib/explore'
import { usageLogsSearchFromDrilldown } from '../lib/links'
import type { ActivityGranularity } from '../lib/search'

interface ExploreTabProps {
  loading: boolean
  failed: boolean
  exportDisabled: boolean
  usage: Usage
  start: number
  end: number
  metric: UsageMetric
  granularity: ActivityGranularity
  money: MoneyConfig | null
  onMetricChange: (m: UsageMetric) => void
  onGranularityChange: (g: ActivityGranularity) => void
  onRetry: () => void
}

export function ExploreTab(props: ExploreTabProps) {
  const { t } = useTranslation()
  const label = useModelLabel()
  const nameOf = useSeriesName()
  const locale = currentLocale()

  const metricItems = [
    { value: 'quota', label: t('Spend') },
    { value: 'count', label: t('Requests') },
    { value: 'tokens', label: t('Tokens') },
  ]
  const granItems = [
    { value: 'hour', label: t('Hour') },
    { value: 'day', label: t('Day') },
    { value: 'week', label: t('Week') },
  ]
  const selectedMetric =
    metricItems.find((x) => x.value === props.metric)?.label ?? t('Spend')
  const selectedGran =
    granItems.find((x) => x.value === props.granularity)?.label ?? t('Day')

  const rows = useMemo(
    () => rankModels(props.usage, props.metric, 20),
    [props.usage, props.metric]
  )
  const { series, points } = useMemo(
    () => exploreChart(props.usage, props.granularity, props.metric),
    [props.usage, props.granularity, props.metric]
  )
  const config = useMemo(
    () => buildSeriesConfig(series, nameOf),
    // nameOf closes over t
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [series, t]
  )

  if (props.exportDisabled) {
    return (
      <EmptyState
        title={t('Usage export is off')}
        description={t(
          'The operator has disabled data export, so Explore cannot aggregate spend by model.'
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

  const Chart = props.granularity === 'hour' ? LineChart : BarChart
  const Series = props.granularity === 'hour' ? Line : Bar

  return (
    <div className='flex flex-col gap-4' data-testid='activity-explore'>
      <div
        role='toolbar'
        aria-label={t('Explore controls')}
        className='flex flex-wrap items-center gap-2'
      >
        <Select
          items={metricItems}
          value={props.metric}
          onValueChange={(v) => {
            if (v === 'quota' || v === 'count' || v === 'tokens') {
              props.onMetricChange(v)
            }
          }}
        >
          <SelectTrigger
            className='w-40'
            aria-label={t('Metric')}
            data-testid='activity-metric'
          >
            <SelectValue>
              <span className='truncate'>{selectedMetric}</span>
            </SelectValue>
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectGroup>
              {metricItems.map((x) => (
                <SelectItem key={x.value} value={x.value}>
                  {x.label}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
        <Select
          items={granItems}
          value={props.granularity}
          onValueChange={(v) => {
            if (v === 'hour' || v === 'day' || v === 'week') {
              props.onGranularityChange(v)
            }
          }}
        >
          <SelectTrigger
            className='w-36'
            aria-label={t('Granularity')}
            data-testid='activity-granularity'
          >
            <SelectValue>
              <span className='truncate'>{selectedGran}</span>
            </SelectValue>
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectGroup>
              {granItems.map((x) => (
                <SelectItem key={x.value} value={x.value}>
                  {x.label}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      </div>

      {empty ? (
        <EmptyState
          title={t('No usage in this period')}
          description={t('Try a longer period, or send traffic through a key first.')}
        />
      ) : (
        <>
          <section className='bg-card rounded-xl border p-4'>
            <header className='mb-3'>
              <h2 className='text-sm font-medium'>{t('By model over time')}</h2>
              <p className='text-muted-foreground text-xs'>
                {t('Click a table row to open matching usage logs')}
              </p>
            </header>
            {props.loading ? (
              <div className='bg-muted/40 h-64 animate-pulse rounded-lg' />
            ) : (
              <ChartContainer config={config} className='aspect-auto h-64 w-full'>
                <Chart data={points} margin={{ left: 4, right: 4, top: 8 }}>
                  <CartesianGrid vertical={false} />
                  <XAxis
                    dataKey='label'
                    tickLine={false}
                    axisLine={false}
                    className='text-xs'
                  />
                  <YAxis hide />
                  <ChartTooltip content={<ChartTooltipContent />} />
                  {series.map((s) => (
                    <Series
                      key={s.key}
                      type='monotone'
                      dataKey={s.key}
                      stroke={`var(--color-${s.key})`}
                      fill={`var(--color-${s.key})`}
                      stackId={props.granularity === 'day' ? 'a' : undefined}
                      fillOpacity={props.granularity === 'day' ? 0.9 : 0}
                      strokeWidth={2}
                      dot={false}
                    />
                  ))}
                </Chart>
              </ChartContainer>
            )}
          </section>

          <section className='bg-card rounded-xl border' data-testid='activity-explore-table'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Model')}</TableHead>
                  <TableHead className='text-right'>{t('Requests')}</TableHead>
                  <TableHead className='text-right'>{t('Tokens')}</TableHead>
                  <TableHead className='text-right'>{t('Spend')}</TableHead>
                  <TableHead className='text-right'>{t('Share')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {props.loading
                  ? null
                  : rows.map((r) => (
                      <TableRow key={r.model || '__blank'}>
                        <TableCell>
                          <Link
                            to='/usage-logs'
                            search={usageLogsSearchFromDrilldown({
                              model: r.model,
                              startSec: props.start,
                              endSec: props.end,
                            })}
                            className='text-primary hover:underline'
                            data-testid={`activity-explore-row-${r.model || 'unknown'}`}
                          >
                            {label(r.model)}
                          </Link>
                        </TableCell>
                        <TableCell className='text-right tabular-nums'>
                          {formatNumber(r.count, locale)}
                        </TableCell>
                        <TableCell className='text-right tabular-nums'>
                          {formatCompactNumber(r.tokens, locale)}
                        </TableCell>
                        <TableCell className='text-right tabular-nums'>
                          {formatQuota(r.quota, props.money)}
                        </TableCell>
                        <TableCell className='text-right tabular-nums'>
                          {`${r.sharePct.toFixed(1)}%`}
                        </TableCell>
                      </TableRow>
                    ))}
              </TableBody>
            </Table>
          </section>
        </>
      )}
    </div>
  )
}
