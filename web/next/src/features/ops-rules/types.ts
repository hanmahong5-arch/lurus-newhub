export type RuleKind = 'mask' | 'reject'
export type RuleMode = 'observe' | 'enforce'
export type RulePatternType = 'builtin' | 'regex'
export type RuleRoleScope = 'any' | 'system' | 'user' | 'assistant'

export const RULE_KINDS: RuleKind[] = ['mask', 'reject']
export const RULE_MODES: RuleMode[] = ['observe', 'enforce']
export const RULE_PATTERN_TYPES: RulePatternType[] = ['builtin', 'regex']
export const RULE_ROLE_SCOPES: RuleRoleScope[] = [
  'any',
  'system',
  'user',
  'assistant',
]

/** One platform content rule (GET /api/v2/admin/content-rules). */
export interface ContentRule {
  id: number
  ordinal: number
  name: string
  role_scope: string
  kind: string
  pattern_type: string
  builtin: string
  pattern: string
  replacement: string
  mode: string
  enabled: boolean
  updated_at: number
}

export interface RuleList {
  rules: ContentRule[]
  builtins: string[]
  max_rules: number
  max_pattern_len: number
}

/** Body of POST / PUT /admin/content-rules. */
export interface RuleWriteBody {
  ordinal: number
  name: string
  role_scope: string
  kind: string
  pattern_type: string
  builtin: string
  pattern: string
  replacement: string
  mode: string
  enabled: boolean
}

export interface ChannelTemplate {
  id: number
  name: string
  description: string
  param_override: string
  header_override: string
  version: number
  updated_at: number
}

export interface TemplateWriteBody {
  name: string
  description: string
  param_override: string
  header_override: string
}

export interface ApplyChannelResult {
  channel_id: number
  applied: boolean
  error: string
}

export interface ApplyOutcome {
  template_id: number
  template_version: number
  results: ApplyChannelResult[]
}

/** A bulk apply done in this browser session (the server has no read API). */
export interface ApplyRecord extends ApplyOutcome {
  template_name: string
  at: number
}

export interface ChannelOption {
  id: number
  name: string
  type: number
  status: number
}

export interface ChannelPage {
  channels: ChannelOption[]
  total: number
}
