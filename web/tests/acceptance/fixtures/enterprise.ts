import { execFileSync } from 'node:child_process';
import { expect, type APIRequestContext } from '@playwright/test';
import { ENV } from './env';
import { ok } from './provision';

/**
 * Helpers for the enterprise scenarios (TC-E*): one customer company ("acme")
 * as its own tenant, with a payer who is also its admin, two departments, a
 * department lead, a trusted gateway key and an ordinary key.
 */

export interface Dept {
  id: number;
  name: string;
  /** What the customer's gateway sends in X-Lurus-Dept. */
  code: string;
}

export interface EntWorld {
  tenant: { id: string; slug: string };
  /** The payer: tenant admin, owner of every key, linked to a platform wallet. */
  admin: { userId: number; platformAccountId: number; globalRole: number };
  lead: { userId: number };
  depts: { a: Dept; b: Dept };
  keys: {
    /** trusted_identity_headers = true: the customer's own gateway. */
    trusted: { id: number; key: string; name: string };
    /** Ordinary key, issued to one employee (employee_ref set on the key). */
    plain: { id: number; key: string; name: string; employeeRef: string };
  };
  secondDeepseekChannel: number;
}

/** Tenant-scoped v2 path of the enterprise tenant. */
export function entV2(ent: EntWorld, path: string): string {
  return `/api/v2/${ent.tenant.slug}${path}`;
}

/**
 * Runs one SQL statement against the instance's database and returns its
 * output. The API has no way to make the FIRST admin of a customer tenant: the
 * only writer of users.tenant_role = 'admin' is invite redemption, and that
 * runs behind the platform SDK's session, which this stack does not have. So
 * the harness stands in for the invite: exactly one UPDATE, before the user's
 * first request. Everything after that goes through the product.
 */
export function sqlExec(statement: string): string {
  const flags = ['-v', 'ON_ERROR_STOP=1', '-tA', '-c', statement];
  try {
    if (ENV.pgContainer) {
      return execFileSync(
        'docker',
        [
          'exec',
          ENV.pgContainer,
          'psql',
          '-U',
          'postgres',
          '-d',
          'newhub',
          ...flags,
        ],
        { encoding: 'utf8' },
      ).trim();
    }
    if (ENV.sqlDSN) {
      return execFileSync('psql', [ENV.sqlDSN, ...flags], {
        encoding: 'utf8',
      }).trim();
    }
  } catch (err) {
    throw new Error(`sqlExec failed: ${(err as Error).message}`);
  }
  throw new Error(
    'neither ACCEPT_PG_CONTAINER nor ACCEPT_SQL_DSN is set — start the stack with scripts/acceptance-stack.sh',
  );
}

/** The current calendar month in the statement's default zone, YYYY-MM. */
export function thisMonth(tz = 'Asia/Shanghai'): string {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: tz,
    year: 'numeric',
    month: '2-digit',
  }).formatToParts(new Date());
  const get = (t: string) => parts.find((p) => p.type === t)?.value;
  return `${get('year')}-${get('month')}`;
}

/** One relay call in the chat-completions wire, with extra request headers. */
export function chatWith(
  request: APIRequestContext,
  key: string,
  model: string,
  headers: Record<string, string> = {},
) {
  return request.post('/v1/chat/completions', {
    headers: { Authorization: `Bearer ${key}`, ...headers },
    data: { model, messages: [{ role: 'user', content: 'hi' }] },
  });
}

/** The identity headers a customer gateway adds. */
export function identity(employee: string, dept: string) {
  return { 'X-Lurus-Employee': employee, 'X-Lurus-Dept': dept };
}

/** Minimal RFC 4180 parser: quoted fields, doubled quotes, CRLF or LF. */
export function parseCsv(text: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = '';
  let quoted = false;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (quoted) {
      if (ch === '"' && text[i + 1] === '"') {
        cell += '"';
        i++;
      } else if (ch === '"') {
        quoted = false;
      } else {
        cell += ch;
      }
    } else if (ch === '"') {
      quoted = true;
    } else if (ch === ',') {
      row.push(cell);
      cell = '';
    } else if (ch === '\n' || ch === '\r') {
      if (ch === '\r' && text[i + 1] === '\n') i++;
      row.push(cell);
      rows.push(row);
      row = [];
      cell = '';
    } else {
      cell += ch;
    }
  }
  if (cell !== '' || row.length) {
    row.push(cell);
    rows.push(row);
  }
  return rows;
}

export type CsvRow = Record<string, string>;

