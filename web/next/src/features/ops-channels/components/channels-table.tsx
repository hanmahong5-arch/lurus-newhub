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

import { channelTypeName, REASON_LABELS } from '../lib/map'
import { usePlanKindLabel, useStatusLabel } from '../lib/use-labels'
import type { ChannelListItem } from '../types'

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
