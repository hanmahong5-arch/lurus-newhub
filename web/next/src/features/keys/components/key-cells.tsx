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

import {
  ActivityTimeCell,
  TimestampCell,
} from '@/components/activity-time-cell'
import { CopyButton } from '@/components/copy-button'
import { QuotaDetailsPopover } from '@/components/quota-details-popover'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Popover,
  PopoverContent,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Progress } from '@/components/ui/progress'
import { Switch } from '@/components/ui/switch'
import { toIntlLocale } from '@/i18n/languages'
import type { MoneyConfig } from '@/lib/money'
import { cn } from '@/lib/utils'

import { isStoredEnabled } from '../lib/map'
import { quotaView } from '../lib/quota'
import { splitIps, splitModels } from '../lib/restrictions'
import type { ApiToken } from '../types'

const IDLE_MS = 90 * 24 * 60 * 60 * 1000

/**
 * The masked key. The server cannot reveal an existing key, so the only copy
 * action is for a secret this session just created or rotated.
 */
export function KeyCell(props: { token: ApiToken; secret?: string }) {
  const { t } = useTranslation()
  return (
    <div className='flex max-w-full min-w-0 items-center gap-1'>
      <code className='truncate font-mono text-xs'>{props.token.key}</code>
      {props.secret != null && (
        <CopyButton
          value={props.secret}
          size='icon'
          className='size-7'
          aria-label={t('Copy new key')}
        />
      )}
    </div>
  )
}

/** Remaining / used with a remaining-percentage bar; details in a popover. */
export function QuotaCell(props: {
  token: ApiToken
  money: MoneyConfig | null
  variant?: 'table' | 'card'
}) {
  const { t, i18n } = useTranslation()
  const { token, money } = props
  const q = quotaView(token, money)
  const remainingRatio = q.usedRatio == null ? null : 1 - q.usedRatio
  const percent =
    remainingRatio == null ? 0 : Math.round(remainingRatio * 1000) / 10
  const percentText = new Intl.NumberFormat(
    toIntlLocale(i18n.resolvedLanguage || i18n.language),
    { maximumFractionDigits: 1 }
  ).format(percent)

  const details = q.unlimited
    ? [{ label: t('Used amount'), value: q.used }]
    : [
        { label: t('Remaining'), value: q.remaining },
        { label: t('Used amount'), value: q.used },
        { label: t('Current total quota'), value: q.total },
        ...(remainingRatio == null
          ? []
          : [{ label: t('Remaining percentage'), value: `${percentText}%` }]),
      ]

  const headline = q.unlimited
    ? t('Unlimited')
    : t('{{amount}} left', { amount: q.remaining })
  const usedLine = t('{{amount}} used', { amount: q.used })

  let tone = 'text-emerald-500'
  if (token.status !== 1 || token.remain_quota <= 0) {
    tone = 'text-muted-foreground/60'
  } else if (percent <= 10) {
    tone = 'text-rose-500'
  } else if (percent <= 30) {
    tone = 'text-amber-500'
  }

  return (
    <QuotaDetailsPopover
      title={t('Quota')}
      triggerLabel={`${t('Quota')}: ${headline}; ${usedLine}`}
      details={details}
      description={
        q.unlimited
          ? t(
              'This key has no quota limit. Requests still need available balance.'
            )
          : t(
              'Total = used + remaining. Changing the total cap changes the remaining amount.'
            )
      }
      className={props.variant === 'card' ? 'space-y-2' : 'max-w-48 space-y-1.5'}
      afterTrigger={
        !q.unlimited &&
        remainingRatio != null && (
          <Progress
            value={percent}
            aria-label={t('Remaining percentage')}
            className={cn(
              'w-full [&_[data-slot=progress-indicator]]:bg-current',
              tone
            )}
          />
        )
      }
    >
      <span className='grid w-full min-w-0 gap-0.5 text-sm'>
        <span
          className={cn(
            'truncate font-medium tabular-nums',
            !q.unlimited && token.remain_quota <= 0 && 'text-muted-foreground'
          )}
        >
          {headline}
        </span>
        <span className='text-muted-foreground truncate text-xs tabular-nums'>
          {usedLine}
        </span>
      </span>
    </QuotaDetailsPopover>
  )
}

