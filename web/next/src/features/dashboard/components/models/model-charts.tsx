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
import { PieChart as PieChartIcon } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Line,
  LineChart,
  Pie,
  PieChart,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import { ChartContainer } from '@/components/ui/chart'
import { getChartColor } from '@/lib/colors'
import { currentLocale } from '@/lib/locale'
import type { TimeGranularity } from '@/lib/time'

import { MODEL_ANALYTICS_CHART_OPTIONS } from '../../lib/constants'
import {
  buildChartSeries,
  rankByCount,
  type Usage,
} from '../../lib/series'
import type { LoadState } from '../../lib/state'
import {
  buildSeriesConfig,
  seriesColor,
  useSeriesName,
} from '../../lib/series-style'
import type { ModelAnalyticsChartTab } from '../../lib/types'
import { Panel, Segmented } from '../panel'
import { LoadFailed } from '../panels'

import { SeriesLegend, UsageTooltip } from './chart-parts'

export interface ModelChartsProps {
  state: LoadState
  usage: Usage
  granularity: TimeGranularity
  defaultChartTab: ModelAnalyticsChartTab
  onRetry?: () => void
}

/** Call-count views of the same rows: trend, share (donut) and ranking. */
export function ModelCharts(props: ModelChartsProps) {
  const { t } = useTranslation()
  const nameOf = useSeriesName()
  const [tab, setTab] = useState<ModelAnalyticsChartTab>(props.defaultChartTab)
  const [lastDefault, setLastDefault] = useState(props.defaultChartTab)
  if (lastDefault !== props.defaultChartTab) {
    setLastDefault(props.defaultChartTab)
    setTab(props.defaultChartTab)
  }

  const fmt = (v: number) => v.toLocaleString(currentLocale())
  const { series, points } = useMemo(
    () => buildChartSeries(props.usage, props.granularity, 'count'),
    [props.usage, props.granularity]
  )
  const rank = useMemo(() => rankByCount(props.usage), [props.usage])
  const config = useMemo(
    () => buildSeriesConfig(series, nameOf),
    // nameOf closes over t only
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [series, t]
  )
  const rankData = rank.map((r, i) => ({
    name: r.model ?? t('Other'),
    count: r.count,
    fill: r.model == null ? 'var(--muted-foreground)' : getChartColor(i),
  }))
  const empty = props.state === 'ok' && props.usage.totals.count === 0

  return (
    <Panel
      testId='dashboard-model-charts'
      icon={<PieChartIcon />}
      tone='chart-4'
      title={t('Model Call Analytics')}
      description={
        props.state === 'ok'
          ? `${t('Total:')} ${fmt(props.usage.totals.count)}`
          : undefined
      }
      loadingHeight={props.state === 'loading' ? 'h-[300px] sm:h-96' : undefined}
      actions={
        <Segmented<ModelAnalyticsChartTab>
          label={t('Chart type')}
          value={tab}
          onChange={setTab}
          testIdPrefix='model-chart-tab'
          options={MODEL_ANALYTICS_CHART_OPTIONS.map((o) => ({
            value: o.value,
            label: t(o.labelKey),
          }))}
        />
      }
    >
      {props.state === 'error' && (
        <LoadFailed
          message={t('Could not load usage data.')}
          onRetry={props.onRetry}
        />
      )}
      {empty && (
        <div
          data-testid='dashboard-model-charts-empty'
          className='text-muted-foreground flex h-48 items-center justify-center text-sm'
        >
          {t('No usage recorded in this range.')}
        </div>
      )}
      {props.state === 'ok' && !empty && tab === 'trend' && (
        <>
          <ChartContainer
            config={config}
            className='aspect-auto h-[300px] w-full sm:h-96'
          >
            <LineChart data={points} margin={{ left: 4, right: 8, top: 8 }}>
              <CartesianGrid vertical={false} />
              <XAxis
                dataKey='label'
                tickLine={false}
                axisLine={false}
                minTickGap={28}
              />
              <YAxis
                tickLine={false}
                axisLine={false}
                width={48}
                allowDecimals={false}
                tickFormatter={fmt}
              />
              <Tooltip
                content={
                  <UsageTooltip
                    series={series}
                    nameOf={nameOf}
                    format={fmt}
                    totalLabel={t('Sum')}
                  />
                }
              />
              {series.map((s) => (
                <Line
                  key={s.key}
                  dataKey={s.key}
                  type='monotone'
                  stroke={seriesColor(series, s.key)}
                  strokeWidth={2}
                  dot={false}
                  isAnimationActive={false}
                />
              ))}
            </LineChart>
          </ChartContainer>
          <SeriesLegend series={series} nameOf={nameOf} />
        </>
      )}
      {props.state === 'ok' && !empty && tab === 'proportion' && (
        <div className='grid items-center gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]'>
          <ChartContainer
            config={{}}
            className='mx-auto aspect-square h-[260px] w-full max-w-[320px] sm:h-[320px]'
          >
            <PieChart>
              <Tooltip
                formatter={(v) => fmt(Number(v))}
                contentStyle={{ borderRadius: 8, fontSize: 12 }}
              />
              <Pie
                data={rankData}
                dataKey='count'
                nameKey='name'
                innerRadius='55%'
                outerRadius='85%'
                paddingAngle={1}
                isAnimationActive={false}
              >
                {rankData.map((r) => (
                  <Cell key={r.name} fill={r.fill} stroke='transparent' />
                ))}
              </Pie>
            </PieChart>
          </ChartContainer>
          <ul className='flex flex-col gap-1.5 text-xs' data-testid='share-rows'>
            {rankData.map((r) => (
              <li key={r.name} className='flex items-center gap-2'>
                <span
                  className='size-2 shrink-0 rounded-sm'
                  style={{ backgroundColor: r.fill }}
                  aria-hidden='true'
                />
                <span className='min-w-0 flex-1 truncate font-mono'>
                  {r.name}
                </span>
                <span className='text-muted-foreground tabular-nums'>
                  {fmt(r.count)} ·{' '}
                  {((r.count / props.usage.totals.count) * 100).toFixed(1)}%
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
      {props.state === 'ok' && !empty && tab === 'top' && (
        <ChartContainer
          config={{}}
          className='aspect-auto w-full'
          style={{ height: Math.max(220, rankData.length * 28 + 24) }}
        >
          <BarChart
            data={rankData}
            layout='vertical'
            margin={{ left: 4, right: 24, top: 4, bottom: 4 }}
          >
            <CartesianGrid horizontal={false} />
            <XAxis
              type='number'
              tickLine={false}
              axisLine={false}
              allowDecimals={false}
              tickFormatter={fmt}
            />
            <YAxis
              type='category'
              dataKey='name'
              tickLine={false}
              axisLine={false}
              width={120}
              tick={{ fontSize: 11 }}
            />
            <Tooltip
              formatter={(v) => fmt(Number(v))}
              contentStyle={{ borderRadius: 8, fontSize: 12 }}
            />
            <Bar dataKey='count' radius={[0, 4, 4, 0]} isAnimationActive={false}>
              {rankData.map((r) => (
                <Cell key={r.name} fill={r.fill} />
              ))}
            </Bar>
          </BarChart>
        </ChartContainer>
      )}
    </Panel>
  )
}
