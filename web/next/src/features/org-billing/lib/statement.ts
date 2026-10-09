/** Mapping and arithmetic for GET /api/v2/~/billing/statement. */

export const GROUP_BYS = ['project', 'employee', 'token', 'model'] as const
export type GroupBy = (typeof GROUP_BYS)[number]

/** One statement line; cny4 fields are in 0.0001 CNY. */
export interface StatementRow {
  key: string
  label: string
  requests: number
  quota: number
  charged_cny4: number
  priced_cny4: number
}

export interface StatementData {
  month: string
  group_by: string
  rows: StatementRow[]
  totals: StatementRow
}

const NUMERIC = ['requests', 'quota', 'charged_cny4', 'priced_cny4'] as const

function num(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : 0
}

function toRow(raw: unknown): StatementRow {
  const r = (raw ?? {}) as Record<string, unknown>
  return {
    key: typeof r.key === 'string' ? r.key : String(r.key ?? ''),
    label: typeof r.label === 'string' ? r.label : '',
    requests: num(r.requests),
    quota: num(r.quota),
    charged_cny4: num(r.charged_cny4),
    priced_cny4: num(r.priced_cny4),
  }
}

/** Tolerate a null `rows` and missing numeric fields. */
export function normalizeStatement(raw: unknown): StatementData {
  const d = (raw ?? {}) as Record<string, unknown>
  return {
    month: typeof d.month === 'string' ? d.month : '',
    group_by: typeof d.group_by === 'string' ? d.group_by : '',
    rows: Array.isArray(d.rows) ? d.rows.map(toRow) : [],
    totals: toRow(d.totals),
  }
}

export interface Mismatch {
  field: (typeof NUMERIC)[number]
  rowsSum: number
  total: number
}

/**
 * The server promises totals == sum of rows. Re-check it here and return
 * every field where it does not hold; the page shows these, never hides them.
 */
export function findMismatches(data: StatementData): Mismatch[] {
  const out: Mismatch[] = []
  for (const field of NUMERIC) {
    const rowsSum = data.rows.reduce((s, r) => s + r[field], 0)
    if (rowsSum !== data.totals[field]) {
      out.push({ field, rowsSum, total: data.totals[field] })
    }
  }
  return out
}

/** 0.0001 CNY units -> "¥12.3456". */
export function formatCny4(units: number | null | undefined): string {
  if (typeof units !== 'number' || !Number.isFinite(units)) return '--'
  return `¥${(units / 10000).toFixed(4)}`
}

/** The wallet balance is plain CNY. */
export function formatCny(v: number | null | undefined): string {
  if (typeof v !== 'number' || !Number.isFinite(v)) return '--'
  return `¥${v.toFixed(2)}`
}

export const MONTH_RE = /^\d{4}-(0[1-9]|1[0-2])$/

export function isValidMonth(m: string): boolean {
  return MONTH_RE.test(m)
}

export function currentMonth(now: Date = new Date()): string {
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}

export function statementQuery(
  month: string,
  groupBy: GroupBy,
  format?: 'csv'
): string {
  const p = new URLSearchParams({ month, group_by: groupBy })
  if (format) p.set('format', format)
  return p.toString()
}

/** Row label for display: the label when present, else the key. */
export function rowName(r: StatementRow): string {
  return r.label || r.key || '--'
}

/** Invoice month bucket (GET /billing/invoices). */
export interface InvoiceMonth {
  month: string
  quota: number
  amount_cny: number
  request_count: number
  unbilled_quota: number
  unbilled_request_count: number
  estimated: boolean
}

export function normalizeInvoices(raw: unknown): InvoiceMonth[] {
  const items = (raw as { items?: unknown } | null)?.items
  if (!Array.isArray(items)) return []
  return items.map((i) => {
    const r = (i ?? {}) as Record<string, unknown>
    return {
      month: typeof r.month === 'string' ? r.month : '',
      quota: num(r.quota),
      amount_cny: num(r.amount_cny),
      request_count: num(r.request_count),
      unbilled_quota: num(r.unbilled_quota),
      unbilled_request_count: num(r.unbilled_request_count),
      estimated: r.estimated === true,
    }
  })
}

/** Platform wallet summary (GET /api/v2/user/billing/summary). */
export interface WalletSummary {
  balance: number | null
  frozen: number | null
  available: number | null
}

export function normalizeSummary(raw: unknown): WalletSummary {
  const r = (raw ?? {}) as Record<string, unknown>
  const n = (v: unknown) =>
    typeof v === 'number' && Number.isFinite(v) ? v : null
  return {
    balance: n(r.balance ?? r.wallet_balance_cny),
    frozen: n(r.frozen),
    available: n(r.available),
  }
}
