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
import { useTranslation } from 'react-i18next'

import { formatTimestampToDate } from '@/components/format'
import { StatusBadge } from '@/components/status-badge'

import { channelModalities, MODALITY_LABELS, REASON_LABELS } from '../lib/map'
import { usePlanKindLabel } from '../lib/use-labels'
import { useChannelOpsInfo } from './channel-ops-context'

const MISMATCH_LABELS: Record<string, string> = {
  modality_conflict:
    'The same model is registered with a different modality on another channel',
  adapter_unsupported: 'This channel type cannot serve this modality',
}

const NO_DATA = <span className='text-muted-foreground text-xs'>--</span>

/** Routable verdict plus a flag when the backend reports a modality mismatch. */
export function RoutingCell(props: { channelId: number }) {
  const { t } = useTranslation()
  const info = useChannelOpsInfo(props.channelId)
  if (!info) return NO_DATA
  return (
    <span className='flex flex-wrap items-center gap-1'>
      {info.routable ? (
        <StatusBadge
          label={t('Routable')}
          variant='success'
          size='sm'
          copyable={false}
        />
      ) : (
        <StatusBadge
          label={t('Not routable')}
          variant='danger'
          size='sm'
          copyable={false}
          title={info.unroutableReasons
            .map((r) => t(REASON_LABELS[r] ?? r))
            .join(', ')}
        />
      )}
      {info.modalityMismatches.length > 0 && (
        <StatusBadge
          label={t('Mismatch')}
          variant='warning'
          size='sm'
          copyable={false}
          data-testid={`channel-${props.channelId}-mismatch`}
          title={info.modalityMismatches
            .map(
              (m) => `${m.model}: ${t(MISMATCH_LABELS[m.reason] ?? m.reason)}`
            )
            .join(', ')}
        />
      )}
    </span>
  )
}

export function ModalityCell(props: { channelId: number }) {
  const { t } = useTranslation()
  const info = useChannelOpsInfo(props.channelId)
  const mods = info ? channelModalities(info) : []
  return (
    <span
      className='flex flex-wrap items-center gap-1'
      data-testid={`channel-${props.channelId}-modality`}
    >
      {mods.length === 0
        ? NO_DATA
        : mods.map((m) => (
            <StatusBadge
              key={m}
              label={t(MODALITY_LABELS[m] ?? m)}
              variant='neutral'
              size='sm'
              copyable={false}
            />
          ))}
    </span>
  )
}

/** Plan kind, expiry and key pool size. */
export function PlanCell(props: { channelId: number }) {
  const { t } = useTranslation()
  const planLabel = usePlanKindLabel()
  const info = useChannelOpsInfo(props.channelId)
  if (!info) return NO_DATA
  const nowSec = Math.floor(Date.now() / 1000)
  const expired = info.expiresAt > 0 && info.expiresAt <= nowSec
  return (
    <div
      className='flex flex-col gap-0.5 text-xs'
      data-testid={`channel-${props.channelId}-plan`}
    >
      <span>{planLabel(info.planKind)}</span>
      {info.expiresAt > 0 && (
        <span
          className={expired ? 'text-destructive' : 'text-muted-foreground'}
          data-testid={`channel-${props.channelId}-expires`}
        >
          {formatTimestampToDate(info.expiresAt)}
          {expired && ` (${t('expired')})`}
        </span>
      )}
      <span
        className='text-muted-foreground'
        data-testid={`channel-${props.channelId}-keys`}
      >
        {t('{{enabled}}/{{total}} keys', {
          enabled: info.enabledKeyCount,
          total: info.keyCount,
        })}
      </span>
    </div>
  )
}
