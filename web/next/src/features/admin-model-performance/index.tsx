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
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { ArrowDown, ArrowUp, Download, RefreshCw } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { PageHeader } from '@/components/layout/page-header'
import { Button, buttonVariants } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { TitledCard } from '@/components/ui/titled-card'
import { ReadFailure } from '@/features/admin-cost/components/read-failure'
import {
  classifyReadFailure,
  type ReadFailureKind,
} from '@/features/admin-cost/lib/read-failure'
import { useQuota } from '@/hooks/use-quota'
import { cn } from '@/lib/utils'

import { exportUrl, performanceQuery, tenantOptionsQuery } from './api'
import {
  DEFAULT_RANGE,
  RANGES,
  errorSeverity,
  fmtInt,
  fmtMs,
  fmtPct,
  nextSort,
  rangeSeconds,
  sortModels,
  summarize,
} from './lib/derive'
import type { ModelPerf, RangeId, SortDir, SortKey } from './types'

function useRangeLabel() {
  const { t } = useTranslation()
  return (id: RangeId) => {
    if (id === '1h') return t('Last 1h')
    if (id === '24h') return t('Last 24h')
    return t('Last 7d')
  }
}

function Bar(props: { value: number; max: number; className?: string }) {
  const width =
    props.max > 0 && props.value > 0
      ? Math.max(2, Math.min(100, (props.value / props.max) * 100))
      : 0
  return (
    <div
      className='bg-muted mt-1 ml-auto h-1 w-full max-w-24 overflow-hidden rounded-full'
      aria-hidden='true'
    >
      <div
        className={cn('bg-primary/60 h-full rounded-full', props.className)}
        style={{ width: `${width}%` }}
      />
    </div>
  )
}

function StatCard(props: {
  label: string
  value: string
  sub: string
  loading: boolean
}) {
  return (
    <TitledCard title={props.label}>
      {props.loading ? (
        <Skeleton className='h-8 w-24' />
      ) : (
        <div className='font-mono text-2xl leading-tight font-semibold tabular-nums'>
          {props.value}
        </div>
      )}
      <p className='text-muted-foreground mt-1 text-xs'>{props.sub}</p>
    </TitledCard>
  )
}

const NUM = 'text-right tabular-nums whitespace-nowrap'

function ariaSort(
  on: boolean,
  dir: SortDir
): 'none' | 'ascending' | 'descending' {
  if (!on) return 'none'
  return dir === 'desc' ? 'descending' : 'ascending'
}

function SortHead(props: {
  /** The column this header sorts by. */
  field: SortKey
  label: string
  active: SortKey
  dir: SortDir
  onSort: (key: SortKey) => void
}) {
  const on = props.active === props.field
  const Icon = props.dir === 'desc' ? ArrowDown : ArrowUp
  return (
    <TableHead className={NUM} aria-sort={ariaSort(on, props.dir)}>
      <button
        type='button'
        data-testid={`perf-sort-${props.field}`}
        className='hover:text-foreground inline-flex items-center gap-1'
        onClick={() => props.onSort(props.field)}
      >
        {props.label}
        {on && <Icon className='size-3' aria-hidden='true' />}
      </button>
    </TableHead>
  )
}

const SEVERITY_CLASS = {
  ok: '',
  warn: 'text-warning',
  bad: 'text-destructive',
} as const

function ModelRow(props: {
  m: ModelPerf
  maxRequests: number
  maxP95: number
}) {
  const { formatQuota } = useQuota()
  const { m } = props
  return (
    <TableRow data-testid='perf-row'>
      <TableCell className='font-medium'>{m.model_name}</TableCell>
      <TableCell className={NUM}>
        {fmtInt(m.requests)}
        <Bar value={m.requests} max={props.maxRequests} />
      </TableCell>
      <TableCell
        className={cn(NUM, SEVERITY_CLASS[errorSeverity(m.error_rate)])}
      >
        {fmtPct(m.error_rate)}
        {m.errors > 0 && (
          <span className='text-muted-foreground'> ({fmtInt(m.errors)})</span>
        )}
      </TableCell>
      <TableCell className={NUM}>
        {fmtInt(m.prompt_tokens)} / {fmtInt(m.completion_tokens)}
      </TableCell>
      <TableCell className={NUM}>{formatQuota(m.quota || 0)}</TableCell>
      <TableCell className={NUM}>
        {fmtMs(m.avg_latency_ms, m.latency_samples)}
      </TableCell>
      <TableCell className={NUM}>
        {fmtMs(m.p50_latency_ms, m.latency_samples)}
      </TableCell>
      <TableCell className={NUM}>
        {fmtMs(m.p95_latency_ms, m.latency_samples)}
        {m.latency_samples > 0 && (
          <Bar value={m.p95_latency_ms} max={props.maxP95} />
        )}
      </TableCell>
    </TableRow>
  )
}

