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
import type { Table } from '@tanstack/react-table'
import { ChevronRight, Database, KeyRound } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { formatTimestampToDate } from '@/components/format'
import { GroupBadge } from '@/components/group-badge'
import { StatusBadge } from '@/components/status-badge'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { useQuota } from '@/hooks/use-quota'
import { usdToQuota } from '@/lib/money'
import { cn } from '@/lib/utils'

import {
  LOG_TYPE,
  detailSegments,
  durationSec,
  firstTokenMs,
  fmtNum,
  formatRatio,
  groupRatio,
  isDisplayableLogType,
  isTimingLogType,
  mappedModel,
  tokenBreakdown,
  tokensPerSecond,
} from '../lib/log-view'
import type { UsageLog } from '../lib/logs'
import { CostCell, TypeBadge } from './log-cells'
import { ModelBadge } from './model-badge'
import { StreamTpsCell, TimingMetricsCell } from './timing-metrics-cell'

const TINT: Record<number, string> = {
  [LOG_TYPE.ERROR]:
    'bg-rose-50/40 dark:bg-rose-950/20 border-rose-200/50 dark:border-rose-900/30',
  [LOG_TYPE.REFUND]:
    'bg-blue-50/30 dark:bg-blue-950/15 border-blue-200/50 dark:border-blue-900/30',
}

interface LogMobileListProps {
  table: Table<UsageLog>
  isLoading?: boolean
  isStaff: boolean
  onOpen: (row: UsageLog) => void
}

