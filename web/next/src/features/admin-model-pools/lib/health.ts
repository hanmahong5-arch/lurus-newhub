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
import type { ModelHealth, Pool, PoolChannel, PoolModel } from '../types'

/**
 * "never_probed" is deliberately its own state: no health row means unknown,
 * which must not read as healthy (and must not look like a passing probe).
 */
export type HealthVariant = 'never_probed' | 'auto_disabled' | 'failing' | 'ok'

export function healthVariant(health: ModelHealth | null): HealthVariant {
  if (!health) return 'never_probed'
  if (health.auto_disabled) return 'auto_disabled'
  if (!health.ok) return 'failing'
  return 'ok'
}

/** A model whose last probe failed or that the prober switched off. */
export function isUnhealthy(model: PoolModel): boolean {
  const v = healthVariant(model.health)
  return v === 'auto_disabled' || v === 'failing'
}

export function formatLatency(ms: number | undefined): string {
  return ms != null && ms > 0 ? `${Math.round(ms)}ms` : '—'
}

export function truncate(text: string | undefined, max: number): string {
  if (!text) return ''
  return text.length > max ? `${text.slice(0, max)}…` : text
}

export interface PoolsSummary {
  pools: number
  channels: number
  models: number
  unhealthy: number
}

export function summarize(pools: Pool[]): PoolsSummary {
  const out: PoolsSummary = {
    pools: pools.length,
    channels: 0,
    models: 0,
    unhealthy: 0,
  }
  for (const pool of pools) {
    out.channels += pool.channels.length
    for (const ch of pool.channels) {
      out.models += ch.models.length
      out.unhealthy += ch.models.filter(isUnhealthy).length
    }
  }
  return out
}

function includes(haystack: string, needle: string): boolean {
  return haystack.toLowerCase().includes(needle)
}

/**
 * Narrow the overview. A match on the pool name keeps the whole pool, a match
 * on a channel (name or type label) keeps all of its models, otherwise only the
 * matching models survive. Empty channels and pools drop out.
 */
export function filterPools(
  pools: Pool[],
  query: string,
  onlyUnhealthy: boolean,
  typeLabel: (type: number) => string
): Pool[] {
  const q = query.trim().toLowerCase()
  if (!q && !onlyUnhealthy) return pools
  const out: Pool[] = []
  for (const pool of pools) {
    const poolHit = q !== '' && includes(pool.group, q)
    const channels: PoolChannel[] = []
    for (const ch of pool.channels) {
      const chHit =
        poolHit ||
        (q !== '' && (includes(ch.name, q) || includes(typeLabel(ch.type), q)))
      let models = ch.models
      if (q && !chHit) models = models.filter((m) => includes(m.model, q))
      if (onlyUnhealthy) models = models.filter(isUnhealthy)
      if (models.length > 0) channels.push({ ...ch, models })
    }
    if (channels.length > 0) out.push({ ...pool, channels })
  }
  return out
}
