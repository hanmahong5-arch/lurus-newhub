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

/** One row of GET /api/v2/~/channels/health-summary. */
export interface HealthSummaryItem {
  id: number
  name: string
  routable: boolean
  reasons: string[]
  /** Earliest plan or key end, unix seconds; 0 = none declared. */
  expires_at: number
  /** Fullest plan window in percent; null when no snapshot is known. */
  window_max_used_pct: number | null
  cooldown_until?: number
  last_error?: string
}

export interface HealthSummaryBody {
  channels?: HealthSummaryItem[]
  total?: number
  /** The server capped the list. */
  truncated?: boolean
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

export interface ProbeResult {
  probes: ChannelProbe[]
  /** The server capped the channel list; later channels are not shown. */
  truncated: boolean
}
