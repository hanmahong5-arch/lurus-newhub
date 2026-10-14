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
/**
 * Pure data layer of the audit page: wire shapes of the two audit feeds
 * (the tenant's own trail and the platform-wide feed), filter -> query
 * mapping, detail-blob reading, the chain-verify report. Nothing renders.
 *
 * Sources:
 *   tenant   GET /api/v2/~/audit               handler/v2_tenant_audit.go
 *   platform GET /api/v2/admin/audit/events    handler/audit.go
 *            GET /api/v2/admin/audit/actions   handler/v2_admin_audit.go
 *            GET /api/v2/admin/audit/chain-verify
 */

export const AUDIT_PAGE_SIZE = 20

export type AuditScope = 'tenant' | 'platform'

export interface AuditRow {
  id: number
  /** Platform feed only; the tenant feed is implicitly the caller's tenant. */
  tenantId: string
  timestamp: number
  actorType: string
  actorId: number
  action: string
  resource: string
  resourceId: number
  ip: string
  requestId: string
  details: string
  /** Platform feed only: hash-chain links ('' = unchained legacy row). */
  rowHash: string
  prevHash: string
}

export interface AuditPage {
  items: AuditRow[]
  total: number
}

function rec(v: unknown): Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : {}
}
function num(v: unknown, fallback = 0): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : fallback
}
function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

export function mapAuditRow(raw: unknown): AuditRow {
  const e = rec(raw)
  return {
    id: num(e.id),
    tenantId: str(e.tenant_id),
    timestamp: num(e.timestamp),
    actorType: str(e.actor_type),
    actorId: num(e.actor_id),
    action: str(e.action),
    resource: str(e.resource),
    resourceId: num(e.resource_id),
    ip: str(e.ip),
    requestId: str(e.request_id),
    details: str(e.details),
    rowHash: str(e.row_hash),
    prevHash: str(e.prev_hash),
  }
}

/** Tenant feed: `{ items, total, page, page_size }`. */
export function mapTenantAuditPage(raw: unknown): AuditPage {
  const r = rec(raw)
  const items = (Array.isArray(r.items) ? r.items : []).map(mapAuditRow)
  return { items, total: num(r.total, items.length) }
}

/** Platform feed: `{ events, total, page }`. */
export function mapPlatformAuditPage(raw: unknown): AuditPage {
  const r = rec(raw)
  const items = (Array.isArray(r.events) ? r.events : []).map(mapAuditRow)
  return { items, total: num(r.total, items.length) }
}

export interface AuditFilters {
  action: string
  /** Operator (user id) as typed; validated by auditFilterError. */
  actorId: string
  /** Platform feed only. */
  resource: string
  /** `datetime-local` values; '' = unset. */
  start: string
  end: string
}

export const EMPTY_AUDIT_FILTERS: AuditFilters = {
  action: '',
  actorId: '',
  resource: '',
  start: '',
  end: '',
}

function toSec(value: string): number | undefined {
  if (!value) return undefined
  const ms = new Date(value).getTime()
  return Number.isFinite(ms) ? Math.floor(ms / 1000) : undefined
}

/** Filters -> query params; blank or invalid fields are omitted. */
export function auditParams(
  f: AuditFilters,
  scope: AuditScope
): Record<string, string | number> {
  const p: Record<string, string | number> = {}
  const action = f.action.trim()
  if (action) p.action = action
  const actor = Number(f.actorId.trim())
  if (f.actorId.trim() !== '' && Number.isInteger(actor) && actor > 0) {
    p.actor_id = actor
  }
  const resource = f.resource.trim()
  if (scope === 'platform' && resource) p.resource = resource
  const start = toSec(f.start)
  if (start !== undefined) p.start_time = start
  const end = toSec(f.end)
  if (end !== undefined) p.end_time = end
  return p
}

