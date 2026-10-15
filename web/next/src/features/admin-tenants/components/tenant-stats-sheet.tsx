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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { useQuota } from '@/hooks/use-quota'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { getTenantStats, tenantsQueryKey } from '../api'
import { statRows } from '../lib/stats'
import type { Tenant } from '../types'
import { useTenants } from './tenants-provider'

export function TenantStatsSheet({ tenant }: { tenant: Tenant }) {
  const { t } = useTranslation()
  const { setDialog } = useTenants()
  const { formatQuota } = useQuota()

  const { data, isLoading, isError, error, refetch } = useQuery({
    queryKey: [...tenantsQueryKey, 'stats', tenant.id],
    queryFn: () => getTenantStats(tenant.id),
    // The failure is shown in place; no second toast on top of it.
    meta: { errorToast: false },
  })

  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open) setDialog(null)
      }}
    >
      <SheetContent
        className={sideDrawerContentClassName('max-w-none sm:!max-w-[460px]')}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>{t('{{name}} · stats', { name: tenant.name })}</SheetTitle>
          <SheetDescription>
            {t('Usage and capacity of this tenant right now.')}
          </SheetDescription>
        </SheetHeader>

        <div className={sideDrawerFormClassName()}>
          {isLoading && <LoadingState message={t('Loading...')} />}

          {isError && (
            <ErrorState
              title={t('Could not load stats')}
              description={getServerErrorMessage(error)}
              onRetry={() => void refetch()}
              className='min-h-[200px]'
            />
          )}

          {data && !isError && (
            <dl className='divide-border border-border divide-y rounded-lg border'>
              {statRows(data, formatQuota, t).map(([label, value]) => (
                <div
                  key={label}
                  className='grid grid-cols-[minmax(120px,44%)_1fr] items-center gap-3 px-4 py-2.5 text-sm'
                >
                  <dt className='text-muted-foreground'>{label}</dt>
                  <dd className='m-0 text-right font-mono font-medium'>
                    {value}
                  </dd>
                </div>
              ))}
            </dl>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}
