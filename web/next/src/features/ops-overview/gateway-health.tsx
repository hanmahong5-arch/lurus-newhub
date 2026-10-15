import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
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

import { gatewayHealthQuery } from './lib/api'
import type { GatewayHealth, GatewayRoute } from './lib/types'

const STATE_VARIANT: Record<string, StatusVariant> = {
  closed: 'success',
  half_open: 'warning',
  open: 'danger',
}

function formatTime(sec: number | undefined): string {
  return sec && sec > 0 ? new Date(sec * 1000).toLocaleString() : '--'
}

function StateBadge(props: { state: string }) {
  const { t } = useTranslation()
  const label: Record<string, string> = {
    closed: t('Breaker closed'),
    half_open: t('Breaker half-open'),
    open: t('Breaker open'),
  }
  return (
    <StatusBadge
      variant={STATE_VARIANT[props.state] ?? 'neutral'}
      label={label[props.state] ?? props.state}
      showDot
      copyable={false}
    />
  )
}

const SEVERITY: Record<string, number> = { open: 0, half_open: 1 }
const SEVERITY_RANK = (state: string) => SEVERITY[state] ?? 2

/** Open first, then half-open, then closed; ties by channel id. */
function bySeverity(a: GatewayRoute, b: GatewayRoute): number {
  return (
    SEVERITY_RANK(a.state) - SEVERITY_RANK(b.state) ||
    a.channel_id - b.channel_id
  )
}

function Summary(props: { label: string; value: number; testId: string }) {
  return (
    <div className='rounded-lg border p-3'>
      <div className='text-muted-foreground text-xs'>{props.label}</div>
      <div className='text-xl font-semibold' data-testid={props.testId}>
        {props.value.toLocaleString()}
      </div>
    </div>
  )
}

export function GatewayHealthBody(props: { data: GatewayHealth }) {
  const { t } = useTranslation()
  const { data } = props
  const routes = [...data.routes].sort(bySeverity)
  return (
    <div className='space-y-3' data-testid='gateway-health'>
      <div className='grid gap-3 sm:grid-cols-3'>
        <Summary
          testId='gateway-tracked'
          label={t('Channels tracked')}
          value={data.tracked}
        />
        <Summary
          testId='gateway-open'
          label={t('Breakers open')}
          value={data.open}
        />
        <Summary
          testId='gateway-half-open'
          label={t('Breakers half-open')}
          value={data.half_open}
        />
      </div>
      {(data.replica_scoped || data.lazy_registered) && (
        <ul
          className='text-muted-foreground list-disc space-y-1 ps-5 text-xs'
          data-testid='gateway-caveats'
        >
          {data.replica_scoped && (
            <li>
              {t(
                'Breaker state is kept per replica: this is the view of one pod.'
              )}
            </li>
          )}
          {data.lazy_registered && (
            <li>
              {t(
                'A channel appears only after it has carried traffic since this replica started; a missing channel is unknown, not healthy.'
              )}
            </li>
          )}
        </ul>
      )}
      {routes.length === 0 ? (
        <p
          className='text-muted-foreground py-3 text-sm'
          data-testid='gateway-empty'
        >
          {t(
            'No channel has carried traffic since this replica started, so no breaker state is known.'
          )}
        </p>
      ) : (
        <Table data-testid='gateway-routes'>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Channel')}</TableHead>
              <TableHead>{t('Breaker')}</TableHead>
              <TableHead className='text-right'>
                {t('Consecutive failures')}
              </TableHead>
              <TableHead>{t('Last failure')}</TableHead>
              <TableHead>{t('Probe eligible at')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {routes.map((r) => (
              <TableRow
                key={r.channel_id}
                data-testid={`gateway-route-${r.channel_id}`}
              >
                <TableCell>
                  {r.channel_name || t('Unnamed channel')}{' '}
                  <span className='text-muted-foreground'>#{r.channel_id}</span>
                  {r.provider && (
                    <span className='text-muted-foreground block text-xs'>
                      {r.provider}
                    </span>
                  )}
                </TableCell>
                <TableCell>
                  <StateBadge state={r.state} />
                </TableCell>
                <TableCell className='text-right tabular-nums'>
                  {r.consecutive_fails} / {r.threshold}
                </TableCell>
                <TableCell>{formatTime(r.last_fail_unix)}</TableCell>
                <TableCell>{formatTime(r.probe_eligible_unix)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}

/**
 * Live circuit-breaker state of every channel the relay has touched. Platform
 * root only (the caller gates it; the endpoint is root-only on the server).
 * A failed read is an error with a retry, never "no open breakers".
 */
export function GatewayHealthCard() {
  const { t } = useTranslation()
  const q = useQuery(gatewayHealthQuery)

  let body
  if (q.isError) {
    body = (
      <div data-testid='gateway-error'>
        <ErrorState
          className='min-h-[160px]'
          title={t('Failed to load gateway health')}
          description={
            q.error instanceof Error && q.error.message
              ? q.error.message
              : undefined
          }
          onRetry={() => void q.refetch()}
        />
      </div>
    )
  } else if (!q.data) {
    body = (
      <div className='space-y-2' aria-busy='true' data-testid='gateway-loading'>
        <Skeleton className='h-8 w-full' />
        <Skeleton className='h-8 w-full' />
      </div>
    )
  } else {
    body = <GatewayHealthBody data={q.data} />
  }

  return (
    <TitledCard
      title={t('Gateway health')}
      description={t('Live circuit-breaker state per channel (platform root)')}
    >
      {body}
    </TitledCard>
  )
}
