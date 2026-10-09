/**
 * Wire shapes and pure helpers for the tenant data-and-security page.
 * Sources of truth (Go): v2_tenant_model_selection.go, v2_data_policy.go,
 * v2_tenant_audit.go, app/contentpolicy.
 */

function num(v: unknown, fallback = 0): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : fallback
}
function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}
function rec(v: unknown): Record<string, unknown> {
  return typeof v === 'object' && v !== null
    ? (v as Record<string, unknown>)
    : {}
}
function strList(v: unknown): string[] {
  return Array.isArray(v)
    ? v.filter((x): x is string => typeof x === 'string')
    : []
}

/* ------------------------------ model allow-list ------------------------------ */

export interface Allowlist {
  platformAllowed: string[]
  /** No platform ceiling configured: every model is grantable. */
  platformUnrestricted: boolean
  tenantSelected: string[]
  tenantConfigured: boolean
  effective: string[]
}

export function mapAllowlist(raw: unknown): Allowlist {
  const r = rec(raw)
  return {
    platformAllowed: strList(r.platform_allowed),
    platformUnrestricted: r.platform_unrestricted === true,
    tenantSelected: strList(r.tenant_selected),
    tenantConfigured: r.tenant_configured === true,
    effective: strList(r.effective),
  }
}

/** Mirror of tenantpolicy.ModelAllowed: exact name, or trailing-"*" prefix. */
export function modelAllowed(list: string[], model: string): boolean {
  return list.some((e) => {
    const entry = e.trim()
    if (entry.endsWith('*')) return model.startsWith(entry.slice(0, -1))
    return entry === model
  })
}

/** Mirror of tenantpolicy.Covered: the entry grants nothing beyond `ceiling`. */
export function covered(ceiling: string[], entry: string): boolean {
  const e = entry.trim()
  if (!e.endsWith('*')) return modelAllowed(ceiling, e)
  const p = e.slice(0, -1)
  return ceiling.some((c) => {
    const ct = c.trim()
    return ct.endsWith('*') && p.startsWith(ct.slice(0, -1))
  })
}

export type EntryCheck =
  | 'ok'
  | 'blank'
  | 'duplicate'
  | 'wildcard'
  | 'too-long'
  | 'not-granted'

/** Client-side mirror of validateAllowlistEntries + the platform ceiling check. */
export function checkEntry(
  raw: string,
  current: string[],
  ceiling: string[] | null
): EntryCheck {
  const e = raw.trim()
  if (e === '') return 'blank'
  if (new TextEncoder().encode(e).length > 128) return 'too-long'
  const star = e.indexOf('*')
  if (star >= 0 && star !== e.length - 1) return 'wildcard'
  if (current.includes(e)) return 'duplicate'
  if (ceiling !== null && !covered(ceiling, e)) return 'not-granted'
  return 'ok'
}

/**
 * Starting selection for the editor: what the tenant picked, or - when it has
 * never narrowed - everything it effectively has today.
 */
export function initialSelection(a: Allowlist): string[] {
  if (a.tenantConfigured) return a.tenantSelected
  return a.effective.filter((m) => m !== '*')
}

export function sameSet(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((x) => b.includes(x))
}

/* ------------------------------ retention ------------------------------ */

export type Retention = '' | 'full' | 'metadata_only' | 'none'
export const RETENTION_ORDER: Retention[] = ['full', 'metadata_only', 'none']

function asRetention(v: unknown): Retention {
  const s = str(v)
  return s === 'full' || s === 'metadata_only' || s === 'none' ? s : ''
}

export interface RetentionView {
  platformDefault: Retention
  tenant: Retention
  effective: Retention
}

export function mapRetention(raw: unknown): RetentionView {
  const r = rec(raw)
  return {
    platformDefault: asRetention(r.platform_default),
    tenant: asRetention(r.tenant),
    effective: asRetention(r.effective) || 'full',
  }
}

function strictness(m: Retention): number {
  const i = RETENTION_ORDER.indexOf(m)
  return i < 0 ? 0 : i
}

/** True when `candidate` would be looser than `floor` (inherit never is). */
export function looser(candidate: Retention, floor: Retention): boolean {
  if (candidate === '') return false
  return strictness(candidate) < strictness(floor)
}

export interface TokenRetentionResult {
  tokenId: number
  contentRetention: Retention
  effective: Retention
}

