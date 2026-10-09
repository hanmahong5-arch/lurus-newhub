import {
  expect,
  type APIRequestContext,
  type APIResponse,
} from '@playwright/test';
import { TENANT_ID, v2 } from './env';

/**
 * Everything a scenario needs to exist on the instance, created through the
 * same API an operator or the platform uses — no SQL, no seeding behind the
 * product's back. Each helper is safe to call against a database that already
 * has the objects (a kept ACCEPT_UP_ONLY stack, a rerun).
 */

/** Parses a JSON body, failing with the status and raw body when it is not the expected one. */
export async function body<T = any>(
  res: APIResponse,
  what: string,
  status: number | number[] = 200,
): Promise<T> {
  const text = await res.text();
  const want = Array.isArray(status) ? status : [status];
  expect(want, `${what}: HTTP ${res.status()} ${text.slice(0, 600)}`).toContain(
    res.status(),
  );
  try {
    return JSON.parse(text) as T;
  } catch {
    throw new Error(`${what}: body is not JSON: ${text.slice(0, 300)}`);
  }
}

/** Like body(), and also requires `success: true`. */
export async function ok<T = any>(
  res: APIResponse,
  what: string,
  status: number | number[] = 200,
): Promise<T> {
  const b = await body<any>(res, what, status);
  expect(b?.success, `${what}: ${JSON.stringify(b).slice(0, 600)}`).toBe(true);
  return b as T;
}

/**
 * A fresh instance has no root user and sends everyone to /setup until this
 * runs. Unauthenticated, like the console's own setup page.
 */
export async function ensureSetup(anon: APIRequestContext): Promise<void> {
  const s = await body(await anon.get('/api/setup'), 'GET /api/setup');
  if (s?.data?.status) return;
  // Production's values: not self-use (unpriced models are refused, not
  // free), not a demo site.
  await ok(
    await anon.post('/api/setup', {
      data: {
        username: 'root',
        SelfUseModeEnabled: false,
        DemoSiteEnabled: false,
      },
    }),
    'POST /api/setup',
  );
}

export async function setOption(
  admin: APIRequestContext,
  key: string,
  value: string,
): Promise<void> {
  await ok(
    await admin.put('/api/option/', { data: { key, value } }),
    `PUT /api/option/ ${key}`,
  );
}

export async function getOption(
  admin: APIRequestContext,
  key: string,
): Promise<string | undefined> {
  const b = await ok(await admin.get('/api/option/'), 'GET /api/option/');
  return (b.data as { key: string; value: string }[]).find((o) => o.key === key)
    ?.value;
}

/**
 * The relay refuses private upstreams by default (SSRF guard). Let it reach
 * exactly the fake on loopback and nothing else private: the narrowest
 * exception the fetch settings can express.
 */
export async function allowFakeUpstream(
  admin: APIRequestContext,
  fakeURL: string,
): Promise<void> {
  const port = new URL(fakeURL).port;
  await setOption(admin, 'fetch_setting.allow_private_ip', 'true');
  await setOption(admin, 'fetch_setting.ip_filter_mode', 'true');
  await setOption(
    admin,
    'fetch_setting.ip_list',
    JSON.stringify(['127.0.0.1/32']),
  );
  await setOption(
    admin,
    'fetch_setting.allowed_ports',
    JSON.stringify(['80', '443', '8080', '8443', port]),
  );
}

/**
 * CREDIT_POOL_REQUIRED=enforce (as on r6-stage) refuses a tenant without a
 * pool. Production's default tenant has an unlimited one; so does this.
 */
export async function ensureCreditPool(
  admin: APIRequestContext,
): Promise<void> {
  const got = await admin.get(`/api/v2/admin/tenants/${TENANT_ID}/credit-pool`);
  if (got.status() === 200 && (await got.json())?.data?.id) return;
  await ok(
    await admin.post(`/api/v2/admin/tenants/${TENANT_ID}/credit-pool`, {
      data: {
        max_balance: -1,
        reset_period: 'monthly',
        alert_threshold_pct: 80,
      },
    }),
    'create credit pool',
    [200, 201],
  );
}

export interface ChannelSpec {
  name: string;
  type: number;
  models: string;
  key: string;
  base_url: string;
}

