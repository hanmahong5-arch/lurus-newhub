import { mkdirSync, writeFileSync } from 'node:fs';
import { expect, request, type APIRequestContext } from '@playwright/test';
import {
  ACME_ADMIN_STATE,
  ADMIN_STATE,
  CUSTOMER_STATE,
  LEAD_STATE,
  ENV,
  ROOT_USER_ID,
  STATE_DIR,
  TENANT_ID,
  TENANT_SLUG,
  WORLD_FILE,
  v2,
} from './env';
import {
  ensureTenantCreditPool,
  sqlExec,
  type Dept,
  type EntWorld,
} from './enterprise';
import { addPlatformAccount } from './fake';
import {
  allowFakeUpstream,
  chat,
  createInternalKey,
  createToken,
  ensureChannel,
  ensureCreditPool,
  ensureSetup,
  ok,
  systemOne,
  waitRoutable,
} from './provision';

/**
 * Builds the world every scenario runs in, once per run:
 *
 *  - the operator (root) and the instance's production-shaped settings: set
 *    up, credit pool, the SSRF exception for the fake, two channels pointing
 *    at the fake vendor (DeepSeek and hosted System One);
 *  - a customer the way production makes one: a platform account first, then
 *    the platform provisions the newhub user over the internal API and tops
 *    its local quota up in yuan. The customer is linked, so every call it
 *    makes settles against the (fake) platform wallet — the path r6-stage
 *    runs with unified billing on.
 *
 * The enterprise world (TC-E*) is a second, real tenant — roles are refused in
 * the default tenant — with its payer (tenant admin), a department lead, two
 * departments, a trusted gateway key and an ordinary key, and its wallet as
 * the only balance gate (tenants.wallet_authoritative).
 *
 * Sessions come from the e2e bridge (5 logins per minute): root, the default
 * tenant's customer, the enterprise admin and the department lead. The fourth
 * and any rerun inside the same minute wait out a 429 rather than fail.
 */

export interface World {
  runId: string;
  customer: { userId: number; platformAccountId: number; idpSubject: string };
  /** The platform's internal key (balance:write), as platform-core holds one. */
  internalKey: string;
  /** CNY of the setup top-up and the quota it credited. */
  topup: { cny: number; quota: number };
  channels: { deepseek: number; systemOne: number };
  ent: EntWorld;
}

export const SETUP_TOPUP_CNY = 73;
const CHANNEL_TYPE_DEEPSEEK = 43;
const CHANNEL_TYPE_TYPESAFE = 57;

function bridgeURL(userId: number): string {
  return `/api/v2/bridge/exchange?token=${encodeURIComponent(ENV.bridgeToken)}&user_id=${userId}`;
}

async function bridge(
  userId: number,
  statePath: string,
  slug: string = TENANT_SLUG,
): Promise<APIRequestContext> {
  const ctx = await request.newContext({ baseURL: ENV.baseURL });
  // 5 logins per minute: wait out the window instead of failing the run.
  let res = await ctx.post(bridgeURL(userId));
  for (let i = 0; i < 6 && res.status() === 429; i++) {
    await new Promise((r) => setTimeout(r, 15_000));
    res = await ctx.post(bridgeURL(userId));
  }
  const b = await ok(res, `bridge login as user ${userId}`);
  // The SPA reads who is logged in from localStorage; seed it with the
  // cookie so a browser started from this state is already inside.
  const state = await ctx.storageState();
  state.origins = [
    {
      origin: new URL(ENV.baseURL).origin,
      localStorage: [
        { name: 'user', value: JSON.stringify(b.data) },
        { name: 'tenant_slug', value: slug },
      ],
    },
  ];
  writeFileSync(statePath, JSON.stringify(state, null, 2));
  return ctx;
}