export function mapTokenRetention(raw: unknown): TokenRetentionResult {
  const r = rec(raw)
  return {
    tokenId: num(r.token_id),
    contentRetention: asRetention(r.content_retention),
    effective: asRetention(r.effective) || 'full',
  }
}

export interface TokenOption {
  id: number
  name: string
  key: string
}

export function mapTokenOptions(raw: unknown): {
  items: TokenOption[]
  total: number
} {
  const r = rec(raw)
  const items = (Array.isArray(r.items) ? r.items : [])
    .map((x) => {
      const t = rec(x)
      return { id: num(t.id), name: str(t.name), key: str(t.key) }
    })
    .filter((t) => t.id > 0)
  return { items, total: num(r.total, items.length) }
}

/* ------------------------------ content rules ------------------------------ */

export const ROLE_SCOPES = ['any', 'system', 'user', 'assistant'] as const
export const RULE_KINDS = ['mask', 'reject'] as const
export const RULE_MODES = ['observe', 'enforce'] as const
export const PATTERN_TYPES = ['builtin', 'regex'] as const

export interface ContentRule {
  id: number
  ordinal: number
  name: string
  roleScope: string
  kind: string
  patternType: string
  builtin: string
  pattern: string
  replacement: string
  mode: string
  enabled: boolean
  updatedAt: number
}

export interface RulesView {
  rules: ContentRule[]
  builtins: string[]
  maxRules: number
  maxPatternLen: number
}

export function mapRule(raw: unknown): ContentRule {
  const r = rec(raw)
  return {
    id: num(r.id),
    ordinal: num(r.ordinal),
    name: str(r.name),
    roleScope: str(r.role_scope) || 'any',
    kind: str(r.kind) || 'mask',
    patternType: str(r.pattern_type) || 'builtin',
    builtin: str(r.builtin),
    pattern: str(r.pattern),
    replacement: str(r.replacement),
    mode: str(r.mode) || 'observe',
    enabled: r.enabled !== false,
    updatedAt: num(r.updated_at),
  }
}

export function mapRules(raw: unknown): RulesView {
  const r = rec(raw)
  return {
    rules: (Array.isArray(r.rules) ? r.rules : []).map(mapRule),
    builtins: strList(r.builtins),
    maxRules: num(r.max_rules),
    maxPatternLen: num(r.max_pattern_len, 512),
  }
}

export interface RuleForm {
  name: string
  roleScope: string
  kind: string
  patternType: string
  builtin: string
  pattern: string
  replacement: string
  mode: string
  enabled: boolean
  ordinal: string
}

export function emptyRuleForm(builtins: string[]): RuleForm {
  return {
    name: '',
    roleScope: 'any',
    kind: 'mask',
    patternType: 'builtin',
    builtin: builtins[0] ?? '',
    pattern: '',
    replacement: '',
    // New rules start in observe: look at the hits before blocking anything.
    mode: 'observe',
    enabled: true,
    ordinal: '0',
  }
}

export function formFromRule(r: ContentRule): RuleForm {
  return {
    name: r.name,
    roleScope: r.roleScope,
    kind: r.kind,
    patternType: r.patternType,
    builtin: r.builtin,
    pattern: r.pattern,
    replacement: r.replacement,
    mode: r.mode,
    enabled: r.enabled,
    ordinal: String(r.ordinal),
  }
}

export type RuleFormError =
  | 'name'
  | 'builtin'
  | 'pattern'
  | 'pattern-long'
  | 'ordinal'

export type RuleBuild =
  | { ok: true; body: Record<string, unknown> }
  | { ok: false; error: RuleFormError }

/**
 * Form -> request body. The server replaces every field on update, so the
 * whole rule is always sent; builtin rules carry no pattern and regex rules no
 * builtin (the server rejects the mix).
 */
