import type {
  ChannelDetail,
  ChannelHealth,
  ChannelProbe,
  SummaryChannel,
} from './types'

/** channel status 2 = switched off by an operator (common.ChannelStatusManuallyDisabled). */
export const STATUS_MANUALLY_DISABLED = 2

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

/**
 * Earliest declared end of a channel: the channel-level plan end inside
 * `setting`, or any per-key end in `multi_key_meta`. null when none is set;
 * a malformed `setting` string is treated as "none declared".
 */
export function earliestExpiry(detail: ChannelDetail): number | null {
  const candidates: number[] = []
  if (typeof detail.setting === 'string' && detail.setting.trim() !== '') {
    try {
      const parsed: unknown = JSON.parse(detail.setting)
      if (typeof parsed === 'object' && parsed !== null) {
        const v = positiveInt((parsed as { expires_at?: unknown }).expires_at)
        if (v !== null) candidates.push(v)
      }
    } catch {
      // not a JSON document: no declared end
    }
  }
  const meta = detail.channel_info?.multi_key_meta
  if (meta && typeof meta === 'object') {
    for (const m of Object.values(meta)) {
      const v = positiveInt(m?.expires_at)
      if (v !== null) candidates.push(v)
    }
  }
  return candidates.length > 0 ? Math.min(...candidates) : null
}

/**
 * Highest used percent in a health `window` snapshot. Accepts either
 * `{used_pct}` or `{windows:[{used_pct}]}` (planquota.Snapshot); anything
 * else means no window is known (null), never 0.
 */
export function maxWindowUsedPct(
  window: Record<string, unknown> | null | undefined
): number | null {
  if (!window || typeof window !== 'object') return null
  const values: number[] = []
  const direct = window.used_pct
  if (typeof direct === 'number' && Number.isFinite(direct)) values.push(direct)
  const list = window.windows
  if (Array.isArray(list)) {
    for (const w of list) {
      const p = (w as { used_pct?: unknown } | null)?.used_pct
      if (typeof p === 'number' && Number.isFinite(p)) values.push(p)
    }
  }
  return values.length > 0 ? Math.max(...values) : null
}

export function buildProbe(
  channel: SummaryChannel,
  health: ChannelHealth,
  detail: ChannelDetail
): ChannelProbe {
  return {
    channelId: channel.channel_id,
    name: channel.name,
    expiresAt: earliestExpiry(detail),
    windowUsedPct: maxWindowUsedPct(health.window),
    routable: health.routable,
    reasons: Array.isArray(health.reasons) ? health.reasons : [],
    cooldownUntil: positiveInt(health.cooldown_until),
    lastError: health.last_error ?? '',
  }
}

/** A channel an operator switched off is not read; its state is already known. */
export function manualDisabledProbe(channel: SummaryChannel): ChannelProbe {
  return {
    channelId: channel.channel_id,
    name: channel.name,
    expiresAt: null,
    windowUsedPct: null,
    routable: false,
    reasons: ['disabled_manual'],
    cooldownUntil: null,
    lastError: '',
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
