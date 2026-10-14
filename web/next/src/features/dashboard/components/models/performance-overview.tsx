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
import { HeartPulse } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { currentLocale } from '@/lib/locale'
import { cn } from '@/lib/utils'

import { PERFORMANCE_WINDOWS } from '../../lib/constants'
import { fetchPerformance } from '../../lib/api'
import { formatErrorRate, formatLatencyMs } from '../../lib/kpis'
import { hasVolume, sortPerformance } from '../../lib/performance'
import type { PerformanceRow } from '../../lib/types'
import { Panel, Segmented } from '../panel'
import { LoadFailed } from '../panels'

const COLLAPSED_ROWS = 8

/** Error-rate colour: only a measured rate is judged, never a thin sample. */
function rateClass(row: PerformanceRow): string {
  if (!row.enough_samples) return 'text-muted-foreground'
  if (row.error_rate >= 0.05) return 'text-destructive'
  if (row.error_rate >= 0.01) return 'text-warning'
  return 'text-success'
}

/**
 * Per-model latency and error rate from GET ~/models/performance. Every
 * member sees p50 / p95 / error rate; request volume (and the pooled success
 * rate that needs it) appears only when the server sent it.
 */
export function PerformanceOverview() {
  const { t } = useTranslation()
  const [hours, setHours] = useState<number>(24)
  const [expanded, setExpanded] = useState(false)
  const q = useQuery({
    queryKey: ['dashboard', 'performance', hours],
    queryFn: () => fetchPerformance(hours),
    staleTime: 60_000,
    retry: false,
  })
  const rows = useMemo(() => sortPerformance(q.data?.items ?? []), [q.data])
  const volume = hasVolume(rows)
  const requests = rows.reduce((s, r) => s + (r.requests ?? 0), 0)
  const errors = rows.reduce((s, r) => s + (r.errors ?? 0), 0)
  const shown = expanded ? rows : rows.slice(0, COLLAPSED_ROWS)
  const fmt = (v: number) => v.toLocaleString(currentLocale())

  return (
    <Panel
      testId='dashboard-performance'
      icon={<HeartPulse />}
      tone='success'
      title={t('Performance health')}
      description={t('Latency and error rate per model')}
      loadingHeight={q.isPending ? 'h-28' : undefined}
      actions={
        <Segmented<string>
          label={t('Time window')}
          value={String(hours)}
          onChange={(v) => setHours(Number(v))}
          testIdPrefix='performance-window'
          options={PERFORMANCE_WINDOWS.map((w) => ({
            value: String(w.hours),
            label: t(w.label),
          }))}
        />
      }
    >
      {q.isError && (
        <LoadFailed
          message={t('Could not load model performance.')}
          onRetry={() => void q.refetch()}
        />
      )}
      {q.isSuccess && rows.length === 0 && (
        <div
          data-testid='dashboard-performance-empty'
          className='text-muted-foreground py-4 text-center text-sm'
        >
          {t('No performance data available')}
        </div>
      )}
      {q.isSuccess && rows.length > 0 && (
        <div className='flex flex-col gap-3'>
          {volume && requests > 0 && (
            <div
              data-testid='performance-summary'
              className='flex flex-wrap items-center gap-x-6 gap-y-1 text-xs'
            >
              <span>
                <span className='text-muted-foreground'>
                  {t('Success rate')}{' '}
                </span>
                <span className='font-mono font-semibold tabular-nums'>
                  {(((requests - errors) / requests) * 100).toFixed(2)}%
                </span>
              </span>
              <span>
                <span className='text-muted-foreground'>{t('Requests')} </span>
                <span className='font-mono font-semibold tabular-nums'>
                  {fmt(requests)}
                </span>
              </span>
            </div>
          )}
          <div className='overflow-x-auto'>
            <table className='w-full text-xs' data-testid='performance-rows'>
              <thead>
                <tr className='text-muted-foreground border-b text-start'>
                  <th className='py-1.5 pe-3 text-start font-medium'>
                    {t('Model')}
                  </th>
                  <th className='px-3 py-1.5 text-end font-medium'>P50</th>
                  <th className='px-3 py-1.5 text-end font-medium'>P95</th>
                  <th className='px-3 py-1.5 text-end font-medium'>
                    {t('Error rate')}
                  </th>
                  {volume && (
                    <th className='ps-3 py-1.5 text-end font-medium'>
                      {t('Requests')}
                    </th>
                  )}
                </tr>
              </thead>
              <tbody>
                {shown.map((r) => (
                  <tr
                    key={r.model_name}
                    data-testid='performance-row'
                    className='border-border/60 border-b last:border-0'
                  >
                    <td className='max-w-56 truncate py-1.5 pe-3 font-mono'>
                      {r.model_name}
                      {!r.enough_samples && (
                        <span className='text-muted-foreground ms-2 font-sans'>
                          {t('Low sample')}
                        </span>
                      )}
                    </td>
                    <td className='px-3 py-1.5 text-end font-mono tabular-nums'>
                      {formatLatencyMs(r.p50_latency_ms || null)}
                    </td>
                    <td className='px-3 py-1.5 text-end font-mono tabular-nums'>
                      {formatLatencyMs(r.p95_latency_ms || null)}
                    </td>
                    <td
                      className={cn(
                        'px-3 py-1.5 text-end font-mono font-semibold tabular-nums',
                        rateClass(r)
                      )}
                    >
                      {formatErrorRate(r.error_rate)}
                    </td>
                    {volume && (
                      <td className='ps-3 py-1.5 text-end font-mono tabular-nums'>
                        {typeof r.requests === 'number' ? fmt(r.requests) : '--'}
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {rows.length > COLLAPSED_ROWS && (
            <Button
              variant='ghost'
              size='sm'
              className='self-center'
              onClick={() => setExpanded((v) => !v)}
            >
              {expanded
                ? t('Show fewer')
                : t('Show all {{n}} models', { n: rows.length })}
            </Button>
          )}
        </div>
      )}
    </Panel>
  )
}