export function buildRuleBody(f: RuleForm, maxPatternLen: number): RuleBuild {
  const name = f.name.trim()
  if (name === '' || new TextEncoder().encode(name).length > 64) {
    return { ok: false, error: 'name' }
  }
  const ordinal = Number(f.ordinal.trim() === '' ? '0' : f.ordinal)
  if (!Number.isInteger(ordinal) || ordinal < 0) {
    return { ok: false, error: 'ordinal' }
  }
  const body: Record<string, unknown> = {
    name,
    role_scope: f.roleScope,
    kind: f.kind,
    pattern_type: f.patternType,
    mode: f.mode,
    enabled: f.enabled,
    ordinal,
    replacement: f.kind === 'mask' ? f.replacement.trim() : '',
  }
  if (f.patternType === 'builtin') {
    if (f.builtin === '') return { ok: false, error: 'builtin' }
    body.builtin = f.builtin
    body.pattern = ''
  } else {
    const pattern = f.pattern
    if (pattern.trim() === '') return { ok: false, error: 'pattern' }
    if (pattern.length > maxPatternLen) {
      return { ok: false, error: 'pattern-long' }
    }
    // RE2 (server) and JavaScript regexes differ, e.g. `(?i)secret` is valid RE2
    // but throws in JS, so no local syntax check: the server's INVALID_RULE
    // message is shown on rejection.
    body.builtin = ''
    body.pattern = pattern
  }
  return { ok: true, body }
}

/* ------------------------------ audit log ------------------------------ */

export interface AuditEvent {
  id: number
  timestamp: number
  actorType: string
  actorId: number
  action: string
  resource: string
  resourceId: number
  ip: string
  requestId: string
  details: string
}

export interface AuditPage {
  items: AuditEvent[]
  total: number
  page: number
  pageSize: number
}

export function mapAuditPage(raw: unknown): AuditPage {
  const r = rec(raw)
  const items = (Array.isArray(r.items) ? r.items : []).map((x) => {
    const e = rec(x)
    return {
      id: num(e.id),
      timestamp: num(e.timestamp),
      actorType: str(e.actor_type),
      actorId: num(e.actor_id),
      action: str(e.action),
      resource: str(e.resource),
      resourceId: num(e.resource_id),
      ip: str(e.ip),
      requestId: str(e.request_id),
      details: str(e.details),
    }
  })
  return {
    items,
    total: num(r.total, items.length),
    page: num(r.page, 1),
    pageSize: num(r.page_size, items.length),
  }
}

export interface AuditFilters {
  action: string
  actorId: string
  /** yyyy-mm-dd, local day. */
  from: string
  to: string
}

export const EMPTY_AUDIT_FILTERS: AuditFilters = {
  action: '',
  actorId: '',
  from: '',
  to: '',
}

export const AUDIT_PAGE_SIZE = 20

function dayStart(day: string): number | undefined {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day)
  if (!m) return undefined
  return Math.floor(
    new Date(+m[1], +m[2] - 1, +m[3], 0, 0, 0).getTime() / 1000
  )
}

function dayEnd(day: string): number | undefined {
  const start = dayStart(day)
  return start === undefined ? undefined : start + 86400 - 1
}

/** Filters -> query params; blank / invalid fields are omitted. */
export function auditParams(f: AuditFilters): Record<string, string | number> {
  const p: Record<string, string | number> = {}
  const action = f.action.trim()
  if (action !== '') p.action = action
  const actor = Number(f.actorId.trim())
  if (f.actorId.trim() !== '' && Number.isInteger(actor) && actor > 0) {
    p.actor_id = actor
  }
  const from = dayStart(f.from)
  if (from !== undefined) p.start_time = from
  const to = dayEnd(f.to)
  if (to !== undefined) p.end_time = to
  return p
}

/** null when the filters are consistent, else the problem. */
export function auditFilterError(f: AuditFilters): 'actor' | 'range' | null {
  if (f.actorId.trim() !== '') {
    const n = Number(f.actorId.trim())
    if (!Number.isInteger(n) || n <= 0) return 'actor'
  }
  const from = dayStart(f.from)
  const to = dayEnd(f.to)
  if (from !== undefined && to !== undefined && from > to) return 'range'
  return null
}

export function pageCount(total: number, size = AUDIT_PAGE_SIZE): number {
  return Math.max(1, Math.ceil(total / size))
}

/**
 * Join CSV pages: the first page keeps its header row, later pages drop theirs
 * (every page the server returns starts with the same header line).
 */
export function joinCsvPages(pages: string[]): string {
  return pages
    .map((p, i) => {
      if (i === 0) return p
      const nl = p.indexOf('\n')
      return nl < 0 ? '' : p.slice(nl + 1)
    })
    .join('')
}
