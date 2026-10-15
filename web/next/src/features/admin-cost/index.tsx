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
import { ArrowRight, RefreshCw, TriangleAlert } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { PageHeader } from '@/components/layout/page-header'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
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
import { useQuota } from '@/hooks/use-quota'
import { cn } from '@/lib/utils'

import { savingsQuery } from './api'
import { ReadFailure } from './components/read-failure'
import type { Opportunity, ScenarioId, SavingsReport } from './types'

const SCENARIOS: readonly ScenarioId[] = ['conservative', 'aggressive']

function useScenarioText() {
  const { t } = useTranslation()
  return (id: ScenarioId) =>
    id === 'conservative'
      ? { label: t('Conservative'), hint: t('Same-family substitute') }
      : { label: t('Aggressive'), hint: t('Same-tier cross-vendor') }
}

/** Width (0-100) of a bar relative to the largest value in its table. */
function barWidth(value: number, max: number): number {
  if (!(max > 0) || !(value > 0)) return 0
  return Math.max(2, Math.min(100, (value / max) * 100))
}

function ShareBar(props: { value: number; max: number }) {
  return (
    <div
      className='bg-muted mt-1 h-1 w-full overflow-hidden rounded-full'
      aria-hidden='true'
    >
      <div
        className='bg-primary/60 h-full rounded-full'
        style={{ width: `${barWidth(props.value, props.max)}%` }}
      />
    </div>
  )
}

function Panel(props: {
  testId: string
  title: string
  description?: string
  children: ReactNode
}) {
  return (
    <div data-testid={props.testId} className='min-w-0'>
      <TitledCard title={props.title} description={props.description}>
        {props.children}
      </TitledCard>
    </div>
  )
}

function EmptyNote(props: { children: ReactNode }) {
  return <p className='text-muted-foreground py-3 text-sm'>{props.children}</p>
}

const NUM = 'text-right tabular-nums'