export default async function globalSetup(): Promise<void> {
  mkdirSync(STATE_DIR, { recursive: true });
  const runId = Date.now().toString(36);
  // A fresh instance has no root until setup creates it, and setup is the
  // one call that needs no session.
  const anon = await request.newContext({ baseURL: ENV.baseURL });
  await ensureSetup(anon);
  await anon.dispose();
  const admin = await bridge(ROOT_USER_ID, ADMIN_STATE);

  await ensureCreditPool(admin);
  await allowFakeUpstream(admin, ENV.fakeURL);
  const deepseek = await ensureChannel(admin, {
    name: 'accept-deepseek',
    type: CHANNEL_TYPE_DEEPSEEK,
    models: 'deepseek-chat',
    key: ENV.vendorKey || 'unused',
    base_url: ENV.fakeURL,
  });
  const systemOneCh = await ensureChannel(admin, {
    name: 'accept-typesafe',
    type: CHANNEL_TYPE_TYPESAFE,
    models: 'jev-latest',
    key: ENV.vendorKey || 'unused',
    base_url: ENV.fakeURL,
  });

  // TC-E5's second source for the same model: routing has somewhere to go
  // when the first one answers 429.
  const deepseekB = await ensureChannel(admin, {
    name: 'accept-deepseek-b',
    type: CHANNEL_TYPE_DEEPSEEK,
    models: 'deepseek-chat',
    key: ENV.vendorKey || 'unused',
    base_url: ENV.fakeURL,
  });

  // The customer, as the platform creates one.
  const internalKey = await createInternalKey(
    admin,
    `accept-platform-${runId}`,
    ['user:read', 'user:write', 'balance:write'],
  );
  const idpSubject = `accept-${runId}`;
  const platformAccountId = 100_000 + (Date.now() % 1_000_000_000);
  await addPlatformAccount({
    id: platformAccountId,
    idp_subject: idpSubject,
    email: `${idpSubject}@accept.test`,
    balance: 100,
  });
  const internal = await request.newContext({
    baseURL: ENV.baseURL,
    extraHTTPHeaders: { 'X-API-Key': internalKey },
  });
  const prov = await ok(
    await internal.post('/internal/user/provision', {
      data: {
        idp_subject: idpSubject,
        email: `${idpSubject}@accept.test`,
        tenant_id: TENANT_ID,
      },
    }),
    'provision the customer',
    [200, 201],
  );
  const userId: number = prov.data.user_id;
  const top = await ok(
    await internal.post('/internal/balance/topup', {
      data: {
        user_id: userId,
        amount_rmb: SETUP_TOPUP_CNY,
        order_id: `accept-setup-${runId}`,
        reason: 'acceptance setup',
      },
    }),
    'top the customer up',
  );
  const customer = await bridge(userId, CUSTOMER_STATE);

  // Routable only after the channel cache syncs; wait with the operator's
  // own token so the customer's ledgers start untouched.
  const probe = await createToken(admin, {
    name: `accept-route-${runId}`,
    unlimited_quota: true,
  });
  await waitRoutable(admin, probe.key, [
    { model: 'deepseek-chat', call: chat },
    { model: 'jev-latest', call: systemOne },
  ]);

  await waitBothChannelsServe(admin, probe, [deepseek, deepseekB]);

  const ent = await buildEnterprise(admin, internal, runId, deepseekB);

  const world: World = {
    runId,
    customer: { userId, platformAccountId, idpSubject },
    internalKey,
    topup: { cny: SETUP_TOPUP_CNY, quota: top.data.amount },
    channels: { deepseek, systemOne: systemOneCh },
    ent,
  };
  writeFileSync(WORLD_FILE, JSON.stringify(world, null, 2));
  await Promise.all([admin.dispose(), customer.dispose(), internal.dispose()]);
}

/**
 * Both deepseek channels enter the channel cache on its next sync, maybe not
 * the same one. Calls with the operator's probe token until the operator's
 * log (which names the serving channel) has seen each.
 */
async function waitBothChannelsServe(
  admin: APIRequestContext,
  probe: { key: string; name: string },
  want: number[],
  timeoutMs = 130_000,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    for (let i = 0; i < 6; i++) await chat(admin, probe.key, 'deepseek-chat');
    await new Promise((r) => setTimeout(r, 1_500));
    const logs = await ok(
      await admin.get(
        v2(
          `/logs/all?token_name=${encodeURIComponent(probe.name)}&model_name=deepseek-chat&page_size=100`,
        ),
      ),
      'probe logs',
    );
    const seen = new Set<number>(
      (logs.data.logs as any[]).map((l) => l.channel),
    );
    if (want.every((id) => seen.has(id))) return;
    if (Date.now() > deadline) {
      throw new Error(
        `channels ${want.join(', ')} never both served deepseek-chat; seen ${[...seen].join(', ')}`,
      );
    }
    await new Promise((r) => setTimeout(r, 3_000));
  }
}

/**
 * The enterprise tenant ("acme"), built through the product's own API except
 * for the first admin's role (see sqlExec).
 */
