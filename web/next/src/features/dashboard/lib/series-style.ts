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
import { useTranslation } from 'react-i18next'

import type { ChartConfig } from '@/components/ui/chart'
import { getChartColor } from '@/lib/colors'

import { useModelLabel } from './model-name'
import { OTHER_KEY, type SeriesDef } from './series'

/** Display name of a series: the model, "Unknown model", or "Other". */
export function useSeriesName() {
  const { t } = useTranslation()
  const label = useModelLabel()
  return (s: SeriesDef) => (s.model === null ? t('Other') : label(s.model))
}

export function seriesColor(series: SeriesDef[], key: string): string {
  if (key === OTHER_KEY) return 'var(--muted-foreground)'
  const idx = series.findIndex((s) => s.key === key)
  return getChartColor(Math.max(0, idx))
}

export function buildSeriesConfig(
  series: SeriesDef[],
  nameOf: (s: SeriesDef) => string
): ChartConfig {
  const config: ChartConfig = {}
  for (const s of series) {
    config[s.key] = { label: nameOf(s), color: seriesColor(series, s.key) }
  }
  return config
}
