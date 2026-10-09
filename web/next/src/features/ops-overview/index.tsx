import { useQuery } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { PageHeader } from '@/components/layout/page-header'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
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
import { formatQuota } from '@/lib/money'
import { useMoneyConfig } from '@/lib/status'

import {
  expiringSoon,
  formatCostCny4,
  formatPercent,
  reasonTotals,
  totalsOf,
  unroutable,
  windowNearLimit,
} from './lib/derive'
import { modelStatusQuery, probesQuery, usageSummaryQuery } from './lib/api'
import type { ChannelProbe, ModelState, SummaryChannel } from './lib/types'

function errorMessage(error: unknown): string | undefined {
  return error instanceof Error && error.message ? error.message : undefined
}

function SectionError(props: {
  title: string
  error: unknown
  onRetry: () => void
  testId: string
}) {
  return (
    <div data-testid={props.testId}>
      <ErrorState
        className='min-h-[160px]'
        title={props.title}
        description={errorMessage(props.error)}
        onRetry={props.onRetry}
      />
    </div>
  )
}

function Loading() {
  return (
    <div className='space-y-2'>
      <Skeleton className='h-8 w-full' />
      <Skeleton className='h-8 w-full' />
    </div>
  )
}

function Muted(props: { children: ReactNode }) {
  return <p className='text-muted-foreground py-3 text-sm'>{props.children}</p>
}

const REASON_LABELS: Record<string, string> = {
  disabled_manual: 'Switched off manually',
  auto_disabled: 'Auto-disabled',
  cooling_429: 'Cooling down (429)',
  plan_window_exhausted: 'Plan window exhausted',
  expired: 'Expired',
  balance_low: 'Balance too low',
  auth_failed: 'Authentication failed',
}

function useReasonLabel() {
  const { t } = useTranslation()
  return (code: string) =>
    code in REASON_LABELS ? t(REASON_LABELS[code]) : code
}

function formatTime(sec: number): string {
  return new Date(sec * 1000).toLocaleString()
}

function formatLeft(
  expiresAt: number,
  nowSec: number,
  t: (k: string) => string
): string {
  const left = expiresAt - nowSec
  if (left <= 0) return t('Expired')
  const hours = Math.floor(left / 3600)
  return `${hours}h`
}

// ---- usage summary -------------------------------------------------------

function VerdictBadge(props: { verdict: SummaryChannel['verdict'] }) {
  const { t } = useTranslation()
  if (props.verdict === 'idle') {
    return <StatusBadge variant='warning' label={t('Idle')} copyable={false} />
  }
  if (props.verdict === 'overuse') {
    return (
      <StatusBadge variant='danger' label={t('Overused')} copyable={false} />
    )
  }
  return null
}

