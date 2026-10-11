import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatDate } from '@/lib/time'

import {
  channelModalities,
  channelTypeName,
  MODALITY_LABELS,
  REASON_LABELS,
} from '../lib/map'
import { usePlanKindLabel, useStatusLabel } from '../lib/use-labels'
import type { ChannelListItem } from '../types'

const MISMATCH_LABELS: Record<string, string> = {
  modality_conflict:
    'The same model is registered with a different modality on another channel',
  adapter_unsupported: 'This channel type cannot serve this modality',
}

function ModalityCell(props: { channel: ChannelListItem }) {
  const { t } = useTranslation()
  const c = props.channel
  const mods = channelModalities(c)
  if (mods.length === 0 && c.modalityMismatches.length === 0) return <>--</>
  return (
    <span className='flex flex-wrap items-center gap-1'>
      {mods.map((m) => (
        <Badge key={m} variant='outline'>
          {t(MODALITY_LABELS[m] ?? m)}
        </Badge>
      ))}
      {c.modalityMismatches.length > 0 && (
        <Badge
          variant='destructive'
          data-testid={`channel-${c.id}-mismatch`}
          title={c.modalityMismatches
            .map(
              (x) => `${x.model}: ${t(MISMATCH_LABELS[x.reason] ?? x.reason)}`
            )
            .join(', ')}
        >
          {t('Mismatch')}
        </Badge>
      )}
    </span>
  )
}

function ChannelRow(props: {
  channel: ChannelListItem
  nowSec: number
  onOpen: (c: ChannelListItem) => void
}) {
  const { t } = useTranslation()
  const c = props.channel
  const statusOf = useStatusLabel()
  const planLabel = usePlanKindLabel()
  const st = statusOf(c.status)

  // Everything on the row comes from the list payload: no per-row requests.
  const expiresAt = c.expiresAt
  const expired = expiresAt > 0 && expiresAt <= props.nowSec

  const routable: ReactNode = c.routable ? (
    <Badge variant='default'>{t('Routable')}</Badge>
  ) : (
    <Badge
      variant='destructive'
      title={c.unroutableReasons
        .map((r) => t(REASON_LABELS[r] ?? r))
        .join(', ')}
    >
      {t('Not routable')}
    </Badge>
  )

  return (
    <TableRow data-testid={`channel-row-${c.id}`}>
      <TableCell className='max-w-48 truncate font-medium'>{c.name}</TableCell>
      <TableCell>{channelTypeName(c.type)}</TableCell>
      <TableCell>
        <Badge variant={st.variant}>{st.label}</Badge>
      </TableCell>
      <TableCell data-testid={`channel-${c.id}-modality`}>
        <ModalityCell channel={c} />
      </TableCell>
      <TableCell data-testid={`channel-${c.id}-plan`}>
        {planLabel(c.planKind)}
      </TableCell>
      <TableCell
        className={expired ? 'text-destructive' : undefined}
        data-testid={`channel-${c.id}-expires`}
      >
        {expiresAt > 0 ? formatDate(expiresAt) : '--'}
        {expired && ` (${t('expired')})`}
      </TableCell>
      <TableCell data-testid={`channel-${c.id}-keys`}>{c.keyCount}</TableCell>
      <TableCell>{routable}</TableCell>
      <TableCell className='text-end'>
        <Button
          size='sm'
          variant='outline'
          onClick={() => props.onOpen(c)}
          aria-label={t('Details of {{name}}', { name: c.name })}
        >
          {t('Details')}
        </Button>
      </TableCell>
    </TableRow>
  )
}

export function ChannelsTable(props: {
  channels: ChannelListItem[]
  nowSec: number
  onOpen: (c: ChannelListItem) => void
}) {
  const { t } = useTranslation()
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{t('Name')}</TableHead>
          <TableHead>{t('Type')}</TableHead>
          <TableHead>{t('Status')}</TableHead>
          <TableHead>{t('Modality')}</TableHead>
          <TableHead>{t('Plan')}</TableHead>
          <TableHead>{t('Expires')}</TableHead>
          <TableHead>{t('Keys')}</TableHead>
          <TableHead>{t('Routing')}</TableHead>
          <TableHead className='text-end'>{t('Actions')}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {props.channels.map((c) => (
          <ChannelRow
            key={c.id}
            channel={c}
            nowSec={props.nowSec}
            onOpen={props.onOpen}
          />
        ))}
      </TableBody>
    </Table>
  )
}