/** Creates the channel, or points the existing one of that name at the current fake. */
export async function ensureChannel(
  admin: APIRequestContext,
  spec: ChannelSpec,
): Promise<number> {
  const found = await ok(
    await admin.get(
      `/api/channel/search?keyword=${encodeURIComponent(spec.name)}&p=1&page_size=50`,
    ),
    'search channels',
  );
  const items: any[] = Array.isArray(found.data)
    ? found.data
    : (found.data?.items ?? []);
  const existing = items.find((c) => c.name === spec.name);
  const fields = {
    ...spec,
    group: 'default',
    status: 1,
    priority: 0,
    weight: 1,
  };
  if (existing) {
    await ok(
      await admin.put('/api/channel/', {
        data: { id: existing.id, ...fields },
      }),
      `update channel ${spec.name}`,
    );
    return existing.id;
  }
  await ok(
    await admin.post('/api/channel/', {
      data: { mode: 'single', channel: fields },
    }),
    `create channel ${spec.name}`,
  );
  return ensureChannel(admin, spec);
}

export interface CreatedToken {
  id: number;
  key: string;
  name: string;
}

export async function createToken(
  ctx: APIRequestContext,
  fields: Record<string, unknown> & { name: string },
): Promise<CreatedToken> {
  const b = await ok(
    await ctx.post(v2('/tokens'), { data: { expired_time: -1, ...fields } }),
    `create token ${fields.name}`,
    201,
  );
  expect(b.data.key).toMatch(/^sk-/);
  return { id: b.data.id, key: b.data.key, name: fields.name };
}

/** Mints one redemption code worth `quota` units (tenant admin). */
export async function mintCode(
  admin: APIRequestContext,
  name: string,
  quota: number,
): Promise<{ id: number; key: string }> {
  const b = await ok(
    await admin.post(v2('/redemptions'), { data: { name, count: 1, quota } }),
    'mint redemption code',
    [200, 201],
  );
  const { id, key } = b.data.codes[0];
  expect(key).toHaveLength(32);
  return { id, key };
}

export async function createInternalKey(
  admin: APIRequestContext,
  name: string,
  scopes: string[],
): Promise<string> {
  const b = await ok(
    await admin.post('/api/api-keys/', { data: { name, scopes } }),
    'create internal API key',
    [200, 201],
  );
  const key: string = b.data.key;
  expect(key).toMatch(/^lurus_ik_/);
  return key;
}

/** One relay call in the OpenAI chat wire. */
export function chat(
  request: APIRequestContext,
  key: string,
  model: string,
  extra: Record<string, unknown> = {},
): Promise<APIResponse> {
  return request.post('/v1/chat/completions', {
    headers: { Authorization: `Bearer ${key}` },
    data: { model, messages: [{ role: 'user', content: 'hi' }], ...extra },
  });
}

/** One relay call in the Anthropic messages wire. */
export function messages(
  request: APIRequestContext,
  key: string,
  model: string,
): Promise<APIResponse> {
  return request.post('/v1/messages', {
    headers: { 'x-api-key': key, 'anthropic-version': '2023-06-01' },
    data: { model, max_tokens: 5, messages: [{ role: 'user', content: 'hi' }] },
  });
}

/** One relay call in the System One wire. */
export function systemOne(
  request: APIRequestContext,
  key: string,
  model: string,
): Promise<APIResponse> {
  return request.post('/v1/systemone', {
    headers: { Authorization: `Bearer ${key}` },
    data: {
      model,
      state: 'The customer asked for a refund twice.',
      questions: {
        escalate: { type: 'noul', instructions: 'Should this be escalated?' },
      },
    },
  });
}

/**
 * New channels become routable when the channel cache next syncs
 * (SYNC_FREQUENCY, 60s on r6-stage). Waits until each model answers 200.
 */
export async function waitRoutable(
  request: APIRequestContext,
  key: string,
  calls: { model: string; call: typeof chat }[],
  timeoutMs = 100_000,
): Promise<void> {
  for (const { model, call } of calls) {
    const deadline = Date.now() + timeoutMs;
    let last = '';
    for (;;) {
      const res = await call(request, key, model);
      if (res.status() === 200) break;
      last = `${res.status()} ${(await res.text()).slice(0, 300)}`;
      if (Date.now() > deadline) {
        throw new Error(`${model} never became routable: last answer ${last}`);
      }
      await new Promise((r) => setTimeout(r, 3_000));
    }
  }
}

/** Redeems a code into the session user's wallet. */
export function redeem(
  ctx: APIRequestContext,
  key: string,
): Promise<APIResponse> {
  return ctx.post(v2('/redeem'), { data: { key } });
}