export function UsageSection(props: {
  channels: SummaryChannel[]
  truncated: boolean
}) {
  const { t } = useTranslation()
  const money = useMoneyConfig()
  const totals = totalsOf(props.channels)
  return (
    <div className='space-y-3' data-testid='usage-section'>
      <div className='grid gap-3 sm:grid-cols-3'>
        <Stat label={t('30-day cost')} value={formatCostCny4(totals.cost)} />
        <Stat
          label={t('30-day requests')}
          value={totals.requests.toLocaleString()}
        />
        <Stat
          label={t('30-day billed amount')}
          value={formatQuota(totals.quota, money)}
        />
      </div>
      {props.truncated && (
        <Muted>
          {t('Only the first channels are listed (list truncated).')}
        </Muted>
      )}
      {props.channels.length === 0 ? (
        <Muted>{t('No channels.')}</Muted>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Channel')}</TableHead>
              <TableHead className='text-right'>{t('Utilization')}</TableHead>
              <TableHead className='text-right'>{t('Cost')}</TableHead>
              <TableHead className='text-right'>{t('Requests')}</TableHead>
              <TableHead className='text-right'>{t('Errors')}</TableHead>
              <TableHead>{t('Verdict')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {props.channels.map((c) => (
              <TableRow key={c.channel_id}>
                <TableCell>
                  {c.name}{' '}
                  <span className='text-muted-foreground'>#{c.channel_id}</span>
                </TableCell>
                <TableCell className='text-right'>
                  {typeof c.utilization === 'number'
                    ? formatPercent(c.utilization)
                    : t('No plan fee')}
                </TableCell>
                <TableCell className='text-right'>
                  {formatCostCny4(c.cost_cny4)}
                </TableCell>
                <TableCell className='text-right'>
                  {c.requests.toLocaleString()}
                </TableCell>
                <TableCell className='text-right'>
                  {c.errors.toLocaleString()}
                </TableCell>
                <TableCell>
                  <VerdictBadge verdict={c.verdict} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}

function Stat(props: { label: string; value: string }) {
  return (
    <div className='rounded-lg border p-3'>
      <div className='text-muted-foreground text-xs'>{props.label}</div>
      <div className='text-xl font-semibold'>{props.value}</div>
    </div>
  )
}

// ---- channel health lists ---------------------------------------------------

export function ExpiringList(props: { rows: ChannelProbe[]; nowSec: number }) {
  const { t } = useTranslation()
  if (props.rows.length === 0) {
    return <Muted>{t('Nothing expires within 72 hours.')}</Muted>
  }
  return (
    <ul className='divide-y text-sm' data-testid='expiring-list'>
      {props.rows.map((p) => (
        <li
          key={p.channelId}
          className='flex items-center justify-between py-2'
        >
          <span>
            {p.name}{' '}
            <span className='text-muted-foreground'>#{p.channelId}</span>
          </span>
          <span className='flex items-center gap-2'>
            <span className='text-muted-foreground'>
              {formatTime(p.expiresAt ?? 0)}
            </span>
            <StatusBadge
              variant={
                (p.expiresAt ?? 0) <= props.nowSec ? 'danger' : 'warning'
              }
              label={formatLeft(p.expiresAt ?? 0, props.nowSec, t)}
              copyable={false}
            />
          </span>
        </li>
      ))}
    </ul>
  )
}

export function WindowList(props: { rows: ChannelProbe[] }) {
  const { t } = useTranslation()
  if (props.rows.length === 0) {
    return <Muted>{t('No plan window is above 90%.')}</Muted>
  }
  return (
    <ul className='divide-y text-sm' data-testid='window-list'>
      {props.rows.map((p) => (
        <li
          key={p.channelId}
          className='flex items-center justify-between py-2'
        >
          <span>
            {p.name}{' '}
            <span className='text-muted-foreground'>#{p.channelId}</span>
          </span>
          <StatusBadge
            variant='danger'
            label={`${(p.windowUsedPct ?? 0).toFixed(0)}%`}
            copyable={false}
          />
        </li>
      ))}
    </ul>
  )
}

export function UnroutableList(props: { rows: ChannelProbe[] }) {
  const { t } = useTranslation()
  const label = useReasonLabel()
  if (props.rows.length === 0) {
    return <Muted>{t('Every channel is routable.')}</Muted>
  }
  return (
    <Table data-testid='unroutable-list'>
      <TableHeader>
        <TableRow>
          <TableHead>{t('Channel')}</TableHead>
          <TableHead>{t('Reasons')}</TableHead>
          <TableHead>{t('Last error')}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {props.rows.map((p) => (
          <TableRow key={p.channelId}>
            <TableCell>
              {p.name}{' '}
              <span className='text-muted-foreground'>#{p.channelId}</span>
            </TableCell>
            <TableCell>
              <div className='flex flex-wrap gap-1'>
                {p.reasons.map((r) => (
                  <StatusBadge
                    key={r}
                    variant={r === 'disabled_manual' ? 'neutral' : 'warning'}
                    label={label(r)}
                    copyable={false}
                  />
                ))}
              </div>
            </TableCell>
            <TableCell className='text-muted-foreground max-w-xs truncate'>
              {p.lastError || '--'}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

// ---- public model status ----------------------------------------------------

const MODEL_VARIANT: Record<ModelState, StatusVariant> = {
  operational: 'success',
  degraded: 'warning',
  down: 'danger',
}

function ModelStatusSection() {
  const { t } = useTranslation()
  const q = useQuery(modelStatusQuery)
  if (q.isError) {
    return (
      <SectionError
        testId='model-status-error'
        title={t('Failed to load model status')}
        error={q.error}
        onRetry={() => void q.refetch()}
      />
    )
  }
  if (!q.data) return <Loading />
  if (q.data.models.length === 0) {
    return <Muted>{t('No models are published.')}</Muted>
  }
  const stateLabel: Record<ModelState, string> = {
    operational: t('Operational'),
    degraded: t('Degraded'),
    down: t('Down'),
  }
  return (
    <div className='flex flex-wrap gap-2' data-testid='model-status-list'>
      {q.data.models.map((m) => (
        <StatusBadge
          key={m.model}
          variant={MODEL_VARIANT[m.status]}
          label={`${m.model} · ${stateLabel[m.status]}`}
          showDot
          copyable={false}
        />
      ))}
    </div>
  )
}

// ---- page --------------------------------------------------------------------

function HealthSections() {
  const { t } = useTranslation()
  const label = useReasonLabel()
  const q = useQuery(probesQuery)
  const nowSec = Math.floor(Date.now() / 1000)

  if (q.isError) {
    const failure = (
      <SectionError
        testId='probes-error'
        title={t('Failed to read channel health')}
        error={q.error}
        onRetry={() => void q.refetch()}
      />
    )
    return (
      <>
        <TitledCard title={t('Expiring within 72 hours')}>{failure}</TitledCard>
        <TitledCard title={t('Plan window above 90%')}>{failure}</TitledCard>
        <TitledCard title={t('Not routable')}>{failure}</TitledCard>
      </>
    )
  }
  const data = q.data
  const body = (render: (d: NonNullable<typeof data>) => ReactNode) =>
    data ? render(data) : <Loading />
  const notice = data?.truncated === true && (
    <div
      className='border-warning/50 rounded-md border p-3 text-sm'
      data-testid='probes-partial'
    >
      <p>
        {t(
          'The channel list is capped; the health roll-up below is incomplete.'
        )}
      </p>
    </div>
  )
  const reasons = data ? reasonTotals(data.probes) : []

  return (
    <>
      {notice}
      <TitledCard title={t('Expiring within 72 hours')}>
        {body((d) => (
          <ExpiringList rows={expiringSoon(d.probes, nowSec)} nowSec={nowSec} />
        ))}
      </TitledCard>
      <TitledCard title={t('Plan window above 90%')}>
        {body((d) => (
          <WindowList rows={windowNearLimit(d.probes)} />
        ))}
      </TitledCard>
      <TitledCard
        title={t('Not routable')}
        description={
          reasons.length > 0
            ? reasons.map(([r, n]) => `${label(r)} ${n}`).join(' / ')
            : undefined
        }
      >
        {body((d) => (
          <UnroutableList rows={unroutable(d.probes)} />
        ))}
      </TitledCard>
    </>
  )
}

export function OpsOverviewPage() {
  const { t } = useTranslation()
  const summary = useQuery(usageSummaryQuery)

  let usage: ReactNode
  if (summary.isError) {
    usage = (
      <SectionError
        testId='usage-error'
        title={t('Failed to load channel usage')}
        error={summary.error}
        onRetry={() => void summary.refetch()}
      />
    )
  } else if (summary.data) {
    usage = (
      <UsageSection
        channels={summary.data.channels}
        truncated={summary.data.truncated}
      />
    )
  } else {
    usage = <Loading />
  }

  return (
    <div className='space-y-4'>
      <PageHeader
        title={t('Operations Overview')}
        description={t('Channel cost, plan health and public model status')}
      />
      <TitledCard
        title={t('Channel usage (30 days)')}
        description={t('Sorted by utilization: overused first, idle last')}
      >
        {usage}
      </TitledCard>
      <HealthSections />
      <TitledCard
        title={t('Public model status')}
        description={t('What the public status page shows')}
      >
        <ModelStatusSection />
      </TitledCard>
    </div>
  )
}
