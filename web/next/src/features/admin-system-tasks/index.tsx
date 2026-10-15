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
import type { TFunction } from 'i18next'
import {
  Activity,
  LogIn,
  Pause,
  Play,
  RefreshCw,
  ShieldAlert,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import {
  formatTimestampRelative,
  formatTimestampToDate,
} from '@/components/format'
import { LoadingState } from '@/components/loading-state'
import { SectionPageLayout } from '@/components/section-page-layout'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toApiError } from '@/lib/api'
import { buildLoginUrl } from '@/lib/auth'

import { systemTasksQueryOptions, type SystemTask } from './api'
import {
  ERROR_POLL_MS,
  POLL_MS,
  errorStatus,
  formatInterval,
  isTerminalRefusal,
} from './lib'

const STATE_VARIANT: Record<string, StatusVariant> = {
  ok: 'success',
  overdue: 'danger',
  standby: 'neutral',
}

function stateLabel(state: string, t: TFunction): string {
  // Literal t() calls so the key extractor sees every label.
  switch (state) {
    case 'ok':
      return t('On schedule')
    case 'overdue':
      return t('Overdue')
    case 'standby':
      return t('Standby')
    default:
      return state
  }
}

function standbyReasonLabel(reason: string, t: TFunction): string {
  switch (reason) {
    case 'follower':
      return t('Follower replica, not the leader')
    case 'disabled':
      return t('Disabled by an administrator')
    default:
      return reason
  }
}

function overdueHeadline(overdue: number, t: TFunction): string {
  if (overdue === 0) return t('All tasks on schedule')
  if (overdue === 1) return t('{{count}} task overdue', { count: overdue })
  return t('{{count}} tasks overdue', { count: overdue })
}

function TaskStateBadge(props: { task: SystemTask }) {
  const { t } = useTranslation()
  const { task } = props
  const label = stateLabel(task.state, t)
  const reason =
    task.state === 'standby' && task.standby_reason
      ? standbyReasonLabel(task.standby_reason, t)
      : null
  return (
    <div className='flex flex-wrap items-center gap-x-2 gap-y-0.5'>
      <StatusBadge
        label={label}
        variant={STATE_VARIANT[task.state] ?? 'neutral'}
        copyable={false}
        data-testid={`system-tasks-state-${task.name}`}
      />
      {reason != null && (
        <span
          className='text-muted-foreground text-xs'
          data-testid={`system-tasks-reason-${task.name}`}
        >
          ({reason})
        </span>
      )}
    </div>
  )
}

function LastSuccess(props: { task: SystemTask }) {
  const { t } = useTranslation()
  const at = props.task.last_success_at
  if (!at || at <= 0) {
    // A standby task was never this replica's job; an active one that has
    // never succeeded is worth saying out loud.
    return (
      <span className='text-muted-foreground'>
        {props.task.state === 'standby' ? '—' : t('Never')}
      </span>
    )
  }
  return (
    <div className='flex flex-col'>
      <span className='font-mono text-xs'>{formatTimestampToDate(at)}</span>
      <span className='text-muted-foreground text-xs'>
        {formatTimestampRelative(at)}
      </span>
    </div>
  )
}

