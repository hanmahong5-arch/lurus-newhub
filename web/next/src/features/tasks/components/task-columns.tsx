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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { formatTimestampToDate } from '@/components/format'
import { StatusBadge } from '@/components/status-badge'
import { useQuota } from '@/hooks/use-quota'

import {
  durationSeconds,
  mjActionLabel,
  mjActionVariant,
  mjStatus,
  safeHttpUrl,
  taskActionLabel,
  taskFailReason,
  taskResultUrl,
  taskStatus,
  type MjLog,
  type TaskLog,
} from '../lib/tasks'
import {
  Dash,
  DetailsCell,
  DurationPill,
  ExternalLinkButton,
  ProgressPill,
} from './task-cells'

export function useTaskColumns(
  onOpen: (row: TaskLog) => void
): ColumnDef<TaskLog>[] {
  const { t } = useTranslation()
  const { formatLogQuota } = useQuota()
  return useMemo(
    () => [
      {
        accessorKey: 'submit_time',
        header: t('Submit time'),
        enableHiding: false,
        size: 180,
        cell: ({ row }) => (
          <div className='flex min-w-0 flex-col gap-0.5'>
            <span className='truncate font-mono text-xs tabular-nums'>
              {formatTimestampToDate(row.original.submit_time)}
            </span>
            {row.original.finish_time > 0 ? (
              <span className='text-muted-foreground/60 truncate font-mono text-[11px] tabular-nums'>
                {formatTimestampToDate(row.original.finish_time)}
              </span>
            ) : (
              <span className='text-muted-foreground/50 text-[11px]'>-</span>
            )}
          </div>
        ),
        meta: { label: t('Submit time') },
      },
      {
        accessorKey: 'task_id',
        header: t('Task ID'),
        cell: ({ row }) => {
          const log = row.original
          if (!log.task_id) return <Dash />
          return (
            <div className='flex max-w-[190px] flex-col gap-0.5'>
              <StatusBadge
                label={log.task_id}
                copyText={log.task_id}
                variant='neutral'
                size='sm'
                className='border-border/60 bg-muted/30 !text-foreground max-w-full truncate rounded-md border px-1.5 py-0.5 font-mono'
              />
              <span className='text-muted-foreground/60 truncate text-[11px]'>
                {log.platform} · {t(taskActionLabel(log.action))}
              </span>
            </div>
          )
        },
        meta: { label: t('Task ID'), mobileTitle: true },
      },
      {
        id: 'duration',
        header: t('Duration'),
        accessorFn: (r) => durationSeconds(r.submit_time, r.finish_time, 'seconds'),
        cell: ({ row }) => (
          <DurationPill
            seconds={durationSeconds(
              row.original.submit_time,
              row.original.finish_time,
              'seconds'
            )}
            warnAfterSec={300}
          />
        ),
        meta: { label: t('Duration') },
      },
      {
        accessorKey: 'status',
        header: t('Status'),
        cell: ({ row }) => {
          const s = taskStatus(row.original.status)
          return (
            <StatusBadge
              label={t(s.label)}
              variant={s.variant}
              size='sm'
              copyable={false}
              className='-ml-1.5'
            />
          )
        },
        meta: { label: t('Status'), mobileBadge: true },
      },
      {
        accessorKey: 'progress',
        header: t('Progress'),
        cell: ({ row }) => <ProgressPill value={row.original.progress} />,
        meta: { label: t('Progress') },
      },
      {
        accessorKey: 'quota',
        header: t('Cost'),
        cell: ({ row }) =>
          row.original.quota > 0 ? (
            <span className='font-mono text-xs tabular-nums'>
              {formatLogQuota(row.original.quota)}
            </span>
          ) : (
            <Dash />
          ),
        meta: { label: t('Cost') },
      },
      {
        id: 'result',
        header: t('Result'),
        cell: ({ row }) => {
          const url = taskResultUrl(row.original)
          return url ? (
            <ExternalLinkButton href={url} label={t('Open result')} />
          ) : (
            <Dash />
          )
        },
        meta: { label: t('Result') },
      },
      {
        id: 'details',
        header: t('Details'),
        size: 220,
        cell: ({ row }) => (
          <DetailsCell
            failReason={taskFailReason(row.original)}
            label={t('View details')}
            onOpen={() => onOpen(row.original)}
          />
        ),
        meta: { label: t('Details') },
      },
    ],
    [t, formatLogQuota, onOpen]
  )
}

