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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { formatTimestampToDate } from '@/components/format'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'

import {
  REDACTED,
  parseDetails,
  type AuditRow,
  type AuditScope,
} from '../lib/audit'
import { DetailRow, DetailSection } from './detail-layout'

interface AuditDetailDialogProps {
  row: AuditRow | null
  scope: AuditScope
  onClose: () => void
}

function DetailsBody(props: { details: string }) {
  const { t } = useTranslation()
  const [raw, setRaw] = useState(false)
  const parsed = parseDetails(props.details)

  if (parsed.kind === 'empty') {
    return (
      <p className='text-muted-foreground text-xs'>
        {t('This event carries no extra details.')}
      </p>
    )
  }
  if (parsed.kind === 'text') {
    return (
      <p
        data-testid='audit-detail-text'
        className='text-xs break-all whitespace-pre-wrap'
      >
        {parsed.text}
      </p>
    )
  }
  return (
    <div className='space-y-2'>
      <div className='flex justify-end'>
        <Button
          type='button'
          variant='ghost'
          size='xs'
          onClick={() => setRaw((v) => !v)}
        >
          {raw ? t('Show fields') : t('Show raw JSON')}
        </Button>
      </div>
      {raw ? (
        <pre
          data-testid='audit-detail-raw'
          className='bg-muted max-h-72 overflow-auto rounded-md p-2 text-xs break-all whitespace-pre-wrap'
        >
          {parsed.raw}
        </pre>
      ) : (
        <div className='space-y-1.5'>
          {parsed.entries.map((entry) => (
            <DetailRow
              key={entry.key}
              label={entry.key}
              mono
              muted={entry.value === REDACTED}
              value={
                entry.nested ? (
                  <pre className='overflow-auto text-xs whitespace-pre-wrap'>
                    {entry.value}
                  </pre>
                ) : (
                  entry.value
                )
              }
            />
          ))}
        </div>
      )}
    </div>
  )
}

/** One audit event in full: who, what, where, and the recorded details. */
export function AuditDetailDialog(props: AuditDetailDialogProps) {
  const { t } = useTranslation()
  const row = props.row
  return (
    <Dialog
      open={row !== null}
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={
        <span className='flex flex-wrap items-center gap-2'>
          {t('Audit event')}
          {row && (
            <StatusBadge
              label={row.action || t('Unknown')}
              autoColor={row.action.split('.')[0]}
              size='sm'
              copyable={false}
              className='font-mono'
            />
          )}
        </span>
      }
      description={
        row && row.timestamp > 0 ? formatTimestampToDate(row.timestamp) : ''
      }
      contentClassName='min-w-0 overflow-hidden sm:max-w-2xl'
      contentHeight='min(72dvh, 720px)'
    >
      {row && (
        <div className='space-y-3' data-testid='audit-detail'>
          <DetailSection label={t('Event')}>
            <DetailRow label={t('Event ID')} value={String(row.id)} mono />
            <DetailRow
              label={t('Operator')}
              value={`${row.actorType || '-'}${row.actorId > 0 ? ` #${row.actorId}` : ''}`}
            />
            <DetailRow
              label={t('Resource')}
              value={
                row.resource
                  ? `${row.resource}${row.resourceId > 0 ? ` #${row.resourceId}` : ''}`
                  : '-'
              }
            />
            <DetailRow label={t('IP')} value={row.ip || '-'} mono />
            {row.requestId && (
              <DetailRow
                label={t('Request ID')}
                value={
                  <span className='inline-flex items-center gap-1'>
                    {row.requestId}
                    <CopyButton
                      value={row.requestId}
                      className='size-5'
                      iconClassName='size-3'
                    />
                  </span>
                }
                mono
              />
            )}
            {props.scope === 'platform' && (
              <DetailRow label={t('Tenant')} value={row.tenantId || '-'} mono />
            )}
          </DetailSection>

          {props.scope === 'platform' && (
            <DetailSection label={t('Tamper-evidence chain')}>
              <DetailRow
                label={t('Row hash')}
                value={row.rowHash || t('Unchained')}
                mono
              />
              <DetailRow
                label={t('Previous hash')}
                value={row.prevHash || '-'}
                mono
              />
            </DetailSection>
          )}

          <DetailSection label={t('Details')}>
            <DetailsBody key={row.id} details={row.details} />
          </DetailSection>
        </div>
      )}
    </Dialog>
  )
}
