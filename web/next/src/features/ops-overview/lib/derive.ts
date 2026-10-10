import type { ChannelProbe, HealthSummaryItem, SummaryChannel } from './types'

export const EXPIRY_WARN_SECONDS = 72 * 3600
export const WINDOW_WARN_PCT = 90

/** Upstream cost: 0.0001 CNY units -> "¥12.34". Not a quota, so not money.ts. */
export function formatCostCny4(cny4: number | null | undefined): string {
  if (typeof cny4 !== 'number' || !Number.isFinite(cny4)) return '--'
  return `¥${(cny4 / 10000).toFixed(2)}`
}

export function formatPercent(ratio: number | null | undefined): string {
  if (typeof ratio !== 'number' || !Number.isFinite(ratio)) return '--'
  return `${(ratio * 100).toFixed(0)}%`
}

function positiveInt(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) && v > 0 ? v : null
}

/** A finite number, else null (an unknown window is null, never 0). */
function finiteOrNull(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

/** Map one server roll-up row; the server already folded plan and key ends. */
export function probeFromSummary(item: HealthSummaryItem): ChannelProbe {
  return {
    channelId: item.id,
    name: item.name,
    expiresAt: positiveInt(item.expires_at),
    windowUsedPct: finiteOrNull(item.window_max_used_pct),
    routable: item.routable === true,
    reasons: Array.isArray(item.reasons) ? item.reasons : [],
    cooldownUntil: positiveInt(item.cooldown_until),
    lastError: item.last_error ?? '',
  }
}

/** Plan end within 72h (already-expired included), soonest first. */
export function expiringSoon(
  probes: ChannelProbe[],
  nowSec: number
): ChannelProbe[] {
  return probes
    .filter(
      (p) => p.expiresAt !== null && p.expiresAt - nowSec < EXPIRY_WARN_SECONDS
    )
    .sort((a, b) => (a.expiresAt ?? 0) - (b.expiresAt ?? 0))
}

/** Plan window above 90% used, fullest first. */
export function windowNearLimit(probes: ChannelProbe[]): ChannelProbe[] {
  return probes
    .filter(
      (p) => p.windowUsedPct !== null && p.windowUsedPct > WINDOW_WARN_PCT
    )
    .sort((a, b) => (b.windowUsedPct ?? 0) - (a.windowUsedPct ?? 0))
}

const isManualOnly = (p: ChannelProbe) =>
  p.reasons.length === 1 && p.reasons[0] === 'disabled_manual'

/** Channels that cannot take traffic now, manual switch-offs last. */
export function unroutable(probes: ChannelProbe[]): ChannelProbe[] {
  return probes
    .filter((p) => !p.routable)
    .sort(
      (a, b) =>
        Number(isManualOnly(a)) - Number(isManualOnly(b)) ||
        a.channelId - b.channelId
    )
}

/** Unroutable channels per reason code (a channel counts once per reason). */
export function reasonTotals(probes: ChannelProbe[]): [string, number][] {
  const counts = new Map<string, number>()
  for (const p of unroutable(probes)) {
    for (const r of p.reasons) counts.set(r, (counts.get(r) ?? 0) + 1)
  }
  return [...counts.entries()].sort(
    (a, b) => b[1] - a[1] || a[0].localeCompare(b[0])
  )
}

export function totalsOf(channels: SummaryChannel[]) {
  let cost = 0
  let requests = 0
  let quota = 0
  for (const c of channels) {
    cost += c.cost_cny4
    requests += c.requests
    quota += c.quota
  }
  return { cost, requests, quota }
}
