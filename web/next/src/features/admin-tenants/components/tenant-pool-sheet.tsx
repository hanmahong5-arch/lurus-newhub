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
import {
  useQuery,
  useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query'
import type { TFunction } from 'i18next'
import { useCallback, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { useQuota } from '@/hooks/use-quota'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import { getCreditPool, listPoolDraws, tenantsQueryKey } from '../api'
import { formatIsoDate, resetPeriodLabel } from '../lib/labels'
import {
  isUnlimited,
  poolFillRatio,
  poolHealth,
  type PoolHealth,
} from '../lib/pool'
import type { PoolDraw, Tenant } from '../types'
import { CeilingForm, TopupForm } from './pool-forms'
import { useTenants } from './tenants-provider'

const HEALTH_VARIANT: Record<PoolHealth, StatusVariant> = {
  unconfigured: 'neutral',
  unlimited: 'info',
  exhausted: 'danger',
  warning: 'warning',
  ok: 'success',
}

function healthLabel(health: PoolHealth, t: TFunction): string {
  switch (health) {
    case 'unconfigured':
      return t('No pool')
    case 'unlimited':
      return t('Unlimited')
    case 'exhausted':
      return t('Exhausted')
    case 'warning':
      return t('Low')
    case 'ok':
      return t('Healthy')
  }
}

function drawReasonLabel(reason: string, t: TFunction): string {
  switch (reason) {
    case 'relay_debit':
      return t('Relay usage')
    case 'relay_overdraft':
      return t('Relay overdraft')
    case 'topup':
      return t('Top-up')
    case 'reset':
      return t('Reset')
    case 'adjustment':
      return t('Adjustment')
    default:
      return reason || '-'
  }
}

function PoolActivity(props: {
  query: UseQueryResult<PoolDraw[]>
  formatQuota: (quota: number) => string
}) {
  const { t } = useTranslation()
  const { query, formatQuota } = props
  const draws = query.data ?? []

  let body: ReactNode
  if (query.isLoading) {
    body = <LoadingState inline size='sm' message={t('Loading...')} />
  } else if (query.isError) {
    // A failed read is not "no draws yet".
    body = (
      <div
        className='text-muted-foreground flex items-center gap-3 text-sm'
        data-testid='usage-load-failed'
      >
        <span>{t('Could not load the pool activity.')}</span>
        <Button
          size='sm'
          variant='outline'
          onClick={() => void query.refetch()}
        >
          {t('Retry')}
        </Button>
      </div>
    )
  } else if (draws.length === 0) {
    body = <p className='text-muted-foreground text-sm'>{t('No draws yet.')}</p>
  } else {
    body = (
      <div className='border-border overflow-x-auto rounded-lg border'>
        <table className='w-full text-sm'>
          <thead>
            <tr className='text-muted-foreground border-b text-left text-xs'>
              <th className='px-3 py-2 font-medium'>{t('Time')}</th>
              <th className='px-3 py-2 font-medium'>{t('Direction')}</th>
              <th className='px-3 py-2 text-right font-medium'>
                {t('Amount')}
              </th>
              <th className='px-3 py-2 font-medium'>{t('Reason')}</th>
            </tr>
          </thead>
          <tbody>
            {draws.map((draw) => (
              <tr
                key={draw.id}
                className='border-b last:border-b-0'
                data-testid={`pool-draw-${draw.id}`}
              >
                <td className='px-3 py-2 font-mono text-xs whitespace-nowrap'>
                  {formatIsoDate(draw.created_at)}
                </td>
                <td className='px-3 py-2'>
                  {draw.direction === 1 ? t('Debit') : t('Credit')}
                </td>
                <td className='px-3 py-2 text-right font-mono tabular-nums'>
                  {formatQuota(draw.amount)}
                </td>
                <td className='px-3 py-2'>{drawReasonLabel(draw.reason, t)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    )
  }

  return (
    <section className='grid gap-2' data-testid='usage-section'>
      <h3 className='text-sm font-semibold'>{t('Recent activity')}</h3>
      {body}
    </section>
  )
}

export function TenantPoolSheet({ tenant }: { tenant: Tenant }) {
  const { t } = useTranslation()
  const { setDialog } = useTenants()
  const { formatQuota } = useQuota()
  const qc = useQueryClient()
  const [busy, setBusy] = useState(false)

  const poolKey = useMemo(
    () => [...tenantsQueryKey, 'pool', tenant.id],
    [tenant.id]
  )
  const drawsKey = useMemo(
    () => [...tenantsQueryKey, 'pool-draws', tenant.id],
    [tenant.id]
  )

  const poolQuery = useQuery({
    queryKey: poolKey,
    queryFn: () => getCreditPool(tenant.id),
    // The failure is shown in place; no second toast on top of it.
    meta: { errorToast: false },
  })
  const pool = poolQuery.data ?? null
  const poolKnown = poolQuery.data !== undefined

  const drawsQuery = useQuery({
    queryKey: drawsKey,
    queryFn: () => listPoolDraws(tenant.id),
    // The failure is shown in place; no second toast on top of it.
    meta: { errorToast: false },
    enabled: pool !== null,
  })

  const reload = useCallback(() => {
    void qc.invalidateQueries({ queryKey: poolKey })
    void qc.invalidateQueries({ queryKey: drawsKey })
  }, [qc, poolKey, drawsKey])

  const health = poolHealth(pool)
  const fill = poolFillRatio(pool)

  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open && !busy) setDialog(null)
      }}
    >
      <SheetContent
        className={sideDrawerContentClassName('max-w-none sm:!max-w-[600px]')}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {t('{{name}} · credit pool', { name: tenant.name })}
          </SheetTitle>
          <SheetDescription>
            {t('The prepaid balance this tenant’s traffic draws on.')}
          </SheetDescription>
        </SheetHeader>

        <div className={sideDrawerFormClassName('gap-6')}>
          {poolQuery.isLoading && <LoadingState message={t('Loading...')} />}

          {poolQuery.isError && (
            // A failed read is not "no pool": the ceiling form would offer to
            // create a pool that may already exist.
            <ErrorState
              title={t('Could not load the credit pool')}
              description={getServerErrorMessage(poolQuery.error)}
              onRetry={() => void poolQuery.refetch()}
              className='min-h-[200px]'
            />
          )}

          {poolKnown && !poolQuery.isError && (
            <>
              <section
                className='border-border grid gap-3 rounded-lg border p-4'
                data-testid='pool-summary'
              >
                <div className='flex items-center justify-between gap-3'>
                  <span className='text-muted-foreground text-xs font-medium uppercase'>
                    {t('Balance')}
                  </span>
                  <StatusBadge
                    label={healthLabel(health, t)}
                    variant={HEALTH_VARIANT[health]}
                    copyable={false}
                    data-testid='pool-health'
                  />
                </div>
                {pool ? (
                  <>
                    <div className='font-mono text-lg font-semibold tabular-nums'>
                      {formatQuota(pool.current_balance)}
                      <span className='text-muted-foreground font-normal'>
                        {' / '}
                        {isUnlimited(pool)
                          ? t('∞ (unlimited)')
                          : formatQuota(pool.max_balance)}
                      </span>
                    </div>
                    {fill !== null && (
                      <div
                        role='progressbar'
                        aria-label={t('Share of the ceiling left')}
                        aria-valuemin={0}
                        aria-valuemax={100}
                        aria-valuenow={Math.round(fill * 100)}
                        className='bg-muted h-1.5 overflow-hidden rounded-full'
                      >
                        <div
                          className={cn(
                            'h-full rounded-full',
                            health === 'warning' && 'bg-warning',
                            health === 'exhausted' && 'bg-destructive',
                            health === 'ok' && 'bg-success'
                          )}
                          style={{ width: `${Math.round(fill * 100)}%` }}
                        />
                      </div>
                    )}
                    <div className='text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs'>
                      <span>
                        {t('Resets: {{period}}', {
                          period: resetPeriodLabel(pool.reset_period, t),
                        })}
                      </span>
                      {pool.next_reset_at && (
                        <span>
                          {t('Next reset: {{when}}', {
                            when: formatIsoDate(pool.next_reset_at),
                          })}
                        </span>
                      )}
                      {pool.alert_threshold_pct != null && (
                        <span>
                          {t('Alert at {{pct}}%', {
                            pct: pool.alert_threshold_pct,
                          })}
                        </span>
                      )}
                    </div>
                  </>
                ) : (
                  <div className='text-muted-foreground text-sm'>
                    {t('No credit pool configured')}
                  </div>
                )}
              </section>

              {pool === null && (
                <SideDrawerSection>
                  <CeilingForm
                    tenantId={tenant.id}
                    onBusyChange={setBusy}
                    onCreated={reload}
                    onAlreadyExists={reload}
                  />
                </SideDrawerSection>
              )}

              <SideDrawerSection>
                <TopupForm
                  tenantId={tenant.id}
                  pool={pool}
                  onBusyChange={setBusy}
                  onToppedUp={reload}
                />
              </SideDrawerSection>

              {pool !== null && (
                <PoolActivity query={drawsQuery} formatQuota={formatQuota} />
              )}
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