function ModelTable(props: {
  models: ModelPerf[]
  sortKey: SortKey
  sortDir: SortDir
  onSort: (key: SortKey) => void
  dimmed: boolean
}) {
  const { t } = useTranslation()
  const sorted = useMemo(
    () => sortModels(props.models, props.sortKey, props.sortDir),
    [props.models, props.sortKey, props.sortDir]
  )
  const maxRequests = Math.max(0, ...props.models.map((m) => m.requests))
  const maxP95 = Math.max(0, ...props.models.map((m) => m.p95_latency_ms))
  const head = {
    active: props.sortKey,
    dir: props.sortDir,
    onSort: props.onSort,
  }
  return (
    <TitledCard title={t('Per-model breakdown')}>
      {sorted.length === 0 ? (
        <EmptyState
          className='min-h-[160px]'
          title={t('No traffic in this window')}
        />
      ) : (
        <div className={cn(props.dimmed && 'opacity-60 transition-opacity')}>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Model')}</TableHead>
                <SortHead field='requests' label={t('Requests')} {...head} />
                <SortHead
                  field='error_rate'
                  label={t('Error rate')}
                  {...head}
                />
                <TableHead className={NUM}>{t('Tokens (in / out)')}</TableHead>
                <TableHead className={NUM}>{t('Spend')}</TableHead>
                <SortHead field='avg_latency_ms' label={t('Avg')} {...head} />
                <SortHead field='p50_latency_ms' label='p50' {...head} />
                <SortHead field='p95_latency_ms' label='p95' {...head} />
              </TableRow>
            </TableHeader>
            <TableBody className='[&>tr]:h-12'>
              {sorted.map((m) => (
                <ModelRow
                  key={m.model_name}
                  m={m}
                  maxRequests={maxRequests}
                  maxP95={maxP95}
                />
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </TitledCard>
  )
}

function Summary(props: { models: ModelPerf[]; loading: boolean }) {
  const { t } = useTranslation()
  const { formatQuota } = useQuota()
  const s = useMemo(() => summarize(props.models), [props.models])
  return (
    <div className='grid gap-4 sm:grid-cols-2 xl:grid-cols-4'>
      <StatCard
        loading={props.loading}
        label={t('Total requests')}
        value={fmtInt(s.totalRequests)}
        sub={t('In window')}
      />
      <StatCard
        loading={props.loading}
        label={t('Total error rate')}
        value={
          s.totalRequests > 0 ? fmtPct(s.totalErrors / s.totalRequests) : '—'
        }
        sub={t('{{count}} errors', { count: fmtInt(s.totalErrors) })}
      />
      <StatCard
        loading={props.loading}
        label={t('Weighted avg latency')}
        value={s.totalSamples > 0 ? `${Math.round(s.weightedLatency)}ms` : '—'}
        sub={t('{{count}} samples', { count: fmtInt(s.totalSamples) })}
      />
      <StatCard
        loading={props.loading}
        label={t('Total spend')}
        value={formatQuota(s.totalQuota)}
        sub={t('Across {{count}} models', {
          count: fmtInt(props.models.length),
        })}
      />
    </div>
  )
}

export function AdminModelPerformancePage() {
  const { t } = useTranslation()
  const rangeLabel = useRangeLabel()
  const [range, setRange] = useState<RangeId>(DEFAULT_RANGE)
  const [tenantId, setTenantId] = useState('')
  const [sort, setSort] = useState<{ key: SortKey; dir: SortDir }>({
    key: 'requests',
    dir: 'desc',
  })

  const q = useQuery({
    ...performanceQuery(range, tenantId),
    placeholderData: keepPreviousData,
  })
  const tenants = useQuery(tenantOptionsQuery)

  const kind: ReadFailureKind | null = q.isError
    ? classifyReadFailure(q.error)
    : null
  const blocked = kind === 'forbidden' || kind === 'signed-out'

  const exportWindow = q.data
    ? { start_time: q.data.start_time, end_time: q.data.end_time }
    : (() => {
        const end = Math.floor(Date.now() / 1000)
        return { start_time: end - rangeSeconds(range), end_time: end }
      })()

  const title =
    kind === 'error' ? t('Model performance unknown') : t('Model performance')

  let body: ReactNode
  if (q.isError) {
    body = (
      <ReadFailure
        error={q.error}
        onRetry={() => void q.refetch()}
        testIdPrefix='perf'
        forbiddenDescription={t(
          'You do not have permission to view model performance analytics.'
        )}
        errorTitle={t('Model performance unknown')}
        errorDescription={t(
          'The analytics endpoint did not answer. This is not a report of zero traffic and zero errors; the window simply could not be read.'
        )}
      />
    )
  } else {
    const models = q.data?.models ?? []
    const pending = q.isPending
    body = (
      <>
        <Summary models={models} loading={pending} />
        {pending ? (
          <Skeleton className='h-64 w-full' />
        ) : (
          <ModelTable
            models={models}
            sortKey={sort.key}
            sortDir={sort.dir}
            onSort={(key) => setSort((cur) => nextSort(cur, key))}
            dimmed={q.isPlaceholderData}
          />
        )}
      </>
    )
  }

  return (
    <div className='space-y-4'>
      <PageHeader
        title={title}
        description={t(
          'Requests · errors · tokens · latency percentiles per model'
        )}
        actions={
          !blocked && (
            <div className='flex flex-wrap items-center gap-2'>
              <Button
                variant='outline'
                size='sm'
                disabled={q.isFetching}
                onClick={() => void q.refetch()}
              >
                <RefreshCw
                  className={cn(q.isFetching && 'animate-spin')}
                  aria-hidden='true'
                />
                {t('Refresh')}
              </Button>
              {kind === null && (
                <a
                  data-testid='perf-export-btn'
                  className={buttonVariants({ variant: 'outline', size: 'sm' })}
                  href={exportUrl(exportWindow, tenantId)}
                  download
                >
                  <Download aria-hidden='true' />
                  {t('Export CSV')}
                </a>
              )}
            </div>
          )
        }
      />

      {!blocked && (
        <div className='flex flex-wrap items-center gap-2'>
          <div
            role='group'
            aria-label={t('Time range')}
            className='flex flex-wrap items-center gap-2'
          >
            {RANGES.map((r) => (
              <Button
                key={r.id}
                size='sm'
                variant={range === r.id ? 'default' : 'outline'}
                aria-pressed={range === r.id}
                data-testid={`perf-range-${r.id}`}
                onClick={() => setRange(r.id)}
              >
                {rangeLabel(r.id)}
              </Button>
            ))}
          </div>
          <NativeSelect
            size='sm'
            aria-label={t('Tenant')}
            data-testid='perf-tenant-filter'
            value={tenantId}
            onChange={(e) => setTenantId(e.target.value)}
          >
            <NativeSelectOption value=''>{t('All tenants')}</NativeSelectOption>
            {(tenants.data ?? []).map((tn) => (
              <NativeSelectOption key={tn.id} value={tn.id}>
                {tn.name || tn.slug || tn.id}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          {q.isFetching && (
            <span className='text-muted-foreground text-xs'>
              {t('Loading...')}
            </span>
          )}
          {q.data && !q.isError && (
            <span
              className='text-muted-foreground ml-auto text-xs'
              data-testid='perf-window'
            >
              {t('Window {{start}} to {{end}}', {
                start: new Date(q.data.start_time * 1000).toLocaleString(),
                end: new Date(q.data.end_time * 1000).toLocaleString(),
              })}
            </span>
          )}
        </div>
      )}

      {body}
    </div>
  )
}