export function useMjColumns(
  onOpen: (row: MjLog) => void
): ColumnDef<MjLog>[] {
  const { t } = useTranslation()
  const { formatLogQuota } = useQuota()
  return useMemo(
    () => [
      {
        accessorKey: 'submit_time',
        header: t('Submit time'),
        enableHiding: false,
        size: 180,
        cell: ({ row }) => (
          <div className='flex min-w-0 flex-col gap-0.5'>
            <span className='truncate font-mono text-xs tabular-nums'>
              {formatTimestampToDate(row.original.submit_time, 'milliseconds')}
            </span>
            {row.original.finish_time > 0 ? (
              <span className='text-muted-foreground/60 truncate font-mono text-[11px] tabular-nums'>
                {formatTimestampToDate(row.original.finish_time, 'milliseconds')}
              </span>
            ) : (
              <span className='text-muted-foreground/50 text-[11px]'>-</span>
            )}
          </div>
        ),
        meta: { label: t('Submit time') },
      },
      {
        accessorKey: 'action',
        header: t('Type'),
        cell: ({ row }) => (
          <StatusBadge
            label={t(mjActionLabel(row.original.action))}
            variant={mjActionVariant(row.original.action)}
            size='sm'
            copyable={false}
          />
        ),
        meta: { label: t('Type'), mobileTitle: true },
      },
      {
        accessorKey: 'mj_id',
        header: t('Task ID'),
        cell: ({ row }) =>
          row.original.mj_id ? (
            <StatusBadge
              label={row.original.mj_id}
              copyText={row.original.mj_id}
              variant='neutral'
              size='sm'
              className='border-border/60 bg-muted/30 !text-foreground max-w-[170px] truncate rounded-md border px-1.5 py-0.5 font-mono'
            />
          ) : (
            <Dash />
          ),
        meta: { label: t('Task ID') },
      },
      {
        id: 'duration',
        header: t('Duration'),
        accessorFn: (r) =>
          durationSeconds(r.submit_time, r.finish_time, 'milliseconds'),
        cell: ({ row }) => (
          <DurationPill
            seconds={durationSeconds(
              row.original.submit_time,
              row.original.finish_time,
              'milliseconds'
            )}
            warnAfterSec={300}
          />
        ),
        meta: { label: t('Duration') },
      },
      {
        accessorKey: 'status',
        header: t('Status'),
        cell: ({ row }) => {
          const s = mjStatus(row.original.status)
          return (
            <StatusBadge
              label={t(s.label)}
              variant={s.variant}
              size='sm'
              copyable={false}
              className='-ml-1.5'
            />
          )
        },
        meta: { label: t('Status'), mobileBadge: true },
      },
      {
        accessorKey: 'progress',
        header: t('Progress'),
        cell: ({ row }) => <ProgressPill value={row.original.progress} />,
        meta: { label: t('Progress') },
      },
      {
        accessorKey: 'quota',
        header: t('Cost'),
        cell: ({ row }) =>
          row.original.quota > 0 ? (
            <span className='font-mono text-xs tabular-nums'>
              {formatLogQuota(row.original.quota)}
            </span>
          ) : (
            <Dash />
          ),
        meta: { label: t('Cost') },
      },
      {
        id: 'result',
        header: t('Result'),
        cell: ({ row }) => {
          const image = safeHttpUrl(row.original.image_url)
          const video = safeHttpUrl(row.original.video_url)
          if (!image && !video) return <Dash />
          return (
            <div className='flex flex-col gap-0.5'>
              {image && (
                <ExternalLinkButton href={image} label={t('Open image')} />
              )}
              {video && (
                <ExternalLinkButton href={video} label={t('Open video')} />
              )}
            </div>
          )
        },
        meta: { label: t('Result') },
      },
      {
        id: 'details',
        header: t('Details'),
        size: 220,
        cell: ({ row }) => (
          <DetailsCell
            failReason={row.original.fail_reason}
            label={t('View details')}
            onOpen={() => onOpen(row.original)}
          />
        ),
        meta: { label: t('Details') },
      },
    ],
    [t, formatLogQuota, onOpen]
  )
}
