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
import type { MoneyConfig } from '@/lib/money';

export const MAX_ROWS = 500;
/** Backend compares UTF-8 byte length (Go len), not characters. */
export const MAX_NAME_LENGTH = 50;
/** Backend MaxQuotaMultiplier; cap = this * quota-per-unit. */
export const MAX_QUOTA_MULTIPLIER = 1_000_000_000;
const utf8Bytes = (v: string) => new TextEncoder().encode(v).length;
export const EMPLOYEE_REF_RE = /^[A-Za-z0-9._@:-]{1,64}$/;

/** Columns the roster understands. dept_external_code is read but not sent. */
export const KNOWN_COLUMNS = [
  'employee_ref',
  'name',
  'dept_external_code',
  'quota',
] as const;

export interface RosterRow {
  /** 1-based line number in the source text (header excluded). */
  line: number;
  employeeRef: string;
  name: string;
  dept: string;
  /** Raw quota text; empty = unlimited. */
  quotaText: string;
}

export type RowIssue =
  | 'ref_required'
  | 'ref_invalid'
  | 'ref_duplicate'
  | 'name_too_long'
  | 'quota_invalid'
  | 'quota_too_large'
  | 'quota_unavailable';

export interface ValidatedRow extends RosterRow {
  /** Internal quota units; null = unlimited. */
  quota: number | null;
  issues: RowIssue[];
}

export type ParseResult =
  | { ok: true; rows: RosterRow[]; hasDept: boolean }
  | {
      ok: false;
      reason: 'empty' | 'no_employee_ref_column' | 'too_many_rows';
      count?: number;
    };

/** RFC 4180-ish splitter: quoted fields, doubled quotes, CRLF. */
export function parseCsvText(text: string): string[][] {
  const out: string[][] = [];
  let row: string[] = [];
  let field = '';
  let quoted = false;
  const src = text.replace(/^\uFEFF/, '');
  for (let i = 0; i < src.length; i++) {
    const ch = src[i];
    if (quoted) {
      if (ch === '"') {
        if (src[i + 1] === '"') {
          field += '"';
          i++;
        } else quoted = false;
      } else field += ch;
      continue;
    }
    if (ch === '"') quoted = true;
    else if (ch === ',') {
      row.push(field);
      field = '';
    } else if (ch === '\n' || ch === '\r') {
      if (ch === '\r' && src[i + 1] === '\n') i++;
      row.push(field);
      field = '';
      out.push(row);
      row = [];
    } else field += ch;
  }
  if (field !== '' || row.length > 0) {
    row.push(field);
    out.push(row);
  }
  return out.filter((r) => r.some((c) => c.trim() !== ''));
}

/**
 * Text -> rows. A header row is required (employee_ref column). Unknown
 * columns are ignored.
 */
export function parseRoster(text: string): ParseResult {
  const table = parseCsvText(text);
  if (table.length === 0) return { ok: false, reason: 'empty' };
  const header = table[0].map((h) => h.trim().toLowerCase());
  const idx = (name: string) => header.indexOf(name);
  const iRef = idx('employee_ref');
  if (iRef < 0) return { ok: false, reason: 'no_employee_ref_column' };
  const iName = idx('name');
  const iDept = idx('dept_external_code');
  const iQuota = idx('quota');
  const body = table.slice(1);
  if (body.length === 0) return { ok: false, reason: 'empty' };
  if (body.length > MAX_ROWS) {
    return { ok: false, reason: 'too_many_rows', count: body.length };
  }
  const cell = (r: string[], i: number) => (i < 0 ? '' : (r[i] ?? '').trim());
  const rows = body.map<RosterRow>((r, n) => ({
    line: n + 1,
    employeeRef: cell(r, iRef),
    name: cell(r, iName),
    dept: cell(r, iDept),
    quotaText: cell(r, iQuota),
  }));
  return { ok: true, rows, hasDept: rows.some((r) => r.dept !== '') };
}

/** Display-currency amount -> internal quota (same rule as the keys page). */
export function displayToQuota(value: number, config: MoneyConfig): number {
  if (config.displayType === 'TOKENS') return Math.round(value);
  return Math.round((value / config.rate) * config.quotaPerUnit);
}

export function validateRows(
  rows: RosterRow[],
  money: MoneyConfig | null,
): ValidatedRow[] {
  const firstSeen = new Map<string, number>();
  return rows.map((r) => {
    const issues: RowIssue[] = [];
    if (r.employeeRef === '') issues.push('ref_required');
    else if (!EMPLOYEE_REF_RE.test(r.employeeRef)) issues.push('ref_invalid');
    else if (firstSeen.has(r.employeeRef)) issues.push('ref_duplicate');
    else firstSeen.set(r.employeeRef, r.line);
    // buildItems falls back to employee_ref when name is blank.
    if (utf8Bytes(r.name === '' ? r.employeeRef : r.name) > MAX_NAME_LENGTH) {
      issues.push('name_too_long');
    }

    let quota: number | null = null;
    if (r.quotaText !== '') {
      if (!/^\d+(\.\d+)?$/.test(r.quotaText)) issues.push('quota_invalid');
      else if (money === null) issues.push('quota_unavailable');
      else {
        const q = displayToQuota(Number.parseFloat(r.quotaText), money);
        if (q <= 0) issues.push('quota_invalid');
        else if (q > MAX_QUOTA_MULTIPLIER * money.quotaPerUnit) {
          issues.push('quota_too_large');
        } else quota = q;
      }
    }
    return { ...r, quota, issues };
  });
}

export interface BatchItemBody {
  name: string;
  employee_ref: string;
  unlimited_quota: boolean;
  remain_quota: number;
}

/** Wire body. A blank quota means unlimited (0 remaining would be a dead key). */
export function buildItems(rows: ValidatedRow[]): BatchItemBody[] {
  return rows.map((r) => ({
    name: r.name === '' ? r.employeeRef : r.name,
    employee_ref: r.employeeRef,
    unlimited_quota: r.quota === null,
    remain_quota: r.quota ?? 0,
  }));
}

/** Guard against spreadsheet formula injection in a downloaded CSV. */
function csvCell(value: string): string {
  const safe = /^[=+\-@\t\r]/.test(value) ? `'${value}` : value;
  return /[",\n\r]/.test(safe) ? `"${safe.replaceAll('"', '""')}"` : safe;
}

export interface IssuedRow {
  employee_ref: string;
  name: string;
  key: string;
}

export function issuedToCsv(rows: IssuedRow[]): string {
  const lines = ['employee_ref,name,key'];
  for (const r of rows) {
    lines.push([r.employee_ref, r.name, r.key].map(csvCell).join(','));
  }
  return `${lines.join('\r\n')}\r\n`;
}

export const TEMPLATE_CSV =
  'employee_ref,name,dept_external_code,quota\r\nE1001,Alice,D-01,10\r\nE1002,Bob,D-02,\r\n';
