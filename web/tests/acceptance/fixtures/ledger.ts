import { expect, type APIRequestContext } from '@playwright/test';
import { TENANT_ID, v2 } from './env';
import { platformAccount } from './fake';
import { ok } from './provision';
import { cny4For, walletCNYFor } from './pricebook';

/**
 * Every place a charge is written, read before and after, so a scenario can
 * state exactly where money moved and by how much — and, as importantly,
 * where it did not. "Balance went down" passed through #210 (charged 7.3x too
 * little) and #212 (output at input price); an exact delta on each ledger
 * would not have.
 *
 * Ledgers: the user's local quota and used total, the token's remaining and
 * used, the tenant credit pool, the consume log rows for the token, and the
 * platform wallet (the fake platform's own book).
 */

export interface Snapshot {
  userQuota: number;
  userUsed: number;
  tokenRemain: number;
  tokenUsed: number;
  poolBalance: number;
  poolDraws: number;
  platformBalance: number;
  platformCharged: number;
  logRows: number;
  logQuota: number;
  logCNY4: number;
}

export type Delta = Snapshot;

export interface LedgerScope {
  /** Session of the user who owns the token. */
  user: APIRequestContext;
  /** Tenant admin session, for the pool. */
  admin: APIRequestContext;
  token: { id: number; name: string };
  /** The user's platform account, when the user is linked. */
  platformAccountId?: number;
}

export interface LogRow {
  id: number;
  type: number;
  model_name: string;
  quota: number;
  prompt_tokens: number;
  completion_tokens: number;
  is_stream: boolean;
  charged_cny4?: number;
  token_name: string;
  other?: Record<string, unknown>;
}

const LOG_TYPE_CONSUME = 2;

export async function consumeRows(
  user: APIRequestContext,
  tokenName: string,
): Promise<LogRow[]> {
  const b = await ok(
    await user.get(
      v2(
        `/logs?token_name=${encodeURIComponent(tokenName)}&page=1&page_size=100`,
      ),
    ),
    'list logs',
  );
  return ((b.data?.logs ?? []) as LogRow[]).filter(
    (r) => r.type === LOG_TYPE_CONSUME,
  );
}

export async function snapshot(s: LedgerScope): Promise<Snapshot> {
  const me = await ok(await s.user.get(v2('/user/me')), 'user/me');
  const tokens = await ok(
    await s.user.get(v2('/tokens?p=1&size=100')),
    'list tokens',
  );
  const tok = (tokens.data.items as any[]).find((t) => t.id === s.token.id);
  const pool = await ok(
    await s.admin.get(`/api/v2/admin/tenants/${TENANT_ID}/credit-pool`),
    'credit pool',
  );
  const draws = await ok(
    await s.admin.get(
      `/api/v2/admin/tenants/${TENANT_ID}/credit-pool/usage?limit=1`,
    ),
    'credit pool usage',
  );
  const rows = await consumeRows(s.user, s.token.name);
  let platformBalance = 0;
  let platformCharged = 0;
  if (s.platformAccountId) {
    const p = await platformAccount(s.platformAccountId);
    platformBalance = p.account.balance;
    platformCharged = p.charged;
  }
  return {
    userQuota: me.data.quota,
    userUsed: me.data.used_quota,
    // A deleted token is gone from the list; take both snapshots after the
    // deletion and its 0s compare equal.
    tokenRemain: tok?.remain_quota ?? 0,
    tokenUsed: tok?.used_quota ?? 0,
    poolBalance: pool.data.current_balance,
    poolDraws: draws.data.total,
    platformBalance,
    platformCharged,
    logRows: rows.length,
    logQuota: rows.reduce((a, r) => a + r.quota, 0),
    logCNY4: rows.reduce((a, r) => a + (r.charged_cny4 ?? 0), 0),
  };
}

// `|| 0` folds -0 into 0: toEqual tells them apart.
const yuan = (v: number) => Math.round(v * 1e4) / 1e4 || 0;

export function delta(before: Snapshot, after: Snapshot): Delta {
  const d = {} as Delta;
  for (const k of Object.keys(before) as (keyof Snapshot)[]) {
    d[k] =
      k === 'platformBalance' || k === 'platformCharged'
        ? yuan(after[k] - before[k])
        : after[k] - before[k];
  }
  return d;
}

/**
 * The delta a set of successful calls must leave, one entry per call (the
 * platform books each settle separately, each rounded to 0.0001 yuan).
 * Nothing else may move: the default tenant's pool is unlimited and an
 * unlimited pool is never drawn.
 */
export function charged(
  callQuotas: number[],
  opts: { linked: boolean },
): Delta {
  const q = callQuotas.reduce((a, b) => a + b, 0);
  const cny = opts.linked
    ? yuan(callQuotas.reduce((a, c) => a + walletCNYFor(c), 0))
    : 0;
  return {
    userQuota: 0 - q,
    userUsed: q,
    tokenRemain: 0 - q,
    tokenUsed: q,
    poolBalance: 0,
    poolDraws: 0,
    platformBalance: yuan(-cny),
    platformCharged: cny,
    logRows: callQuotas.length,
    logQuota: q,
    logCNY4: opts.linked ? callQuotas.reduce((a, c) => a + cny4For(c), 0) : 0,
  };
}

/** No ledger moved at all. */
export const NOTHING: Delta = charged([], { linked: false });

/**
 * Waits for the ledgers to settle on exactly `want` (settlement and log
 * writes finish after the response), then holds them there for a moment so
 * a late second write cannot slip past.
 */
export async function expectDelta(
  scope: LedgerScope,
  before: Snapshot,
  want: Delta,
  what: string,
): Promise<Snapshot> {
  await expect
    .poll(async () => delta(before, await snapshot(scope)), {
      message: what,
      timeout: 20_000,
      intervals: [250, 500, 1000],
    })
    .toEqual(want);
  await new Promise((r) => setTimeout(r, 1_500));
  const after = await snapshot(scope);
  expect(delta(before, after), `${what} (after settling)`).toEqual(want);
  return after;
}
