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
import { Activity } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

import { fetchUptimeStatus } from '../lib/api'
import { useConsolePanels } from '../lib/status-data'
import type { UptimeGroup } from '../lib/types'
import {
  bySubGroup,
  countUp,
  formatUptime,
  UPTIME_DOT,
  UPTIME_LABEL,
  uptimeState,
  visibleGroups,
} from '../lib/uptime'
import { Panel } from './panel'
import { LoadFailed } from './panels'

function UptimeGroups(props: { groups: UptimeGroup[] }) {
  const { t } = useTranslation()
  return (
    <div className='flex flex-col gap-4'>
      {props.groups.map((g) => (
        <section
          key={`${g.categoryName}/${g.monitors[0]?.name}`}
          data-testid='uptime-group'
          aria-label={g.categoryName || undefined}
        >
          {g.categoryName && (
            <h4 className='text-muted-foreground mb-1.5 text-xs font-medium'>
              {g.categoryName}
            </h4>
          )}
          {bySubGroup(g.monitors).map((sub) => (
            <div key={sub.name} className='mb-1'>
              {sub.name && (
                <div className='text-muted-foreground/70 px-1 py-1 text-xs'>
                  {sub.name}
                </div>
              )}
              <ul className='divide-border/60 divide-y'>
                {sub.monitors.map((m) => {
                  const state = uptimeState(m.status)
                  return (
                    <li
                      key={`${m.group ?? ''}/${m.name}`}
                      data-testid='uptime-monitor'
                      data-state={state}
                      className='flex items-center justify-between gap-3 py-2 text-sm'
                    >
                      <span className='flex min-w-0 items-center gap-2'>
                        <span
                          className={cn(
                            'inline-block size-2 shrink-0 rounded-full',
                            UPTIME_DOT[state]
                          )}
                          aria-hidden='true'
                        />
                        <span className='truncate'>{m.name}</span>
                      </span>
                      <span className='flex shrink-0 items-center gap-3'>
                        <span className='text-muted-foreground text-xs'>
                          {t(UPTIME_LABEL[state])}
                        </span>
                        <span
                          className='w-16 text-end font-mono text-xs tabular-nums'
                          title={t('24-hour availability')}
                        >
                          {formatUptime(m.uptime)}
                        </span>
                      </span>
                    </li>
                  )
                })}
              </ul>
            </div>
          ))}
        </section>
      ))}
    </div>
  )
}

/**
 * Status of the services the operator monitors with Uptime Kuma
 * (console_setting.uptime_kuma_*). Draws nothing when the panel is switched
 * off, while it is loading, or when no monitor is configured; a failed read is
 * an error row, never an all-clear.
 */
export function UptimePanel() {
  const { t } = useTranslation()
  const { uptimeEnabled } = useConsolePanels()
  const q = useQuery({
    queryKey: ['dashboard', 'uptime-status'],
    queryFn: () => fetchUptimeStatus(),
    enabled: uptimeEnabled,
    retry: false,
    refetchInterval: 60_000,
    meta: { errorToast: false },
  })

  if (!uptimeEnabled || q.isPending) return null

  if (q.isError) {
    return (
      <Panel
        testId='dashboard-uptime'
        icon={<Activity />}
        tone='success'
        title={t('Service status')}
      >
        <LoadFailed
          message={t('Could not load service status.')}
          onRetry={() => void q.refetch()}
        />
      </Panel>
    )
  }

  const groups = visibleGroups(q.data)
  if (groups.length === 0) return null
  const { up, total } = countUp(groups)

  return (
    <Panel
      testId='dashboard-uptime'
      icon={<Activity />}
      tone='success'
      title={t('Service status')}
      description={t('{{up}} of {{total}} monitors up', { up, total })}
    >
      <UptimeGroups groups={groups} />
    </Panel>
  )
}