export function AdminSystemTasksPage() {
  const { t } = useTranslation()
  const [live, setLive] = useState(true)

  const query = useQuery({
    ...systemTasksQueryOptions,
    refetchInterval: (q) => {
      if (!live || isTerminalRefusal(q.state.error)) return false
      return q.state.status === 'error' ? ERROR_POLL_MS : POLL_MS
    },
  })

  const status = errorStatus(query.error)
  const forbidden = query.isError && status === 403
  const signedOut = query.isError && status === 401
  const failed = query.isError && !forbidden && !signedOut

  const tasks = query.data?.tasks ?? []
  const overdue = tasks.filter((x) => x.state === 'overdue').length
  const standby = tasks.filter((x) => x.state === 'standby').length
  const onSchedule = tasks.length - overdue - standby

  const showControls = !forbidden && !signedOut && !failed

  let body
  if (query.isPending) {
    body = <LoadingState />
  } else if (forbidden) {
    body = (
      <ErrorState
        icon={ShieldAlert}
        title={t('Root access required')}
        description={t(
          'You do not have permission to view background tasks. Contact a platform administrator.'
        )}
      />
    )
  } else if (signedOut) {
    body = (
      <div data-testid='system-tasks-signed-out'>
        <ErrorState
          icon={LogIn}
          title={t('Your session has expired')}
          description={t(
            'The server no longer recognises this session, so nothing on this page can be read. Sign in again to continue.'
          )}
          action={
            <Button
              size='sm'
              render={
                <a
                  href={buildLoginUrl(
                    window.location.pathname + window.location.search
                  )}
                  data-testid='system-tasks-sign-in'
                />
              }
            >
              {t('Sign in again')}
            </Button>
          }
        />
      </div>
    )
  } else if (failed) {
    // A failed read is an error, never an empty schedule: "no task is late"
    // would be a claim the page cannot make.
    body = (
      <div data-testid='system-tasks-error'>
        <ErrorState
          title={t('Task schedule unknown')}
          description={`${t(
            'The background-task endpoint did not answer. This is not a report that every job is on schedule, nothing about the schedule is known until the call succeeds. Retrying every 30 seconds.'
          )} ${toApiError(query.error).message}`}
          onRetry={() => void query.refetch()}
        />
      </div>
    )
  } else {
    const headline = overdueHeadline(overdue, t)
    body = (
      <div className='mx-auto flex w-full max-w-5xl flex-col gap-4'>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <div className='flex min-w-0 flex-col gap-0.5'>
            <h3
              className={
                overdue > 0
                  ? 'text-destructive text-base font-semibold'
                  : 'text-base font-semibold'
              }
              data-testid='system-tasks-headline'
            >
              {headline}
            </h3>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Periodic background jobs, this pod only, no cluster-wide aggregation'
              )}
            </p>
          </div>
          {tasks.length > 0 && (
            <div className='flex flex-wrap items-center gap-2'>
              <StatusBadge
                label={`${t('On schedule')} ${onSchedule}`}
                variant='success'
                copyable={false}
              />
              <StatusBadge
                label={`${t('Overdue')} ${overdue}`}
                variant={overdue > 0 ? 'danger' : 'neutral'}
                copyable={false}
              />
              <StatusBadge
                label={`${t('Standby')} ${standby}`}
                variant='neutral'
                copyable={false}
              />
            </div>
          )}
        </div>

        <Alert data-testid='system-tasks-caveat'>
          <Activity />
          <AlertDescription>
            {t(
              'This view is per-pod, it lists only the tasks this replica has registered and stamped. A standby task is either leader-gated (this replica does not currently hold the HA lease) or administratively disabled, which is expected and not a fault.'
            )}
          </AlertDescription>
        </Alert>

        {tasks.length === 0 ? (
          <div data-testid='system-tasks-empty'>
            <EmptyState
              icon={Activity}
              bordered
              title={t('No background tasks yet')}
              description={t(
                'No background task has registered on this pod yet.'
              )}
            />
          </div>
        ) : (
          <div className='overflow-x-auto rounded-lg border'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Task')}</TableHead>
                  <TableHead>{t('Interval')}</TableHead>
                  <TableHead>{t('Leader only')}</TableHead>
                  <TableHead>{t('Last success')}</TableHead>
                  <TableHead>{t('Status')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {tasks.map((task) => (
                  <TableRow
                    key={task.name}
                    data-testid={`system-tasks-row-${task.name}`}
                  >
                    <TableCell className='font-mono text-xs'>
                      {task.name}
                    </TableCell>
                    <TableCell className='text-muted-foreground font-mono text-xs'>
                      {formatInterval(task.interval_seconds)}
                    </TableCell>
                    <TableCell className='text-muted-foreground'>
                      {task.leader_only ? t('Yes') : t('No')}
                    </TableCell>
                    <TableCell>
                      <LastSuccess task={task} />
                    </TableCell>
                    <TableCell>
                      <TaskStateBadge task={task} />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}

        {query.data != null && (
          <p
            className='text-muted-foreground text-xs'
            data-testid='system-tasks-pod'
          >
            {t('Pod')} {query.data.pod} · {t('Leader')}{' '}
            {query.data.is_leader ? t('Yes') : t('No')}
          </p>
        )}
      </div>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Background tasks')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        {showControls && (
          <>
            <Button
              variant='outline'
              size='sm'
              onClick={() => void query.refetch()}
              disabled={query.isFetching}
            >
              <RefreshCw className={query.isFetching ? 'animate-spin' : ''} />
              {t('Refresh')}
            </Button>
            <Button
              variant='outline'
              size='sm'
              data-testid='system-tasks-live-toggle'
              aria-pressed={live}
              onClick={() => setLive((v) => !v)}
            >
              {live ? <Pause /> : <Play />}
              {live ? t('Pause live updates') : t('Resume live updates')}
            </Button>
          </>
        )}
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>{body}</SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
