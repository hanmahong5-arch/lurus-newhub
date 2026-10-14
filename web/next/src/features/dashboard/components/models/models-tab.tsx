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
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { useMoneyConfig } from '@/lib/status'

import { fetchQuotaRows } from '../../lib/api'
import {
  buildDefaultFilters,
  filtersKey,
  getSavedChartPreferences,
  resolveRange,
  saveChartPreferences,
} from '../../lib/filters'
import { aggregateUsage } from '../../lib/series'
import { loadState } from '../../lib/state'
import type {
  DashboardChartPreferences,
  DashboardFilters,
} from '../../lib/types'

import { ConsumptionChart } from './consumption-chart'
import { ModelsFilter } from './models-filter'
import { ModelCharts } from './model-charts'
import { ModelsChartPreferences } from './models-preferences'
import { PerformanceOverview } from './performance-overview'
import { StatStrip } from './stat-strip'

/**
 * "Models" section: totals, spend and call charts over a chosen range from
 * GET /api/data/self/, plus per-model latency from ~/models/performance.
 * Buckets are built here: the endpoint only ever answers hour-sized rows.
 */
export function ModelsTab() {
  const { t } = useTranslation()
  const money = useMoneyConfig()
  const [prefs, setPrefs] = useState<DashboardChartPreferences>(() =>
    getSavedChartPreferences()
  )
  const [filters, setFilters] = useState<DashboardFilters>(() =>
    buildDefaultFilters(prefs)
  )

  const q = useQuery({
    queryKey: ['dashboard', 'models-usage', filtersKey(filters)],
    queryFn: async () => {
      const { start, end } = resolveRange(filters)
      const rows = await fetchQuotaRows(start, end)
      return { rows, start, end }
    },
    retry: false,
  })

  const usage = useMemo(
    () =>
      aggregateUsage(
        q.data?.rows ?? [],
        filters.granularity,
        q.data?.start,
        q.data?.end
      ),
    [q.data, filters.granularity]
  )
  const state = loadState(q)
  const retry = () => void q.refetch()

  const savePrefs = (next: DashboardChartPreferences) => {
    setPrefs(next)
    saveChartPreferences(next)
    // A new default range / bucket size applies right away.
    setFilters(buildDefaultFilters(next))
  }

  return (
    <div className='flex flex-col gap-3 sm:gap-4' data-testid='dashboard-models'>
      <div
        role='toolbar'
        aria-label={t('Model analytics controls')}
        className='flex flex-wrap items-center justify-end gap-1.5 sm:gap-2'
      >
        <ModelsChartPreferences preferences={prefs} onSave={savePrefs} />
        <ModelsFilter
          preferences={prefs}
          currentFilters={filters}
          onApply={setFilters}
          onReset={() => setFilters(buildDefaultFilters(prefs))}
        />
      </div>
      <StatStrip
        state={state}
        totals={usage.totals}
        start={q.data?.start ?? 0}
        end={q.data?.end ?? 0}
        money={money}
      />
      <PerformanceOverview />
      <ConsumptionChart
        state={state}
        usage={usage}
        granularity={filters.granularity}
        money={money}
        defaultChartType={prefs.consumptionDistributionChart}
        onRetry={retry}
      />
      <ModelCharts
        state={state}
        usage={usage}
        granularity={filters.granularity}
        defaultChartTab={prefs.modelAnalyticsChart}
        onRetry={retry}
      />
    </div>
  )
}
