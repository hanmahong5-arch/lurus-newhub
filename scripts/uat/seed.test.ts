import { describe, expect, test } from 'bun:test';
import { mkdtempSync, readFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  CODE_COUNT,
  TENANTS,
  TESTERS,
  maskKey,
  parseArgs,
  parseCsv,
  planLines,
  renderAccountsCsv,
  runSeed,
  writeOutputs,
  type Deps,
  type Options,
} from './seed';

/**
 * A small stateful stand-in for the management API. It implements only what
 * the seed calls, with the real status codes, so the test proves the request
 * orchestration (order, idempotency, secrets handling), not the server.
 */
class FakeHub {
  calls: string[] = [];
  nextId = 100;
  tenants: any[] = [];
  pools = new Set<string>();
  users = new Map<string, any>(); // key: tenant|subject
  roles = new Map<string, any>();
  projects = new Map<string, any[]>(); // slug -> projects
  tokens = new Map<string, any[]>(); // slug -> tokens
  members = new Set<string>();
  channels: any[] = [];
  priced = new Set<string>();
  codes: any[] = [];
  grants: any[] = [];
  bridgeHits: number[] = [];
  bridgeRate429 = 0;
  relayStatus = 200;
  options: Record<string, string> = {};

  count(re: RegExp) {
    return this.calls.filter((c) => re.test(c)).length;
  }

  private json(status: number, body: unknown, headers: Record<string, string> = {}) {
    const h = new Headers({ 'content-type': 'application/json', 'x-request-id': 'req-test', ...headers });
    return new Response(JSON.stringify(body), { status, headers: h });
  }

