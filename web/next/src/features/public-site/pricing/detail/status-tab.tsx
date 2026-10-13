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

import { Skeleton } from '@/components/ui/skeleton'

import { HealthBadge } from '../components/price-cells'
import { useModelHealth } from '../use-public-models'
import type { PublicModel } from '../lib/model'

/**
 * Whether the model can be reached right now, from the anonymous status
 * endpoint. That endpoint reports a coarse state per model and nothing else,
 * so this tab shows exactly that: no latency, uptime or throughput figures.
 */
export function StatusTab(props: { model: PublicModel }) {
  const { t } = useTranslation()
  const { health, updatedAt, isLoading } = useModelHealth()
  const state = health.get(props.model.id)

  if (isLoading) return <Skeleton className='h-24 w-full' />

  return (
    <section className='bg-card/60 space-y-3 rounded-xl border p-4 shadow-sm'>
      <h2 className='text-muted-foreground text-xs font-semibold tracking-wider uppercase'>
        {t('Current status')}
      </h2>
      {state ? (
        <>
          <HealthBadge health={state} className='text-base' />
          <p className='text-muted-foreground text-sm'>
            {state === 'operational' &&
              t('All channels that serve this model are routing requests.')}
            {state === 'degraded' &&
              t('Some channels that serve this model are not routing requests.')}
            {state === 'down' &&
              t('No channel is routing requests for this model right now.')}
          </p>
        </>
      ) : (
        <p className='text-muted-foreground text-sm'>
          {t('No status is available for this model yet.')}
        </p>
      )}
      {updatedAt ? (
        <p className='text-muted-foreground/80 text-xs'>
          {t('Updated {{time}}', {
            time: new Date(updatedAt * 1000).toLocaleString(),
          })}
        </p>
      ) : null}
    </section>
  )
}