function RestrictionCell(props: {
  items: string[]
  label: string
  title: string
  emptyLabel: string
  inline?: boolean
}) {
  if (props.items.length === 0) {
    return props.inline ? (
      <span className='inline-flex items-center gap-1.5 text-xs'>
        <span className='text-muted-foreground'>{props.title}</span>
        <span>{props.emptyLabel}</span>
      </span>
    ) : (
      <StatusBadge
        label={props.emptyLabel}
        variant='neutral'
        copyable={false}
        className='-ml-1.5'
      />
    )
  }
  return (
    <Popover>
      <PopoverTrigger
        render={
          <Button
            variant='ghost'
            size='sm'
            aria-label={`${props.title}: ${props.label}`}
            className='h-7 max-w-full justify-start px-0 text-xs font-normal underline decoration-dotted underline-offset-4'
          />
        }
      >
        {props.label}
      </PopoverTrigger>
      <PopoverContent align='start' className='max-w-[calc(100vw-2rem)]'>
        <PopoverTitle>{props.title}</PopoverTitle>
        <div className='max-h-52 space-y-1 overflow-y-auto text-xs'>
          {props.items.map((item) => (
            <div key={item} className='font-mono break-all'>
              {item}
            </div>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  )
}

export function ModelLimitsCell(props: { token: ApiToken; inline?: boolean }) {
  const { t } = useTranslation()
  const models = props.token.model_limits_enabled
    ? splitModels(props.token.model_limits)
    : []
  return (
    <RestrictionCell
      items={models}
      label={t('{{count}} models', { count: models.length })}
      title={t('Models')}
      emptyLabel={t('All models')}
      inline={props.inline}
    />
  )
}

export function IpRestrictionsCell(props: {
  token: ApiToken
  inline?: boolean
}) {
  const { t } = useTranslation()
  const ips = splitIps(props.token.allow_ips)
  return (
    <RestrictionCell
      items={ips}
      label={t('{{count}} IPs', { count: ips.length })}
      title={t('Allowed IPs')}
      emptyLabel={t('Any address')}
      inline={props.inline}
    />
  )
}

export function ExpiryCell(props: { token: ApiToken; nowMs: number }) {
  const { t, i18n } = useTranslation()
  const expiry = props.token.expired_time
  if (!(expiry > 0)) {
    return (
      <StatusBadge
        label={t('Never')}
        variant='neutral'
        copyable={false}
        className='-ml-1.5'
      />
    )
  }
  return (
    <TimestampCell
      timestamp={expiry}
      now={props.nowMs}
      locale={toIntlLocale(i18n.resolvedLanguage || i18n.language)}
      justNowLabel={t('Just now')}
      className={
        expiry * 1000 <= props.nowMs
          ? 'text-destructive'
          : 'text-muted-foreground'
      }
    />
  )
}

/** Created + last used; a key idle for three months is flagged. */
export function ActivityCell(props: {
  token: ApiToken
  nowMs: number
  layout?: 'rows' | 'columns'
}) {
  const { t } = useTranslation()
  const last = props.token.accessed_time
  const stale = last > 0 && last * 1000 < props.nowMs - IDLE_MS
  return (
    <ActivityTimeCell
      createdAt={props.token.created_time}
      lastAt={last}
      lastLabel={t('Last used')}
      lastClassName={stale ? 'text-warning' : 'text-muted-foreground'}
      now={props.nowMs}
      layout={props.layout}
    />
  )
}

/** The status switch lives next to the badge so it is reachable on mobile. */
export function StatusToggle(props: {
  token: ApiToken
  busy: boolean
  onToggle: (token: ApiToken, enable: boolean) => void
}) {
  const { t } = useTranslation()
  const { token } = props
  const enabled = isStoredEnabled(token)
  return (
    <Switch
      size='sm'
      checked={enabled}
      disabled={props.busy}
      onCheckedChange={(v) => props.onToggle(token, v)}
      aria-label={
        enabled
          ? t('Disable {{name}}', { name: token.name })
          : t('Enable {{name}}', { name: token.name })
      }
    />
  )
}