  handle = async (url: string, init: RequestInit = {}): Promise<Response> => {
    const u = new URL(url);
    const method = (init.method || 'GET').toUpperCase();
    const path = u.pathname;
    const body = init.body ? JSON.parse(String(init.body)) : undefined;
    const hdr = new Headers(init.headers as any);
    this.calls.push(`${method} ${path}`);
    let m: RegExpMatchArray | null;

    if (path === '/api/v2/bridge/exchange') {
      this.bridgeHits.push(Number(u.searchParams.get('user_id')));
      if (u.searchParams.get('token') !== 'bridge-secret') return this.json(403, { success: false });
      if (this.bridgeRate429 > 0) {
        this.bridgeRate429--;
        return this.json(429, { success: false }, { 'retry-after': '1' });
      }
      const res = this.json(200, { success: true, data: { id: 1, username: 'u' } });
      res.headers.append('set-cookie', 'session=abc; Path=/; HttpOnly');
      return res;
    }
    if (path.startsWith('/internal/') && hdr.get('x-api-key') !== 'lurus_ik_test') {
      return this.json(401, { success: false, message: 'API key required' });
    }
    if (!path.startsWith('/internal/') && !path.startsWith('/v1/') && !(hdr.get('cookie') || '').includes('session=abc')) {
      return this.json(401, { success: false, message: 'no session' });
    }

    if (path === '/internal/user/provision') {
      const key = `${body.tenant_id}|${body.idp_subject}`;
      const ex = this.users.get(key);
      if (ex) return this.json(200, { success: true, data: { user_id: ex.id, is_existing: true } });
      const id = this.nextId++;
      this.users.set(key, { id, ...body });
      const data: any = { user_id: id, is_existing: false };
      if (body.create_initial_token) data.token = { id: this.nextId++, key: `sk-fake${id}abcdefgh${id}`, name: body.initial_token_name };
      return this.json(201, { success: true, data });
    }
    if (method === 'GET' && (m = path.match(/^\/api\/v2\/admin\/tenants\/([^/]+)\/credit-pool$/))) {
      return this.pools.has(m[1]) ? this.json(200, { success: true, data: { id: 1 } }) : this.json(404, { success: false });
    }
    if (method === 'POST' && (m = path.match(/^\/api\/v2\/admin\/tenants\/([^/]+)\/credit-pool$/))) {
      this.pools.add(m[1]);
      return this.json(201, { success: true, data: { id: 1 } });
    }
    if (method === 'GET' && path === '/api/v2/admin/tenants') return this.json(200, { success: true, data: { tenants: this.tenants } });
    if (method === 'POST' && path === '/api/v2/admin/tenants') {
      const t = { id: `tid-${body.slug}`, slug: body.slug };
      this.tenants.push(t);
      this.projects.set(body.slug, []);
      this.tokens.set(body.slug, []);
      return this.json(201, { success: true, data: t });
    }
    if (method === 'PUT' && (m = path.match(/^\/api\/v2\/admin\/tenants\/([^/]+)\/members\/(\d+)\/role$/))) {
      this.roles.set(`${m[1]}|${m[2]}`, body);
      return this.json(200, { success: true });
    }
    if (method === 'PUT' && (m = path.match(/^\/api\/v2\/admin\/users\/(\d+)$/))) {
      this.roles.set(`user|${m[1]}`, body);
      return this.json(200, { success: true });
    }
    if (method === 'GET' && path === '/api/v2/admin/authz/grants') return this.json(200, { success: true, data: this.grants });
    if (method === 'POST' && path === '/api/v2/admin/authz/grants') {
      this.grants.push(body);
      return this.json(201, { success: true });
    }
    if (method === 'PUT' && path === '/api/option/') {
      this.options[body.key] = body.value;
      return this.json(200, { success: true });
    }
    if (method === 'GET' && path === '/api/channel/search') {
      const kw = u.searchParams.get('keyword');
      return this.json(200, { success: true, data: { items: this.channels.filter((c) => c.name === kw) } });
    }
    if (method === 'POST' && path === '/api/channel/') {
      this.channels.push({ id: this.nextId++, status: 1, ...body.channel });
      return this.json(200, { success: true });
    }
    if (method === 'PUT' && path === '/api/channel/') {
      Object.assign(this.channels.find((c) => c.id === body.id), body);
      return this.json(200, { success: true });
    }
    if (method === 'GET' && (m = path.match(/^\/api\/v2\/([^/]+)\/pricing$/))) {
      return this.json(200, {
        success: true,
        data: { version: 7, pricing: [...this.priced].map((n) => ({ model_name: n, model_ratio: 1 })) },
      });
    }
    if (method === 'POST' && (m = path.match(/^\/api\/v2\/([^/]+)\/pricing$/))) {
      if (hdr.get('if-match-pricing-version') !== '7') return this.json(409, { success: false });
      for (const it of body) this.priced.add(it.model_name);
      return this.json(200, { success: true });
    }
    if (method === 'GET' && (m = path.match(/^\/api\/v2\/([^/]+)\/projects$/))) {
      return this.json(200, { success: true, data: { items: this.projects.get(m[1]) ?? [] } });
    }
    if (method === 'POST' && (m = path.match(/^\/api\/v2\/([^/]+)\/projects$/))) {
      const p = { id: this.nextId++, name: body.name, external_code: body.external_code };
      this.projects.get(m[1])!.push(p);
      return this.json(201, { success: true, data: p });
    }
    if (method === 'POST' && (m = path.match(/^\/api\/v2\/([^/]+)\/projects\/(\d+)\/members$/))) {
      this.members.add(`${m[1]}|${m[2]}|${body.user_id}`);
      return this.json(200, { success: true });
    }
    if (method === 'GET' && (m = path.match(/^\/api\/v2\/([^/]+)\/tokens$/))) {
      const list = (this.tokens.get(m[1]) ?? []).map((t) => ({ ...t, key: `${t.key.slice(0, 4)}****${t.key.slice(-4)}` }));
      const page = Number(u.searchParams.get('p') || 1);
      return this.json(200, { success: true, data: { items: page === 1 ? list : [] } });
    }
    if (method === 'POST' && (m = path.match(/^\/api\/v2\/([^/]+)\/tokens$/))) {
      const t = { id: this.nextId++, name: body.name, key: `sk-${body.name}-0123456789`, ...body };
      this.tokens.get(m[1])?.push(t);
      if (!this.tokens.has(m[1])) this.tokens.set(m[1], [t]);
      return this.json(201, { success: true, data: t });
    }
    if (method === 'POST' && (m = path.match(/^\/api\/v2\/([^/]+)\/tokens\/(\d+)\/rotate$/))) {
      return this.json(200, { success: true, data: { key: 'sk-rotated-0123456789' } });
    }
    if (method === 'POST' && (m = path.match(/^\/api\/v2\/([^/]+)\/tokens\/batch$/))) {
      const list = this.tokens.get(m[1])!;
      const created: any[] = [];
      const skipped: any[] = [];
      for (const it of body.tokens) {
        if (list.some((t) => t.employee_ref === it.employee_ref)) {
          skipped.push({ employee_ref: it.employee_ref });
          continue;
        }
        const t = { id: this.nextId++, name: it.name, employee_ref: it.employee_ref, project_id: it.project_id, key: `sk-${it.employee_ref}-9876543210` };
        list.push(t);
        created.push(t);
      }
      return this.json(created.length ? 201 : 200, { success: true, data: { created, skipped } });
    }
    if (method === 'GET' && (m = path.match(/^\/api\/v2\/([^/]+)\/redemptions$/))) {
      return this.json(200, { success: true, data: { redemptions: this.codes } });
    }
    if (method === 'POST' && (m = path.match(/^\/api\/v2\/([^/]+)\/redemptions$/))) {
      const made = [];
      for (let i = 0; i < body.count; i++) {
        const c = { id: this.nextId++, key: `CODE${this.nextId}`.padEnd(32, 'x'), name: body.name };
        this.codes.push({ id: c.id, name: body.name });
        made.push(c);
      }
      return this.json(201, { success: true, data: { codes: made } });
    }
    if (path === '/v1/chat/completions') {
      return this.json(this.relayStatus, this.relayStatus === 200 ? { id: 'x' } : { error: { message: 'boom' } });
    }
    return this.json(404, { success: false, message: `fake: no route ${method} ${path}` });
  };
}

