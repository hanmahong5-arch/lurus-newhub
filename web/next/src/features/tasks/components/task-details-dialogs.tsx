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
import { ExternalLink } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { formatTimestampToDate } from '@/components/format'
import { StatusBadge } from '@/components/status-badge'
import { useQuota } from '@/hooks/use-quota'
import {
  DetailRow,
  DetailSection,
} from '@/features/usage-logs/components/detail-layout'

import {
  audioClips,
  mjActionLabel,
  mjStatus,
  safeHttpUrl,
  taskActionLabel,
  taskFailReason,
  taskResultUrl,
  taskStatus,
  type MjLog,
  type TaskLog,
} from '../lib/tasks'

function Link(props: { href: string; label: string }) {
  return (
    <a
      href={props.href}
      target='_blank'
      rel='noopener noreferrer'
      className='text-primary inline-flex items-center gap-1 hover:underline'
    >
      <ExternalLink className='size-3' aria-hidden='true' />
      {props.label}
    </a>
  )
}

function when(value: number, unit: 'seconds' | 'milliseconds'): string {
  return value > 0 ? formatTimestampToDate(value, unit) : '-'
}

export function TaskDetailsDialog(props: {
  log: TaskLog | null
  onClose: () => void
}) {
  const { t } = useTranslation()
  const { formatLogQuota } = useQuota()
  const log = props.log
  const status = log ? taskStatus(log.status) : null
  const resultUrl = log ? taskResultUrl(log) : null
  const clips = log ? audioClips(log.data) : []
  const failReason = log ? taskFailReason(log) : ''

  return (
    <Dialog
      open={log !== null}
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={
        <span className='flex items-center gap-2'>
          {t('Task details')}
          {status && (
            <StatusBadge
              label={t(status.label)}
              variant={status.variant}
              size='sm'
              copyable={false}
            />
          )}
        </span>
      }
      description={t('The full record of this asynchronous job')}
      contentClassName='min-w-0 overflow-hidden sm:max-w-2xl'
      contentHeight='min(72dvh, 720px)'
    >
      {log && (
        <div className='space-y-3' data-testid='task-details'>
          <DetailSection label={t('Basic information')}>
            <DetailRow label={t('Task ID')} value={log.task_id || '-'} mono />
            <DetailRow label={t('Platform')} value={log.platform || '-'} mono />
            <DetailRow
              label={t('Action')}
              value={t(taskActionLabel(log.action))}
            />
            <DetailRow label={t('Progress')} value={log.progress || '-'} mono />
            <DetailRow
              label={t('Submit time')}
              value={when(log.submit_time, 'seconds')}
              mono
            />
            <DetailRow
              label={t('Start time')}
              value={when(log.start_time, 'seconds')}
              mono
            />
            <DetailRow
              label={t('Finish time')}
              value={when(log.finish_time, 'seconds')}
              mono
            />
            {log.quota > 0 && (
              <DetailRow
                label={t('Cost')}
                value={formatLogQuota(log.quota)}
                mono
              />
            )}
            {log.properties.origin_model_name && (
              <DetailRow
                label={t('Requested model')}
                value={log.properties.origin_model_name}
                mono
              />
            )}
            {log.properties.upstream_model_name && (
              <DetailRow
                label={t('Routed to')}
                value={log.properties.upstream_model_name}
                mono
              />
            )}
            {log.request_id && (
              <DetailRow label={t('Request ID')} value={log.request_id} mono />
            )}
          </DetailSection>

          {log.properties.input && (
            <DetailSection label={t('Input')}>
              <p
                data-testid='task-input'
                className='text-xs break-words whitespace-pre-wrap'
              >
                {log.properties.input}
              </p>
            </DetailSection>
          )}

          {(resultUrl || clips.length > 0) && (
            <DetailSection label={t('Result')}>
              {resultUrl && (
                <DetailRow
                  label={t('Link')}
                  value={<Link href={resultUrl} label={t('Open result')} />}
                />
              )}
              {clips.map((clip) => (
                <DetailRow
                  key={clip.audioUrl}
                  label={t('Audio')}
                  value={
                    <Link
                      href={clip.audioUrl}
                      label={clip.title || t('Open audio')}
                    />
                  }
                />
              ))}
            </DetailSection>
          )}

          {failReason && (
            <DetailSection label={t('Fail reason')} variant='danger'>
              <p
                data-testid='task-fail-reason'
                className='text-xs break-words whitespace-pre-wrap'
              >
                {failReason}
              </p>
            </DetailSection>
          )}
        </div>
      )}
    </Dialog>
  )
}

export function MjDetailsDialog(props: {
  log: MjLog | null
  onClose: () => void
}) {
  const { t } = useTranslation()
  const { formatLogQuota } = useQuota()
  const log = props.log
  const status = log ? mjStatus(log.status) : null
  const image = log ? safeHttpUrl(log.image_url) : null
  const video = log ? safeHttpUrl(log.video_url) : null

  return (
    <Dialog
      open={log !== null}
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={
        <span className='flex items-center gap-2'>
          {t('Drawing details')}
          {status && (
            <StatusBadge
              label={t(status.label)}
              variant={status.variant}
              size='sm'
              copyable={false}
            />
          )}
        </span>
      }
      description={t('The full record of this drawing job')}
      contentClassName='min-w-0 overflow-hidden sm:max-w-2xl'
      contentHeight='min(72dvh, 720px)'
    >
      {log && (
        <div className='space-y-3' data-testid='mj-details'>
          <DetailSection label={t('Basic information')}>
            <DetailRow label={t('Task ID')} value={log.mj_id || '-'} mono />
            <DetailRow
              label={t('Type')}
              value={t(mjActionLabel(log.action))}
            />
            <DetailRow label={t('Progress')} value={log.progress || '-'} mono />
            <DetailRow
              label={t('Submit time')}
              value={when(log.submit_time, 'milliseconds')}
              mono
            />
            <DetailRow
              label={t('Start time')}
              value={when(log.start_time, 'milliseconds')}
              mono
            />
            <DetailRow
              label={t('Finish time')}
              value={when(log.finish_time, 'milliseconds')}
              mono
            />
            {log.quota > 0 && (
              <DetailRow
                label={t('Cost')}
                value={formatLogQuota(log.quota)}
                mono
              />
            )}
            {image && (
              <DetailRow
                label={t('Image')}
                value={<Link href={image} label={t('Open image')} />}
              />
            )}
            {video && (
              <DetailRow
                label={t('Video')}
                value={<Link href={video} label={t('Open video')} />}
              />
            )}
          </DetailSection>

          {log.prompt && (
            <DetailSection label={t('Prompt')}>
              <p
                data-testid='mj-prompt'
                className='text-xs break-words whitespace-pre-wrap'
              >
                {log.prompt}
              </p>
              {log.prompt_en && log.prompt_en !== log.prompt && (
                <p className='text-muted-foreground text-xs break-words whitespace-pre-wrap'>
                  {log.prompt_en}
                </p>
              )}
            </DetailSection>
          )}

          {log.description && (
            <DetailSection label={t('Description')}>
              <p className='text-xs break-words whitespace-pre-wrap'>
                {log.description}
              </p>
            </DetailSection>
          )}

          {log.fail_reason && (
            <DetailSection label={t('Fail reason')} variant='danger'>
              <p
                data-testid='mj-fail-reason'
                className='text-xs break-words whitespace-pre-wrap'
              >
                {log.fail_reason}
              </p>
            </DetailSection>
          )}
        </div>
      )}
    </Dialog>
  )
}