/** null when the filters are consistent, else the problem. */
export function auditFilterError(f: AuditFilters): 'actor' | 'range' | null {
  if (f.actorId.trim() !== '') {
    const n = Number(f.actorId.trim())
    if (!Number.isInteger(n) || n <= 0) return 'actor'
  }
  const start = toSec(f.start)
  const end = toSec(f.end)
  if (start !== undefined && end !== undefined && start > end) return 'range'
  return null
}

export function auditPageCount(total: number, size = AUDIT_PAGE_SIZE): number {
  return Math.max(1, Math.ceil(total / size))
}

/**
 * Join CSV pages: the first keeps its header row, later ones drop theirs
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

/* ---------------------------- detail blob ---------------------------- */

export interface DetailEntry {
  key: string
  /** Scalars render inline; objects / arrays render as pretty JSON. */
  value: string
  nested: boolean
}

export type ParsedDetails =
  | { kind: 'empty' }
  | { kind: 'object'; entries: DetailEntry[]; raw: string }
  | { kind: 'text'; text: string }

/** The server's mask for secrets / supply-chain keys in the tenant feed. */
export const REDACTED = '[redacted]'

/** Read the `details` column: JSON objects become rows, anything else text. */
export function parseDetails(details: string): ParsedDetails {
  const raw = details.trim()
  if (raw === '') return { kind: 'empty' }
  try {
    const parsed: unknown = JSON.parse(raw)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      const entries = Object.entries(parsed as Record<string, unknown>).map(
        ([key, value]): DetailEntry => {
          if (value !== null && typeof value === 'object') {
            return { key, value: JSON.stringify(value, null, 2), nested: true }
          }
          return { key, value: value === null ? 'null' : String(value), nested: false }
        }
      )
      return { kind: 'object', entries, raw: JSON.stringify(parsed, null, 2) }
    }
    if (Array.isArray(parsed)) {
      return {
        kind: 'object',
        entries: [
          { key: 'items', value: JSON.stringify(parsed, null, 2), nested: true },
        ],
        raw: JSON.stringify(parsed, null, 2),
      }
    }
  } catch {
    // Not JSON: fall through to plain text.
  }
  return { kind: 'text', text: raw }
}

/** One-line preview of a details blob for the table cell. */
export function detailsPreview(details: string): string {
  const parsed = parseDetails(details)
  if (parsed.kind === 'empty') return ''
  if (parsed.kind === 'text') return parsed.text
  return parsed.entries
    .map((e) => `${e.key}=${e.nested ? JSON.stringify(JSON.parse(e.value)) : e.value}`)
    .join(' · ')
}

/* ---------------------------- chain verify ---------------------------- */

export interface ChainBreak {
  id: number
  expected: string
  actual: string
}

export interface ChainReport {
  checked: number
  legacyRows: number
  hashBreaks: number
  firstBreak: ChainBreak | null
  linkBreaks: number
  firstLinkBreak: ChainBreak | null
  linkChecksSkipped: boolean
  /** Non-zero when more rows remain; pass back as `after_id`. */
  nextCursor: number
}

function mapBreak(raw: unknown): ChainBreak | null {
  if (raw === null || typeof raw !== 'object') return null
  const b = rec(raw)
  return { id: num(b.id), expected: str(b.expected), actual: str(b.actual) }
}

export function mapChainReport(raw: unknown): ChainReport {
  const r = rec(raw)
  return {
    checked: num(r.checked),
    legacyRows: num(r.legacy_rows),
    hashBreaks: num(r.hash_breaks),
    firstBreak: mapBreak(r.first_break),
    linkBreaks: num(r.link_breaks),
    firstLinkBreak: mapBreak(r.first_link_break),
    linkChecksSkipped: r.link_checks_skipped === true,
    nextCursor: num(r.next_cursor),
  }
}

/** The window is intact only if neither check found a break. */
export function chainIntact(r: ChainReport): boolean {
  return r.hashBreaks === 0 && r.linkBreaks === 0
}

/** Short form of a 64-hex hash for display; '' stays '' (legacy row). */
export function shortHash(hash: string): string {
  return hash.length > 12 ? `${hash.slice(0, 8)}…${hash.slice(-4)}` : hash
}