/** Parses a CSV body into objects keyed by its header row. */
export function csvObjects(text: string): CsvRow[] {
  const [head, ...rest] = parseCsv(text);
  return rest.map((r) => Object.fromEntries(head.map((h, i) => [h, r[i]])));
}

/**
 * The logs export as rows. `query` is the raw query string after `?`. The
 * export carries what the list view does not: project_id, employee_ref,
 * token_id and request_id per row.
 */
export async function exportRows(
  ctx: APIRequestContext,
  ent: EntWorld,
  query: string,
): Promise<CsvRow[]> {
  const res = await ctx.get(entV2(ent, `/logs/export?${query}`));
  const text = await res.text();
  expect(res.status(), `logs export (${query}): ${text.slice(0, 300)}`).toBe(
    200,
  );
  return csvObjects(text);
}

/** Consume rows of the export for one token name, from the whole tenant. */
export async function tenantConsumeRows(
  admin: APIRequestContext,
  ent: EntWorld,
  tokenName?: string,
): Promise<CsvRow[]> {
  const tn = tokenName ? `&token_name=${encodeURIComponent(tokenName)}` : '';
  return exportRows(admin, ent, `scope=tenant&type=2&max_rows=50000${tn}`);
}

export interface StatementRow {
  key: string;
  label: string;
  requests: number;
  quota: number;
  charged_cny4: number;
  priced_cny4: number;
}

export interface Statement {
  month: string;
  group_by: string;
  rows: StatementRow[];
  totals: StatementRow;
}

export async function statement(
  ctx: APIRequestContext,
  ent: EntWorld,
  groupBy: 'project' | 'employee' | 'token',
  month = thisMonth(),
): Promise<Statement> {
  const b = await ok(
    await ctx.get(
      entV2(ent, `/billing/statement?month=${month}&group_by=${groupBy}`),
    ),
    `statement by ${groupBy}`,
  );
  return b.data as Statement;
}

export function rowOf(s: Statement, key: string): StatementRow | undefined {
  return s.rows.find((r) => r.key === key);
}

/** Sums the rows the way the statement promises its totals to be. */
export function sumRows(rows: StatementRow[]) {
  return rows.reduce(
    (a, r) => ({
      requests: a.requests + r.requests,
      quota: a.quota + r.quota,
      charged_cny4: a.charged_cny4 + r.charged_cny4,
      priced_cny4: a.priced_cny4 + r.priced_cny4,
    }),
    { requests: 0, quota: 0, charged_cny4: 0, priced_cny4: 0 },
  );
}

/** Tenant admin's /logs/all, parsed. */
export async function logsAll(
  ctx: APIRequestContext,
  slug: string,
  query: string,
): Promise<any[]> {
  const b = await ok(
    await ctx.get(`/api/v2/${slug}/logs/all?${query}`),
    'logs/all',
  );
  return b.data.logs as any[];
}

/**
 * Like the unlimited credit pool the default tenant has: CREDIT_POOL_REQUIRED
 * =enforce refuses a tenant without one.
 */
export async function ensureTenantCreditPool(
  root: APIRequestContext,
  tenantId: string,
): Promise<void> {
  await ok(
    await root.post(`/api/v2/admin/tenants/${tenantId}/credit-pool`, {
      data: {
        max_balance: -1,
        reset_period: 'monthly',
        alert_threshold_pct: 80,
      },
    }),
    'create credit pool for the enterprise tenant',
    [200, 201],
  );
}

/** A response that must be a refusal, whatever shape the route refuses in. */
export async function refusal(
  res: Awaited<ReturnType<APIRequestContext['get']>>,
  what: string,
) {
  const text = await res.text();
  expect(res.status(), `${what}: ${text.slice(0, 200)}`).toBeLessThan(500);
  let parsed: any = null;
  try {
    parsed = JSON.parse(text);
  } catch {
    // A non-JSON refusal (HTML shell, plain text) is a refusal too, as long
    // as it is not a 2xx carrying data.
  }
  const refused =
    res.status() >= 400 || (parsed && parsed.success === false) || !parsed;
  expect(refused, `${what} was not refused: ${text.slice(0, 300)}`).toBe(true);
  expect(parsed?.data, `${what} leaked data`).toBeUndefined();
}

/** A log row's `other`, whether the API sends it as an object or as JSON text. */
export function parseOther(row: { other?: unknown }): Record<string, any> {
  const o = row.other;
  if (!o) return {};
  if (typeof o === 'string') {
    try {
      return JSON.parse(o);
    } catch {
      return {};
    }
  }
  return o as Record<string, any>;
}
