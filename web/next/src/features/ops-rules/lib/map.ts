import type {
  ApplyChannelResult,
  ApplyOutcome,
  ChannelOption,
  ChannelPage,
  ChannelTemplate,
  ContentRule,
  RuleList,
} from '../types'

function num(value: unknown, fallback = 0): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function record(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null
    ? (value as Record<string, unknown>)
    : {}
}

export function mapRule(raw: unknown): ContentRule {
  const r = record(raw)
  return {
    id: num(r.id),
    ordinal: num(r.ordinal),
    name: str(r.name),
    role_scope: str(r.role_scope) || 'any',
    kind: str(r.kind) || 'mask',
    pattern_type: str(r.pattern_type) || 'builtin',
    builtin: str(r.builtin),
    pattern: str(r.pattern),
    replacement: str(r.replacement),
    mode: str(r.mode) || 'observe',
    enabled: r.enabled !== false,
    updated_at: num(r.updated_at),
  }
}

/** { rules, builtins, max_rules, max_pattern_len }; a missing array is empty. */
export function mapRuleList(raw: unknown): RuleList {
  const r = record(raw)
  return {
    rules: Array.isArray(r.rules) ? r.rules.map(mapRule) : [],
    builtins: Array.isArray(r.builtins)
      ? r.builtins.filter((b): b is string => typeof b === 'string')
      : [],
    max_rules: num(r.max_rules),
    max_pattern_len: num(r.max_pattern_len),
  }
}

export function mapTemplate(raw: unknown): ChannelTemplate {
  const r = record(raw)
  return {
    id: num(r.id),
    name: str(r.name),
    description: str(r.description),
    param_override: str(r.param_override),
    header_override: str(r.header_override),
    version: num(r.version, 1),
    updated_at: num(r.updated_at),
  }
}

/** GET /admin/channel-templates answers a bare array in `data`. */
export function mapTemplateList(raw: unknown): ChannelTemplate[] {
  return Array.isArray(raw) ? raw.map(mapTemplate) : []
}

function mapApplyResult(raw: unknown): ApplyChannelResult {
  const r = record(raw)
  return {
    channel_id: num(r.channel_id),
    applied: r.applied === true,
    error: str(r.error),
  }
}

export function mapApplyOutcome(raw: unknown): ApplyOutcome {
  const r = record(raw)
  return {
    template_id: num(r.template_id),
    template_version: num(r.template_version),
    results: Array.isArray(r.results) ? r.results.map(mapApplyResult) : [],
  }
}

export function mapChannelPage(raw: unknown): ChannelPage {
  const r = record(raw)
  const channels: ChannelOption[] = Array.isArray(r.channels)
    ? r.channels.map((c) => {
        const x = record(c)
        return {
          id: num(x.id),
          name: str(x.name),
          type: num(x.type),
          status: num(x.status),
        }
      })
    : []
  return { channels, total: num(r.total, channels.length) }
}
