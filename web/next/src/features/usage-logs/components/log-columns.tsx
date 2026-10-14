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
import type { ColumnDef } from '@tanstack/react-table'
import { GitBranch, KeyRound } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { GroupBadge } from '@/components/group-badge'
import { formatTimestampToDate } from '@/components/format'
import { StatusBadge } from '@/components/status-badge'
import { useQuota } from '@/hooks/use-quota'
import { usdToQuota } from '@/lib/money'
import { cn } from '@/lib/utils'

import {
  channelChain,
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
import { CostCell, Dash, TypeBadge } from './log-cells'
import { ModelBadge } from './model-badge'
import { StreamTpsCell, TimingMetricsCell } from './timing-metrics-cell'

interface UseLogColumnsOptions {
  /** Platform staff: channel column and the routing trace. */
  isStaff: boolean
  /** Tenant-wide view: show who made each call. */
  tenantWide: boolean
  /** Open the detail sheet for one row. */
  onOpen: (row: UsageLog) => void
}

export function useLogColumns(opts: UseLogColumnsOptions): ColumnDef<UsageLog>[] {
  const { t } = useTranslation()
  const { formatLogQuota, config } = useQuota()
  const { isStaff, tenantWide, onOpen } = opts

  return useMemo(() => {
    // Per-call prices are recorded in USD; the money module turns them into
    // the operator's display currency through quota units.
    const formatUsd = (usd: number) =>
      config === null ? '--' : formatLogQuota(usdToQuota(usd, config))

    const columns: ColumnDef<UsageLog>[] = [
      {
        accessorKey: 'created_at',
        header: t('Time'),
        enableHiding: false,
        size: 170,
        cell: ({ row }) => (
          <div className='flex min-w-0 flex-col gap-0.5'>
            <span className='truncate font-mono text-xs tabular-nums'>
              {formatTimestampToDate(row.original.created_at)}
            </span>
            <TypeBadge type={row.original.type} className='-ml-1.5 !text-xs' />
          </div>
        ),
        meta: { label: t('Time') },
      },
    ]

    if (isStaff) {
      columns.push({
        id: 'channel',
        header: t('Channel'),
        accessorFn: (r) => r.channel,
        cell: ({ row }) => {
          const log = row.original
          if (!isDisplayableLogType(log.type) || !log.channel) return <Dash />
          const chain = channelChain(log)
          return (
            <div className='flex max-w-[160px] flex-col gap-0.5'>
              <span className='inline-flex items-center gap-1'>
                <StatusBadge
                  label={`#${log.channel}`}
                  autoColor={String(log.channel)}
                  copyText={String(log.channel)}
                  size='sm'
                  className='font-mono'
                />
                {chain && (
                  <span title={`${t('Retry chain')}: ${chain}`}>
                    <GitBranch
                      className='size-3.5 text-amber-500'
                      aria-label={t('Retry chain')}
                    />
                  </span>
                )}
              </span>
              {log.channel_name && (
                <span className='text-muted-foreground/70 truncate text-xs'>
                  {log.channel_name}
                </span>
              )}
            </div>
          )
        },
        meta: { label: t('Channel') },
      })
    }

    if (tenantWide) {
      columns.push({
        id: 'user',
        header: t('User'),
        accessorFn: (r) => r.username,
        cell: ({ row }) =>
          row.original.username ? (
            <span className='text-muted-foreground block max-w-[110px] truncate text-sm'>
              {row.original.username}
            </span>
          ) : (
            <Dash />
          ),
        meta: { label: t('User') },
      })
    }

    columns.push(
      {
        accessorKey: 'token_name',
        header: t('Token'),
        size: 160,
        cell: ({ row }) => {
          const log = row.original
          if (!isDisplayableLogType(log.type) || !log.token_name) return <Dash />
          const ratio = groupRatio(log)
          return (
            <div className='flex max-w-[200px] flex-col gap-0.5'>
              <StatusBadge
                label={log.token_name}
                icon={KeyRound}
                copyText={log.token_name}
                size='sm'
                className='border-border/60 bg-muted/30 text-foreground h-6 max-w-full gap-1.5 overflow-hidden rounded-md border px-2 py-0.5'
              />
              {(log.group || ratio !== null) && (
                <span className='block max-w-full truncate text-xs leading-none'>
                  {log.group && (
                    <GroupBadge
                      group={log.group}
                      type='text'
                      size='sm'
                      className='inline align-baseline text-xs leading-none'
                    />
                  )}
                  {log.group && ratio !== null ? ' ' : null}
                  {ratio !== null && (
                    <span className='text-muted-foreground/60 tabular-nums'>
                      {formatRatio(ratio)}x
                    </span>
                  )}
                </span>
              )}
            </div>
          )
        },
        meta: { label: t('Token') },
      },
      {
        accessorKey: 'model_name',
        header: t('Model'),
        cell: ({ row }) => {
          const log = row.original
          if (!isDisplayableLogType(log.type) || !log.model_name) return <Dash />
          const m = mappedModel(log)
          return <ModelBadge modelName={m.name} upstreamModel={m.upstream} />
        },
        meta: { label: t('Model'), mobileTitle: true },
      },
      {
        accessorKey: 'is_stream',
        header: t('Stream'),
        cell: ({ row }) => {
          const log = row.original
          if (!isTimingLogType(log.type)) return <Dash />
          return (
            <StreamTpsCell
              isStream={log.is_stream}
              tokensPerSecond={tokensPerSecond(log)}
            />
          )
        },
        meta: { label: t('Stream') },
      },
      {
        accessorKey: 'prompt_tokens',
        header: t('Tokens in / out'),
        cell: ({ row }) => {
          const log = row.original
          if (!isDisplayableLogType(log.type)) return <Dash />
          const tk = tokenBreakdown(log)
          if (tk.prompt === 0 && tk.completion === 0) return <Dash />
          return (
            <div className='flex flex-col gap-0.5'>
              <span className='font-mono text-xs font-medium tabular-nums'>
                {fmtNum(tk.prompt)} / {fmtNum(tk.completion)}
              </span>
              {(tk.cacheRead > 0 || tk.cacheWrite > 0) && (
                <div className='flex items-center gap-1 text-[11px]'>
                  {tk.cacheRead > 0 && (
                    <span className='text-muted-foreground/60'>
                      {t('Cache')}↓ {fmtNum(tk.cacheRead)}
                    </span>
                  )}
                  {tk.cacheWrite > 0 && (
                    <span className='text-muted-foreground/60'>
                      ↑ {fmtNum(tk.cacheWrite)}
                    </span>
                  )}
                </div>
              )}
            </div>
          )
        },
        meta: { label: t('Tokens in / out') },
      },
      {
        accessorKey: 'quota',
        header: t('Cost'),
        cell: ({ row }) =>
          isDisplayableLogType(row.original.type) ? (
            <CostCell row={row.original} />
          ) : (
            <Dash />
          ),
        meta: { label: t('Cost') },
      },
      {
        accessorKey: 'use_time',
        header: t('Timing'),
        cell: ({ row }) => {
          const log = row.original
          if (!isTimingLogType(log.type)) return <Dash />
          return (
            <TimingMetricsCell
              durationSec={durationSec(log)}
              completionTokens={log.completion_tokens}
              firstTokenMs={firstTokenMs(log)}
              isStream={log.is_stream}
            />
          )
        },
        meta: { label: t('Timing') },
      },
      {
        accessorKey: 'content',
        header: t('Details'),
        size: 200,
        maxSize: 220,
        cell: ({ row }) => {
          const log = row.original
          const segments = detailSegments(log, t, formatUsd)
          const primary = segments[0]
          let tone = 'text-foreground'
          if (primary?.muted) tone = 'text-muted-foreground/60'
          else if (primary?.danger) tone = 'text-red-600 dark:text-red-400'
          return (
            <button
              type='button'
              className='group flex max-w-[200px] items-center gap-1 text-left text-xs'
              title={t('Click to view full details')}
              onClick={(e) => {
                e.stopPropagation()
                onOpen(log)
              }}
            >
              {primary ? (
                <span
                  className={cn(
                    'truncate leading-snug group-hover:underline',
                    tone
                  )}
                >
                  {primary.text}
                  {segments.length > 1 && (
                    <span className='text-muted-foreground/40 ml-0.5'>
                      +{segments.length - 1}
                    </span>
                  )}
                </span>
              ) : (
                <span className='text-muted-foreground/60 group-hover:underline'>
                  {t('View details')}
                </span>
              )}
            </button>
          )
        },
        meta: { label: t('Details') },
      }
    )
    return columns
  }, [t, isStaff, tenantWide, onOpen, formatLogQuota, config])
}