function opts(over: Partial<Options> = {}): Options {
  return {
    baseUrl: 'https://uat.example.test',
    bridgeToken: 'bridge-secret',
    internalKey: 'lurus_ik_test',
    mintInternalKey: false,
    faultsimToken: '',
    upstreamKey: '',
    upstreamBaseUrl: '',
    upstreamType: 1,
    demoModel: 'uat-demo-chat',
    upstreamModel: '',
    modelRatio: 0.15,
    completionRatio: 4,
    allowPrivateUpstream: false,
    skipProbe: false,
    probeTimeoutMs: 50,
    out: mkdtempSync(join(tmpdir(), 'seedtest-')),
    dryRun: false,
    ...over,
  };
}

function depsFor(hub: FakeHub): Deps {
  return { fetch: hub.handle, sleep: async () => {}, log: () => {} };
}

const FULL = {
  faultsimToken: 'fs-secret',
  upstreamBaseUrl: 'https://upstream.example.test',
  upstreamKey: 'up-secret',
};

describe('seed orchestration', () => {
  test('builds the whole world on a blank instance', async () => {
    const hub = new FakeHub();
    const o = opts(FULL);
    const rep = await runSeed(o, depsFor(hub));

    expect(hub.tenants.map((t) => t.slug)).toEqual(TENANTS.map((t) => t.slug));
    for (const t of TENANTS) {
      expect(hub.projects.get(t.slug)!.map((p) => p.external_code)).toEqual(t.depts.map((d) => d.code));
      // 3 employee keys + gateway + plain
      expect(hub.tokens.get(t.slug)).toHaveLength(t.employees.length + 2);
      const gateway = hub.tokens.get(t.slug)!.find((x) => x.name === `${t.code}-gateway`);
      expect(gateway.trusted_identity_headers).toBe(true);
      expect(hub.pools.has(`tid-${t.slug}`)).toBe(true);
    }
    // payer admin per tenant
    const adminRoles = [...hub.roles].filter(([k, v]) => k.startsWith('tid-') && v.tenant_role === 'admin' && v.payer === true);
    expect(adminRoles).toHaveLength(TENANTS.length);
    // operator is root-level, observer is admin-level with the audit grant
    expect([...hub.roles.values()].some((v) => v.role === 100)).toBe(true);
    expect(hub.grants).toEqual([{ user_id: expect.any(Number), resource: 'audit', action: 'read', tenant_id: null }]);
    expect(hub.codes).toHaveLength(CODE_COUNT);
    expect(hub.pools.has('default')).toBe(true);
    // testers: one user + one key each, the small one carries quota 1
    const testerUsers = [...hub.users.values()].filter((u) => /^uat-t0\d$/.test(u.idp_subject));
    expect(testerUsers).toHaveLength(TESTERS.length);
    expect(testerUsers.find((u) => u.idp_subject === 'uat-t06').initial_quota).toBe(1);
    // channels and prices
    expect(hub.channels.map((c) => c.name).sort()).toEqual(['uat-demo-faulty', 'uat-demo-upstream', 'uat-faultsim']);
    const faulty = hub.channels.find((c) => c.name === 'uat-demo-faulty');
    expect(faulty.status).toBe(2); // disabled until the engineer demo
    expect(faulty.priority).toBe(10);
    expect(JSON.parse(faulty.model_mapping)).toEqual({ 'uat-demo-chat': 'http_500' });
    expect(hub.channels.find((c) => c.name === 'uat-faultsim').base_url).toBe('https://uat.example.test/api/v2/faultsim');
    expect(hub.priced.has('uat-demo-chat')).toBe(true);
    expect(hub.priced.has('http_500')).toBe(true);
    // 1 root + 2 admins; stays under the 5/min bridge limit
    expect(hub.bridgeHits).toHaveLength(1 + TENANTS.length);
    expect(rep.steps.every((s) => s.status !== 'failed')).toBe(true);
  });

  test('second run creates nothing', async () => {
    const hub = new FakeHub();
    const o = opts(FULL);
    const first = await runSeed(o, depsFor(hub));
    writeOutputs(o, first);

    const before = {
      tenants: hub.tenants.length,
      channels: hub.channels.length,
      projects: [...hub.projects.values()].flat().length,
      tokens: [...hub.tokens.values()].flat().length,
      codes: hub.codes.length,
      users: hub.users.size,
      grants: hub.grants.length,
    };
    const callsBefore = hub.calls.length;
    const second = await runSeed(o, depsFor(hub));

    expect({
      tenants: hub.tenants.length,
      channels: hub.channels.length,
      projects: [...hub.projects.values()].flat().length,
      tokens: [...hub.tokens.values()].flat().length,
      codes: hub.codes.length,
      users: hub.users.size,
      grants: hub.grants.length,
    }).toEqual(before);
    expect(second.created).toEqual({});
    // and the second run issued no creating call that returned something new
    const secondCalls = hub.calls.slice(callsBefore);
    expect(secondCalls.filter((c) => c === 'POST /api/v2/admin/tenants')).toHaveLength(0);
    expect(secondCalls.filter((c) => c === 'POST /api/channel/')).toHaveLength(0);
    expect(secondCalls.filter((c) => /\/projects$/.test(c) && c.startsWith('POST'))).toHaveLength(0);
    expect(secondCalls.filter((c) => /\/redemptions$/.test(c) && c.startsWith('POST'))).toHaveLength(0);
    expect(secondCalls.filter((c) => c === 'POST /api/v2/admin/authz/grants')).toHaveLength(0);
    expect(secondCalls.filter((c) => /pricing$/.test(c) && c.startsWith('POST'))).toHaveLength(0);
  });

  test('plaintext keys survive a rerun through secrets.csv', async () => {
    const hub = new FakeHub();
    const o = opts(FULL);
    const first = await runSeed(o, depsFor(hub));
    writeOutputs(o, first);
    const second = await runSeed(o, depsFor(hub));
    writeOutputs(o, second);
    const rows = parseCsv(readFileSync(join(o.out, 'secrets.csv'), 'utf8'));
    const labels = rows.slice(1).map((r) => r[0]);
    expect(labels).toContain('uat-key-t06');
    expect(labels).toContain('dt-gateway');
    expect(labels).toContain('mf-e03');
    expect(labels.filter((l) => l.startsWith('uat-code-'))).toHaveLength(CODE_COUNT);
    expect(new Set(labels).size).toBe(labels.length);
    // accounts show masks from the second run, never the plaintext
    const acc = renderAccountsCsv(second.accounts);
    expect(acc).not.toContain('sk-fake');
    expect(acc).toContain(maskKey('sk-uat-key-t01'.padEnd(20, 'x')).slice(0, 3));
  });

  test('accounts files never contain a plaintext key, bridge token or internal key', async () => {
    const hub = new FakeHub();
    const o = opts(FULL);
    const rep = await runSeed(o, depsFor(hub));
    const files = writeOutputs(o, rep);
    for (const f of files.slice(0, 2)) {
      const text = readFileSync(f, 'utf8');
      for (const s of rep.secrets) expect(text.includes(s.value)).toBe(false);
      expect(text).not.toContain('bridge-secret');
      expect(text).not.toContain('lurus_ik_test');
      expect(text).not.toContain('fs-secret');
    }
    expect(readFileSync(files[2], 'utf8')).toContain('uat-key-t06');
  });

  test('without FAULTSIM_TOKEN and an upstream the channel steps are skipped with a manual alternative, not faked', async () => {
    const hub = new FakeHub();
    const rep = await runSeed(opts(), depsFor(hub));
    expect(hub.channels).toHaveLength(0);
    const skipped = rep.steps.filter((s) => s.status === 'skipped');
    expect(skipped.map((s) => s.step).sort()).toEqual(['channel-demo-upstream', 'channel-faultsim']);
    for (const s of skipped) expect(s.manual && s.manual.length > 20).toBe(true);
    expect(hub.count(/\/pricing$/)).toBe(0);
  });

  test('FAULTSIM_TOKEN alone maps the demo model onto the simulator success model', async () => {
    const hub = new FakeHub();
    const rep = await runSeed(opts({ faultsimToken: 'fs-secret' }), depsFor(hub));
    const demo = hub.channels.find((c) => c.name === 'uat-demo-upstream');
    expect(demo.base_url).toBe('https://uat.example.test/api/v2/faultsim');
    expect(demo.key).toBe('fs-secret');
    expect(JSON.parse(demo.model_mapping)).toEqual({ 'uat-demo-chat': 'ok-chat' });
    expect(hub.priced.has('uat-demo-chat')).toBe(true);
    expect(rep.steps.find((s) => s.step === 'channel-demo-upstream')!.status).not.toBe('skipped');
  });

  test('bridge 429 is waited out, not failed', async () => {
    const hub = new FakeHub();
    hub.bridgeRate429 = 2;
    await runSeed(opts(FULL), depsFor(hub));
    expect(hub.bridgeHits.length).toBeGreaterThanOrEqual(1 + TENANTS.length + 2);
  });

  test('a wrong bridge token stops at the first step with the request id', async () => {
    const hub = new FakeHub();
    await expect(runSeed(opts({ ...FULL, bridgeToken: 'nope' }), depsFor(hub))).rejects.toThrow(/403.*req-test/s);
    expect(hub.calls.filter((c) => c.startsWith('POST /internal'))).toHaveLength(0);
  });

  test('missing internal key without --mint-internal-key is a clear error before any write', async () => {
    const hub = new FakeHub();
    await expect(runSeed(opts({ ...FULL, internalKey: '' }), depsFor(hub))).rejects.toThrow(/UAT_INTERNAL_KEY/);
    expect(hub.calls.filter((c) => c.startsWith('POST') && !c.includes('bridge'))).toHaveLength(0);
  });

  test('route probe failing is reported, not hidden', async () => {
    const hub = new FakeHub();
    hub.relayStatus = 503;
    await expect(runSeed(opts(FULL), depsFor(hub))).rejects.toThrow(/route-probe/);
  });
});