function Skeletons() {
  const { t } = useTranslation()
  return (
    <div
      role='status'
      aria-label={t('Loading')}
      aria-busy='true'
      className='min-w-0 space-y-3'
    >
      {[1, 2, 3].map((i) => (
        <div
          key={i}
          className='border-border/60 bg-card space-y-3 rounded-xl border p-3.5'
        >
          <div className='flex items-center justify-between gap-3'>
            <Skeleton className='h-5 w-40 rounded-md' />
            <Skeleton className='h-5 w-16 rounded-md' />
          </div>
          <div className='grid grid-cols-2 gap-x-4 gap-y-2'>
            {[1, 2, 3, 4].map((j) => (
              <div key={j} className='min-w-0 space-y-1'>
                <Skeleton className='h-3 w-10 rounded' />
                <Skeleton className='h-4 w-full rounded' />
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}

function LogCard(props: {
  log: UsageLog
  visible: Set<string>
  isStaff: boolean
  onOpen: (row: UsageLog) => void
}) {
  const { t } = useTranslation()
  const { formatLogQuota, config } = useQuota()
  const { log, visible } = props
  const displayable = isDisplayableLogType(log.type)
  const timing = isTimingLogType(log.type)
  const model = mappedModel(log)
  const tokens = tokenBreakdown(log)
  const ratio = groupRatio(log)
  const formatUsd = (usd: number) =>
    config === null ? '--' : formatLogQuota(usdToQuota(usd, config))
  const preview = detailSegments(log, t, formatUsd)[0]
  const showTokens =
    displayable &&
    visible.has('prompt_tokens') &&
    (tokens.prompt > 0 || tokens.completion > 0)
  const meta: Array<{ id: string; label: string; node: React.ReactNode }> = []
  if (visible.has('user') && log.username) {
    meta.push({
      id: 'user',
      label: t('User'),
      node: <span className='truncate'>{log.username}</span>,
    })
  }
  if (props.isStaff && visible.has('channel') && log.channel > 0) {
    meta.push({
      id: 'channel',
      label: t('Channel'),
      node: (
        <span className='truncate font-mono'>
          {log.channel_name ? `${log.channel_name} ` : ''}#{log.channel}
        </span>
      ),
    })
  }
  if (displayable && visible.has('token_name') && log.token_name) {
    meta.push({
      id: 'token',
      label: t('Token'),
      node: (
        <StatusBadge
          label={log.token_name}
          copyable={false}
          icon={KeyRound}
          className='border-border/60 bg-muted/30 text-foreground max-w-full rounded-md border px-1.5 py-0.5 text-sm'
        />
      ),
    })
  }
  if (displayable && visible.has('token_name') && log.group) {
    meta.push({
      id: 'group',
      label: t('Group'),
      node: (
        <GroupBadge
          group={log.group}
          type='text'
          className='max-w-full text-sm'
        />
      ),
    })
  }

  return (
    <div
      className={cn(
        'border-border/60 bg-card min-w-0 rounded-xl border p-3.5',
        TINT[log.type]
      )}
    >
      <button
        type='button'
        data-testid='log-card'
        aria-label={`${t('View details')}: ${log.model_name || formatTimestampToDate(log.created_at)}`}
        onClick={() => props.onOpen(log)}
        className='block w-full min-w-0 space-y-2.5 text-left text-sm leading-5'
      >
        <div className='flex min-w-0 flex-wrap items-start gap-x-3 gap-y-2'>
          {displayable && visible.has('model_name') && log.model_name && (
            <div className='min-w-0 flex-[1_1_10rem]'>
              <ModelBadge
                modelName={model.name}
                upstreamModel={model.upstream}
                wrapText
                copyable={false}
              />
            </div>
          )}
          {displayable && visible.has('quota') && (
            <div className='ml-auto max-w-full min-w-0 self-center'>
              <CostCell row={log} align='right' />
            </div>
          )}
        </div>
        <div className='grid min-w-0 grid-cols-2 items-stretch gap-x-3'>
          {visible.has('created_at') && (
            <div className='flex min-w-0 flex-col items-start justify-between gap-1'>
              <TypeBadge type={log.type} className='px-0 text-xs' />
              <span className='text-muted-foreground text-xs tabular-nums'>
                {formatTimestampToDate(log.created_at)}
              </span>
            </div>
          )}
          {timing && (visible.has('use_time') || visible.has('is_stream')) && (
            <div className='col-start-2 flex min-w-0 flex-col items-end gap-1'>
              {visible.has('is_stream') && (
                <StreamTpsCell
                  compact
                  className='justify-end'
                  isStream={log.is_stream}
                  tokensPerSecond={tokensPerSecond(log)}
                />
              )}
              {visible.has('use_time') && (
                <TimingMetricsCell
                  indicator='dot'
                  compact
                  className='justify-end'
                  durationSec={durationSec(log)}
                  completionTokens={log.completion_tokens}
                  firstTokenMs={firstTokenMs(log)}
                  isStream={log.is_stream}
                />
              )}
            </div>
          )}
        </div>
        {meta.length > 0 && (
          <div className='grid min-w-0 grid-cols-2 gap-x-4 gap-y-1'>
            {meta.map((m) => (
              <div key={m.id} className='flex min-w-0 items-center gap-2'>
                <span className='text-muted-foreground max-w-[40%] shrink-0 text-xs'>
                  {m.label}
                </span>
                {m.node}
              </div>
            ))}
            {ratio !== null && visible.has('token_name') && (
              <div className='text-muted-foreground col-span-2 text-xs'>
                {t('Group ratio')}: {formatRatio(ratio)}x
              </div>
            )}
          </div>
        )}
        {showTokens && (
          <div className='text-muted-foreground flex flex-wrap gap-x-3 gap-y-1 text-xs'>
            <span>
              {t('Input')}{' '}
              <span className='text-foreground tabular-nums'>
                {fmtNum(tokens.prompt)}
              </span>
            </span>
            <span>
              {t('Output')}{' '}
              <span className='text-foreground tabular-nums'>
                {fmtNum(tokens.completion)}
              </span>
            </span>
            {tokens.cacheRead > 0 && (
              <span>
                {t('Cache')} ↓ {fmtNum(tokens.cacheRead)}
              </span>
            )}
            {tokens.cacheWrite > 0 && (
              <span>
                {t('Cache')} ↑ {fmtNum(tokens.cacheWrite)}
              </span>
            )}
          </div>
        )}
        {visible.has('content') && (
          <div className='relative flex items-center justify-between gap-2 border-t pt-2 text-xs'>
            <span
              className={cn(
                'line-clamp-2 min-w-0 [overflow-wrap:anywhere]',
                preview?.danger && 'text-red-600 dark:text-red-400',
                preview?.muted && 'text-muted-foreground/60'
              )}
            >
              {preview?.text ?? t('View details')}
            </span>
            <ChevronRight
              aria-hidden='true'
              className='text-primary size-4 shrink-0'
            />
          </div>
        )}
      </button>
    </div>
  )
}

/** The phone layout of the usage table: one tappable card per call. */
export function LogMobileList(props: LogMobileListProps) {
  const { t } = useTranslation()
  if (props.isLoading) return <Skeletons />

  const rows = props.table.getRowModel().rows
  if (rows.length === 0) {
    return (
      <div className='rounded-lg border p-6'>
        <Empty className='border-none p-0'>
          <EmptyHeader>
            <EmptyMedia variant='icon'>
              <Database className='size-6' />
            </EmptyMedia>
            <EmptyTitle>{t('No logs in this window')}</EmptyTitle>
            <EmptyDescription>
              {t('Logs appear here once API calls are made.')}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      </div>
    )
  }

  return (
    <div className='min-w-0 space-y-3'>
      {rows.map((row) => (
        <LogCard
          key={row.id}
          log={row.original}
          visible={new Set(row.getVisibleCells().map((c) => c.column.id))}
          isStaff={props.isStaff}
          onOpen={props.onOpen}
        />
      ))}
    </div>
  )
}
