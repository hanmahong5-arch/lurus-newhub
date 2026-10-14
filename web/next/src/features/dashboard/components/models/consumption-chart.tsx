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
import { AreaChart as AreaChartIcon, BarChart3, WalletCards } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import { ChartContainer } from '@/components/ui/chart'
import { formatQuota, type MoneyConfig } from '@/lib/money'
import type { TimeGranularity } from '@/lib/time'

import { CONSUMPTION_DISTRIBUTION_CHART_OPTIONS } from '../../lib/constants'
import { buildChartSeries, type Usage } from '../../lib/series'
import type { ConsumptionDistributionChartType } from '../../lib/types'
import { Panel, Segmented } from '../panel'
import { LoadFailed } from '../panels'
import type { LoadState } from '../../lib/state'
import {
  buildSeriesConfig,
  seriesColor,
  useSeriesName,
} from '../../lib/series-style'

import { SeriesLegend, UsageTooltip } from './chart-parts'

const TYPE_ICONS = {
  bar: <BarChart3 className='size-3.5' />,
  area: <AreaChartIcon className='size-3.5' />,
} as const

export interface ConsumptionChartProps {
  state: LoadState
  usage: Usage
  granularity: TimeGranularity
  money: MoneyConfig | null
  defaultChartType: ConsumptionDistributionChartType
  onRetry?: () => void
}

/** Spend per model over the selected range, stacked per time bucket. */
export function ConsumptionChart(props: ConsumptionChartProps) {
  const { t } = useTranslation()
  const nameOf = useSeriesName()
  const [chartType, setChartType] = useState<ConsumptionDistributionChartType>(
    props.defaultChartType
  )
  const [lastDefault, setLastDefault] = useState(props.defaultChartType)
  if (lastDefault !== props.defaultChartType) {
    // The saved default changed (preferences dialog): follow it once.
    setLastDefault(props.defaultChartType)
    setChartType(props.defaultChartType)
  }

  const { series, points } = useMemo(
    () => buildChartSeries(props.usage, props.granularity, 'quota'),
    [props.usage, props.granularity]
  )
  const config = useMemo(
    () => buildSeriesConfig(series, nameOf),
    // nameOf closes over t only
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [series, t]
  )
  const empty = props.state === 'ok' && props.usage.totals.count === 0 && props.usage.totals.quota === 0
  const fmtAxis = (v: number) => formatQuota(v, props.money, 2)
  const fmtTip = (v: number) => formatQuota(v, props.money, 4)

  const tooltip = (
    <Tooltip
      content={
        <UsageTooltip
          series={series}
          nameOf={nameOf}
          format={fmtTip}
          totalLabel={t('Sum')}
        />
      }
    />
  )
  const axes = (
    <>
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
        width={64}
        tickFormatter={fmtAxis}
      />
      {tooltip}
    </>
  )

  return (
    <Panel
      testId='dashboard-consumption'
      icon={<WalletCards />}
      tone='success'
      title={t('Quota Distribution')}
      description={
        props.state === 'ok'
          ? `${t('Total:')} ${formatQuota(props.usage.totals.quota, props.money)}`
          : undefined
      }
      loadingHeight={props.state === 'loading' ? 'h-[300px] sm:h-96' : undefined}
      actions={
        <Segmented<ConsumptionDistributionChartType>
          label={t('Chart type')}
          value={chartType}
          onChange={setChartType}
          testIdPrefix='consumption-type'
          options={CONSUMPTION_DISTRIBUTION_CHART_OPTIONS.map((o) => ({
            value: o.value,
            label: t(o.labelKey),
            icon: TYPE_ICONS[o.value],
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
          data-testid='dashboard-consumption-empty'
          className='text-muted-foreground flex h-48 items-center justify-center text-sm'
        >
          {t('No usage recorded in this range.')}
        </div>
      )}
      {props.state === 'ok' && !empty && (
        <>
          <ChartContainer
            config={config}
            className='aspect-auto h-[300px] w-full sm:h-96'
          >
            {chartType === 'bar' ? (
              <BarChart data={points} margin={{ left: 4, right: 8, top: 8 }}>
                {axes}
                {series.map((s) => (
                  <Bar
                    key={s.key}
                    dataKey={s.key}
                    stackId='usage'
                    fill={seriesColor(series, s.key)}
                    isAnimationActive={false}
                  />
                ))}
              </BarChart>
            ) : (
              <AreaChart data={points} margin={{ left: 4, right: 8, top: 8 }}>
                {axes}
                {series.map((s) => (
                  <Area
                    key={s.key}
                    dataKey={s.key}
                    type='monotone'
                    stackId='usage'
                    stroke={seriesColor(series, s.key)}
                    fill={seriesColor(series, s.key)}
                    fillOpacity={0.35}
                    isAnimationActive={false}
                  />
                ))}
              </AreaChart>
            )}
          </ChartContainer>
          <SeriesLegend series={series} nameOf={nameOf} />
        </>
      )}
    </Panel>
  )
}