describe('cli', () => {
  test('secrets on the command line are refused', () => {
    expect(() => parseArgs(['--bridge-token', 'x'], {})).toThrow(/E2E_BRIDGE_TOKEN/);
    expect(() => parseArgs(['--internal-key=lurus_ik_x'], { E2E_BRIDGE_TOKEN: 't' })).toThrow(/UAT_INTERNAL_KEY/);
  });

  test('secrets come from the environment; dry-run needs none', () => {
    const o = parseArgs(['--base-url', 'http://127.0.0.1:9999/', '--out', '/x'], {
      E2E_BRIDGE_TOKEN: 'tok',
      UAT_INTERNAL_KEY: 'lurus_ik_a',
    });
    expect(o.baseUrl).toBe('http://127.0.0.1:9999');
    expect(o.bridgeToken).toBe('tok');
    expect(o.internalKey).toBe('lurus_ik_a');
    expect(() => parseArgs([], {})).toThrow(/bridge token missing/);
    expect(parseArgs(['--dry-run'], {}).dryRun).toBe(true);
  });

  test('--bridge-token-env reads another variable (local acceptance stack)', () => {
    const o = parseArgs(['--bridge-token-env', 'ACCEPT_BRIDGE_TOKEN'], { ACCEPT_BRIDGE_TOKEN: 'local' });
    expect(o.bridgeToken).toBe('local');
  });

  test('dry-run plan names every actor and sends nothing', () => {
    const lines = planLines(opts({ dryRun: true })).join('\n');
    for (const t of TENANTS) expect(lines).toContain(t.name);
    expect(lines).toContain('SKIP fault-simulator channel');
    expect(lines).not.toContain('bridge-secret');
    expect(existsSync('/definitely/not/written')).toBe(false);
  });
});

describe('engineer toggles survive a rerun', () => {
  test('an enabled faulty channel is not switched back off', async () => {
    const hub = new FakeHub();
    const o = opts(FULL);
    writeOutputs(o, await runSeed(o, depsFor(hub)));
    hub.channels.find((c) => c.name === 'uat-demo-faulty').status = 1;
    await runSeed(o, depsFor(hub));
    expect(hub.channels.find((c) => c.name === 'uat-demo-faulty').status).toBe(1);
    expect(hub.calls.filter((c) => c === 'PUT /api/channel/')).toHaveLength(0);
  });
});
