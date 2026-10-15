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
import type { UptimeGroup, UptimeMonitor } from './types'

export type UptimeState = 'up' | 'down' | 'degraded' | 'maintenance' | 'unknown'

/** Uptime Kuma heartbeat codes: 0 down, 1 up, 2 pending, 3 maintenance. */
export function uptimeState(status: number): UptimeState {
  if (status === 1) return 'up'
  if (status === 0) return 'down'
  if (status === 2) return 'degraded'
  if (status === 3) return 'maintenance'
  return 'unknown'
}

/** English source of the state label. */
export const UPTIME_LABEL: Record<UptimeState, string> = {
  up: 'Up',
  down: 'Down',
  degraded: 'Degraded',
  maintenance: 'Maintenance',
  unknown: 'Unknown',
}

/** Tailwind background of the state dot. */
export const UPTIME_DOT: Record<UptimeState, string> = {
  up: 'bg-success',
  down: 'bg-destructive',
  degraded: 'bg-warning',
  maintenance: 'bg-chart-2',
  unknown: 'bg-muted-foreground/50',
}

/** Availability ratio (0..1) as "99.95%"; non-finite -> a dash. */
export function formatUptime(ratio: number): string {
  return Number.isFinite(ratio) ? `${(ratio * 100).toFixed(2)}%` : '--'
}

/**
 * Groups worth drawing: a group whose monitor list is empty (nothing in it, or
 * its Uptime Kuma page could not be read by the server) is dropped. Empty
 * result = nothing to show, and the panel renders nothing at all.
 */
export function visibleGroups(groups: UptimeGroup[]): UptimeGroup[] {
  return groups.filter((g) => g.monitors.length > 0)
}

export function countUp(groups: UptimeGroup[]): { up: number; total: number } {
  let up = 0
  let total = 0
  for (const g of groups) {
    for (const m of g.monitors) {
      total += 1
      if (uptimeState(m.status) === 'up') up += 1
    }
  }
  return { up, total }
}

/** Monitors of one group, bucketed by Uptime Kuma's own sub-group name. */
export function bySubGroup(
  monitors: UptimeMonitor[]
): Array<{ name: string; monitors: UptimeMonitor[] }> {
  const out: Array<{ name: string; monitors: UptimeMonitor[] }> = []
  for (const m of monitors) {
    const name = m.group ?? ''
    const hit = out.find((b) => b.name === name)
    if (hit) hit.monitors.push(m)
    else out.push({ name, monitors: [m] })
  }
  return out
}
