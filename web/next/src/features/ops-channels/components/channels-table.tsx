import { useQuery } from '@tanstack/react-query'
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

import { channelDetailQueryOptions, channelHealthQueryOptions } from '../api'
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
  // Both calls are cached and shared with the drawer.
  const detail = useQuery(channelDetailQueryOptions(c.id))
  const health = useQuery(channelHealthQueryOptions(c.id))
  const st = statusOf(c.status)

  const expiresAt = detail.data?.plan.expiresAt ?? 0
  const expired = expiresAt > 0 && expiresAt <= props.nowSec

  let routable: ReactNode
  if (health.isPending) {
    routable = <span className='text-muted-foreground'>...</span>
  } else if (health.isError) {
    routable = (
      <Badge variant='warning' title={health.error.message}>
        {t('Unknown')}
      </Badge>
    )
  } else if (health.data.routable) {
    routable = <Badge variant='default'>{t('Routable')}</Badge>
  } else {
    routable = (
      <Badge
        variant='destructive'
        title={health.data.reasons
          .map((r) => t(REASON_LABELS[r] ?? r))
          .join(', ')}
      >
        {t('Not routable')}
      </Badge>
    )
  }

  let plan: ReactNode
  if (detail.isPending) {
    plan = '...'
  } else if (detail.isError) {
    plan = (
      <Badge variant='warning' title={detail.error.message}>
        {t('Unknown')}
      </Badge>
    )
  } else {
    plan = planLabel(detail.data.plan.planKind)
  }

  return (
    <TableRow data-testid={`channel-row-${c.id}`}>
      <TableCell className='max-w-48 truncate font-medium'>{c.name}</TableCell>
      <TableCell>{channelTypeName(c.type)}</TableCell>
      <TableCell>
        <Badge variant={st.variant}>{st.label}</Badge>
      </TableCell>
      <TableCell data-testid={`channel-${c.id}-plan`}>{plan}</TableCell>
      <TableCell
        className={expired ? 'text-destructive' : undefined}
        data-testid={`channel-${c.id}-expires`}
      >
        {expiresAt > 0 ? formatDate(expiresAt) : '--'}
        {expired && ` (${t('expired')})`}
      </TableCell>
      <TableCell data-testid={`channel-${c.id}-keys`}>
        {detail.data != null ? detail.data.keyCount : '--'}
      </TableCell>
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
