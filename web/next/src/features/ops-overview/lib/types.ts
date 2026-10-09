/** One channel line of GET /api/v2/~/channels/usage-summary (channelusage.SummaryRow). */
export interface SummaryChannel {
  channel_id: number
  name: string
  status: number
  requests: number
  errors: number
  prompt_tokens: number
  completion_tokens: number
  /** Internal quota units; show through lib/money.ts. */
  quota: number
  /** Upstream cost in 0.0001 CNY. */
  cost_cny4: number
  plan_monthly_fee_cny4: number
  /** Present only for channels that carry a plan fee. 1.0 = the plan's price. */
  utilization?: number
  verdict?: '' | 'idle' | 'overuse'
}

export interface UsageSummary {
  window: string
  since: number
  channels: SummaryChannel[]
  truncated: boolean
}

/** GET /api/v2/~/channels/:id/health (handler.channelHealth), the used part. */
export interface ChannelHealth {
  channel_id: number
  routable: boolean
  reasons: string[]
  cooldown_until?: number
  /** Plan-window snapshot; null/absent when none is known. */
  window?: Record<string, unknown> | null
  last_error?: string
}

/** The part of GET /api/v2/~/channels/:id (repo.Channel) used for expiry. */
export interface ChannelDetail {
  id: number
  /** JSON document string (dto.ChannelSettings); carries expires_at. */
  setting?: string | null
  channel_info?: {
    multi_key_meta?: Record<string, { expires_at?: number } | undefined> | null
  } | null
}

export type ModelState = 'operational' | 'degraded' | 'down'

export interface PublicModelStatus {
  models: { model: string; status: ModelState }[]
  updated_at: number
}

/** Everything the operator view derives per channel. */
export interface ChannelProbe {
  channelId: number
  name: string
  /** Earliest plan/key end (unix seconds), null = none declared. */
  expiresAt: number | null
  /** Highest used percent over the known plan windows, null = unknown. */
  windowUsedPct: number | null
  routable: boolean
  reasons: string[]
  cooldownUntil: number | null
  lastError: string
}

export interface ProbeFailure {
  channelId: number
  name: string
  message: string
}

export interface ProbeResult {
  probes: ChannelProbe[]
  failures: ProbeFailure[]
  /** Channels beyond the probe cap that were not read. */
  skipped: number
}