async function buildEnterprise(
  root: APIRequestContext,
  internal: APIRequestContext,
  runId: string,
  secondDeepseekChannel: number,
): Promise<EntWorld> {
  const slug = `acme-${runId}`;
  const made = await ok(
    await root.post('/api/v2/admin/tenants', {
      data: {
        zitadel_org_id: `accept-org-${runId}`,
        slug,
        name: `Acme ${runId}`,
        max_users: 50,
      },
    }),
    'create the enterprise tenant',
    201,
  );
  const tenantId: string = made.data.id;
  expect(made.data.slug).toBe(slug);
  const flag = await ok(
    await root.put(`/api/v2/admin/tenants/${tenantId}`, {
      data: { wallet_authoritative: true },
    }),
    'make the wallet authoritative',
  );
  expect(flag.data.wallet_authoritative).toBe(true);
  await ensureTenantCreditPool(root, tenantId);

  // The payer: the company's one Lugo account. Its local quota stays 0 on
  // purpose — only the wallet may let a request through.
  const idpSubject = `accept-acme-${runId}`;
  const platformAccountId = 200_000 + (Date.now() % 1_000_000_000);
  await addPlatformAccount({
    id: platformAccountId,
    idp_subject: idpSubject,
    email: `${idpSubject}@accept.test`,
    balance: 100,
  });
  const provision = async (subject: string) => {
    const r = await ok(
      await internal.post('/internal/user/provision', {
        data: {
          idp_subject: subject,
          email: `${subject}@accept.test`,
          tenant_id: tenantId,
        },
      }),
      `provision ${subject}`,
      [200, 201],
    );
    return r.data.user_id as number;
  };
  const adminUserId = await provision(idpSubject);
  const leadUserId = await provision(`accept-lead-${runId}`);

  // Stand-in for invite redemption, before the user's first request so no
  // cached copy of the row can predate it.
  sqlExec(
    `UPDATE users SET tenant_role = 'admin' WHERE id = ${adminUserId} AND tenant_id = '${tenantId}'`,
  );
  expect(
    sqlExec(`SELECT tenant_role FROM users WHERE id = ${adminUserId}`),
  ).toBe('admin');
  // The payer (migration 048): an admin-issued key (employee_ref) is booked
  // to the tenant's payer and the API answers 409 payer_not_set without one.
  // Designating the payer is a root bootstrap step in production; here it is
  // the same stand-in as the role above.
  sqlExec(
    `UPDATE tenants SET payer_user_id = ${adminUserId} WHERE id = '${tenantId}'`,
  );
  expect(
    sqlExec(`SELECT payer_user_id FROM tenants WHERE id = '${tenantId}'`),
  ).toBe(String(adminUserId));

  const admin = await bridge(adminUserId, ACME_ADMIN_STATE, slug);
  // TC-E4 premise: the customer's admin is a plain user globally (role 1).
  const meAdmin = await ok(
    await admin.get(`/api/v2/${slug}/user/me`),
    'enterprise admin user/me',
  );
  expect(meAdmin.data.role, 'global role of the customer admin').toBe(1);
  const entPath = (p: string) => `/api/v2/${slug}${p}`;

  const dept = async (label: string): Promise<Dept> => {
    const name = `acc-dept-${label}-${runId}`;
    const code = `d${label}-${runId}`;
    const b = await ok(
      await admin.post(entPath('/projects'), {
        data: { name, external_code: code },
      }),
      `create department ${label}`,
      201,
    );
    expect(b.data.external_code).toBe(code);
    return { id: b.data.id, name, code };
  };
  const a = await dept('a');
  const b = await dept('b');

  // The lead leads department A only; membership makes the role.
  await ok(
    await admin.post(entPath(`/projects/${a.id}/members`), {
      data: { user_id: leadUserId },
    }),
    'make the lead a member of department A',
  );
  const members = await ok(
    await admin.get(entPath(`/projects/${a.id}/members`)),
    'list department A members',
  );
  expect(
    (members.data.items as any[]).map((m) => [m.user_id, m.tenant_role]),
  ).toEqual([[leadUserId, 'dept_lead']]);
  const lead = await bridge(leadUserId, LEAD_STATE, slug);

  const trusted = await createTokenIn(admin, slug, {
    name: `acc-gateway-${runId}`,
    unlimited_quota: true,
    trusted_identity_headers: true,
  });
  const employeeRef = `plain-${runId}`;
  const plain = await createTokenIn(admin, slug, {
    name: `acc-plain-${runId}`,
    unlimited_quota: true,
    employee_ref: employeeRef,
  });

  await Promise.all([admin.dispose(), lead.dispose()]);

  return {
    tenant: { id: tenantId, slug },
    admin: { userId: adminUserId, platformAccountId, globalRole: 1 },
    lead: { userId: leadUserId },
    depts: { a, b },
    keys: { trusted, plain: { ...plain, employeeRef } },
    secondDeepseekChannel,
  };
}

async function createTokenIn(
  ctx: APIRequestContext,
  slug: string,
  fields: Record<string, unknown> & { name: string },
): Promise<{ id: number; key: string; name: string }> {
  const b = await ok(
    await ctx.post(`/api/v2/${slug}/tokens`, {
      data: { expired_time: -1, ...fields },
    }),
    `create token ${fields.name}`,
    201,
  );
  expect(b.data.key).toMatch(/^sk-/);
  return { id: b.data.id, key: b.data.key, name: fields.name };
}