function SpendTable(props: {
  testId: string
  title: string
  nameLabel: string
  rows: { name: string; count: number; quota: number }[]
}) {
  const { t } = useTranslation()
  const { formatQuota } = useQuota()
  const max = Math.max(0, ...props.rows.map((r) => r.quota))
  return (
    <Panel testId={props.testId} title={props.title}>
      {props.rows.length === 0 ? (
        <EmptyNote>{t('No data')}</EmptyNote>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{props.nameLabel}</TableHead>
              <TableHead className={NUM}>{t('Requests')}</TableHead>
              <TableHead className={NUM}>{t('Spend')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody className='[&>tr]:h-10'>
            {props.rows.map((r) => (
              <TableRow key={r.name}>
                <TableCell className='min-w-0 font-medium'>
                  <div className='truncate'>{r.name}</div>
                  <ShareBar value={r.quota} max={max} />
                </TableCell>
                <TableCell className={NUM}>
                  {r.count.toLocaleString()}
                </TableCell>
                <TableCell className={NUM}>{formatQuota(r.quota)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Panel>
  )
}

function OpportunitiesTable(props: { rows: Opportunity[]; title: string }) {
  const { t } = useTranslation()
  const { formatQuota } = useQuota()
  const max = Math.max(0, ...props.rows.map((o) => o.savings_quota))
  return (
    <Panel testId='top-opportunities' title={props.title}>
      {props.rows.length === 0 ? (
        <EmptyNote>
          {t('No eligible savings opportunities in this window')}
        </EmptyNote>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Model')}</TableHead>
              <TableHead>{t('Alternative')}</TableHead>
              <TableHead className={NUM}>{t('Current spend')}</TableHead>
              <TableHead className={NUM}>{t('Est. savings')}</TableHead>
              <TableHead className={NUM}>%</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody className='[&>tr]:h-10'>
            {props.rows.map((o) => (
              <TableRow key={`${o.model}>${o.alt_model}`}>
                <TableCell className='font-medium'>{o.model}</TableCell>
                <TableCell>
                  <span className='text-muted-foreground inline-flex items-center gap-1.5'>
                    <ArrowRight className='size-3.5' aria-hidden='true' />
                    <span className='text-foreground'>{o.alt_model}</span>
                  </span>
                </TableCell>
                <TableCell className={NUM}>
                  {o.current_quota === undefined
                    ? '--'
                    : formatQuota(o.current_quota)}
                </TableCell>
                <TableCell className={NUM}>
                  {formatQuota(o.savings_quota)}
                  <ShareBar value={o.savings_quota} max={max} />
                </TableCell>
                <TableCell className={NUM}>
                  {`${(o.savings_pct * 100).toFixed(0)}%`}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Panel>
  )
}

function Report(props: { data: SavingsReport }) {
  const { t } = useTranslation()
  const { formatQuota } = useQuota()
  const scenarioText = useScenarioText()
  const [scenario, setScenario] = useState<ScenarioId>('conservative')
  const { data } = props

  const active = data.scenarios[scenario] ?? null
  const savingsQuota = active?.total_savings_quota ?? 0
  const hasSavings = savingsQuota > 0
  const scenarioLabel = scenarioText(scenario).label

  const headline = hasSavings
    ? formatQuota(savingsQuota)
    : t('{{amount}} — no eligible savings', { amount: formatQuota(0) })

  return (
    <div className='space-y-4'>
      {/* Honest heuristic caveat: always visible, never collapsed. */}
      <Alert data-testid='caveat'>
        <TriangleAlert aria-hidden='true' />
        <AlertDescription>
          {data.caveat ||
            t(
              'Aggregate estimate; the model tiers are heuristic, not measured'
            )}
        </AlertDescription>
      </Alert>

      <div
        role='group'
        aria-label={t('Savings scenario')}
        className='flex flex-wrap items-center gap-2'
      >
        {SCENARIOS.map((id) => {
          const text = scenarioText(id)
          return (
            <Button
              key={id}
              size='sm'
              variant={scenario === id ? 'default' : 'outline'}
              aria-pressed={scenario === id}
              data-testid={`scenario-${id}`}
              title={text.hint}
              onClick={() => setScenario(id)}
            >
              {text.label}
            </Button>
          )
        })}
        <span className='text-muted-foreground text-xs'>
          {scenarioText(scenario).hint}
        </span>
      </div>

      <TitledCard
        title={t('Estimated savings · {{scenario}}', {
          scenario: scenarioLabel,
        })}
      >
        <div
          data-testid='savings-headline'
          className={cn(
            'font-mono text-3xl leading-tight font-semibold tabular-nums',
            !hasSavings && 'text-muted-foreground'
          )}
        >
          {headline}
        </div>
        <p className='text-muted-foreground mt-1.5 text-sm'>
          {t('of {{total}} total spend · {{pct}}% potential', {
            total: formatQuota(data.total_spend_quota),
            pct: ((active?.savings_pct ?? 0) * 100).toFixed(1),
          })}
        </p>
      </TitledCard>

      <div className='grid gap-4 lg:grid-cols-2'>
        <SpendTable
          testId='spend-by-model'
          title={t('Spend by model')}
          nameLabel={t('Model')}
          rows={data.spend_by_model.map((m) => ({
            name: m.model_name,
            count: m.count,
            quota: m.total_quota,
          }))}
        />
        <SpendTable
          testId='spend-by-product'
          title={t('Spend by product')}
          nameLabel={t('Product')}
          rows={data.spend_by_product.map((p) => ({
            name: p.product,
            count: p.count,
            quota: p.total_quota,
          }))}
        />
      </div>

      <OpportunitiesTable
        title={t('Top opportunities · {{scenario}}', {
          scenario: scenarioLabel,
        })}
        rows={active?.top_opportunities ?? []}
      />

      {data.excluded.length > 0 && (
        <TitledCard
          title={t('Excluded from re-pricing (honest gaps)')}
          description={t(
            'These models were left out of the estimate because they cannot be re-priced per token.'
          )}
        >
          <div
            data-testid='excluded'
            className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs'
          >
            {data.excluded.map((e) => (
              <span key={e.model} className='font-mono'>
                {`${e.model} (${e.reason})`}
              </span>
            ))}
          </div>
        </TitledCard>
      )}
    </div>
  )
}

function Loading() {
  return (
    <div className='space-y-4' data-testid='cost-loading'>
      <Skeleton className='h-12 w-full' />
      <Skeleton className='h-32 w-full' />
      <div className='grid gap-4 lg:grid-cols-2'>
        <Skeleton className='h-48 w-full' />
        <Skeleton className='h-48 w-full' />
      </div>
    </div>
  )
}

export function AdminCostPage() {
  const { t } = useTranslation()
  const q = useQuery(savingsQuery)

  let body: ReactNode
  if (q.isError) {
    body = (
      <ReadFailure
        error={q.error}
        onRetry={() => void q.refetch()}
        testIdPrefix='cost'
        forbiddenDescription={t(
          'You do not have permission to view platform cost intelligence.'
        )}
        errorTitle={t('Savings figure unknown')}
        errorDescription={t(
          'The savings endpoint did not answer. This is not a report that there is nothing to save; the analysis simply did not run.'
        )}
      />
    )
  } else if (q.isPending) {
    body = <Loading />
  } else if (q.data === null) {
    body = (
      <div data-testid='cost-no-data'>
        <EmptyState
          title={t('Savings endpoint returned no data')}
          description={t(
            'The analyzer answered with an empty report for this window.'
          )}
        />
      </div>
    )
  } else {
    body = <Report data={q.data} />
  }

  return (
    <div className='space-y-4'>
      <PageHeader
        title={t('Savings analyzer')}
        description={t(
          'What cost-aware routing would have saved · last 7 days'
        )}
        actions={
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
        }
      />
      {body}
    </div>
  )
}
