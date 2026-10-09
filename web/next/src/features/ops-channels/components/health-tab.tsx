import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { toApiError } from '@/lib/api'

import { channelHealthQueryOptions } from '../api'
import { formatTs } from '../lib/use-labels'
import type { ChannelDetail } from '../types'
import { Countdown, ReasonBadges, WindowBars } from './shared'

export function HealthTab(props: {
  channelId: number
  detail: ChannelDetail | undefined
}) {
  const { t } = useTranslation()
  const q = useQuery(channelHealthQueryOptions(props.channelId))

  if (q.isPending) return <LoadingState />
  if (q.isError) {
    return (
      <ErrorState
        title={t('Could not load channel health')}
        description={toApiError(q.error).message}
        onRetry={() => void q.refetch()}
      />
    )
  }
  const h = q.data
  // Live windows from the health call win; otherwise the last plan-quota probe.
  const windows =
    h.windows.length > 0 ? h.windows : (props.detail?.planWindows ?? [])

  return (
    <div className='grid gap-4' data-testid='health-tab'>
      <div className='flex flex-wrap items-center gap-2'>
        <Badge variant={h.routable ? 'default' : 'destructive'}>
          {h.routable ? t('Routable') : t('Not routable')}
        </Badge>
        {!h.routable && <ReasonBadges reasons={h.reasons} />}
        <Button
          variant='ghost'
          size='sm'
          className='ms-auto'
          onClick={() => void q.refetch()}
          disabled={q.isFetching}
        >
          {t('Refresh')}
        </Button>
      </div>

      {h.cooldownUntil > 0 && (
        <p className='text-sm'>
          {t('Cooling down, retry in')} <Countdown until={h.cooldownUntil} />
        </p>
      )}

      {windows.length > 0 ? (
        <div className='grid gap-1.5'>
          <h3 className='text-sm font-medium'>{t('Plan window usage')}</h3>
          <WindowBars windows={windows} />
        </div>
      ) : (
        <p className='text-muted-foreground text-sm'>
          {t('No plan window data for this channel.')}
        </p>
      )}
      {props.detail?.planProbeError ? (
        <p role='alert' className='text-destructive text-sm'>
          {t('Last plan quota check failed: {{message}}', {
            message: props.detail.planProbeError,
          })}
        </p>
      ) : null}

      <dl className='grid gap-2 text-sm'>
        <div className='flex justify-between gap-4'>
          <dt className='text-muted-foreground'>{t('Last success')}</dt>
          <dd>
            {h.lastSuccessAt != null ? formatTs(h.lastSuccessAt) : '--'}
          </dd>
        </div>
        {h.lastError !== '' && (
          <div className='flex justify-between gap-4'>
            <dt className='text-muted-foreground'>{t('Last error')}</dt>
            <dd className='min-w-0 text-end break-all'>{h.lastError}</dd>
          </div>
        )}
      </dl>
    </div>
  )
}
