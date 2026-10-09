#!/usr/bin/env bun
/**
 * UAT business-test seed.
 *
 * Builds the demo world described in doc/uat/business-test-guide.md through
 * the product's own management API only (no SQL, no kubectl):
 *
 *   - demo channel(s) + prices + one routing probe
 *   - two demo enterprise tenants (payer admin, 2 departments, 1 department
 *     lead, 3 employees with keys, a trusted gateway key, a plain key,
 *     unlimited credit pool)
 *   - a platform operator, a read-only-ish observer, six testers with keys of
 *     a known quota (one tiny), and a batch of unused redemption codes
 *
 * Idempotent: every step looks first and only creates what is missing, so a
 * second run creates nothing. Plaintext keys exist only in the response that
 * creates them; they are written to <out>/secrets.csv (outside the repo) and
 * merged with the previous run's file so a re-run does not lose them.
 *
 * Secrets come from the environment, never from argv or a file:
 *   E2E_BRIDGE_TOKEN   the UAT bridge token (required)
 *   UAT_INTERNAL_KEY   an internal API key (lurus_ik_..., scopes user:write)
 *                      (or pass --mint-internal-key to mint one as root)
 *   FAULTSIM_TOKEN     optional, enables the fault-simulator channel
 *   UAT_UPSTREAM_KEY   optional, key for the demo upstream channel
 *
 * Usage: see scripts/uat/README.md.
 */
import { mkdirSync, readFileSync, existsSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

// ---------------------------------------------------------------------------
// Spec: the single source of the "known values" the guide quotes.
// ---------------------------------------------------------------------------

/** Quota units per US dollar (QuotaPerUnit in internal/pkg/common/constants.go). */
export const QUOTA_PER_USD = 500_000;

export const DEFAULT_TENANT_SLUG = 'lurus';
export const DEFAULT_TENANT_ID = 'default';

export interface DeptSpec {
  name: string;
  code: string;
}
export interface EmployeeSpec {
  ref: string;
  name: string;
  dept: number;
}
export interface TenantSpec {
  slug: string;
  name: string;
  /** Short prefix for subjects and key names. */
  code: string;
  admin: { name: string };
  lead: { name: string; dept: number };
  depts: DeptSpec[];
  employees: EmployeeSpec[];
}

export const TENANTS: TenantSpec[] = [
  {
    slug: 'demo-tech',
    name: '演示科技',
    code: 'dt',
    admin: { name: '李明' },
    lead: { name: '王磊', dept: 0 },
    depts: [
      { name: '研发部', code: 'DT-RD' },
      { name: '市场部', code: 'DT-MK' },
    ],
    employees: [
      { ref: 'dt-e01', name: '张三', dept: 0 },
      { ref: 'dt-e02', name: '李四', dept: 0 },
      { ref: 'dt-e03', name: '王五', dept: 1 },
    ],
  },
  {
    slug: 'demo-mfg',
    name: '传统制造',
    code: 'mf',
    admin: { name: '赵强' },
    lead: { name: '孙伟', dept: 0 },
    depts: [
      { name: '生产部', code: 'MF-PR' },
      { name: '采购部', code: 'MF-PU' },
    ],
    employees: [
      { ref: 'mf-e01', name: '钱一', dept: 0 },
      { ref: 'mf-e02', name: '周二', dept: 0 },
      { ref: 'mf-e03', name: '吴三', dept: 1 },
    ],
  },
];

export interface TesterSpec {
  id: string;
  name: string;
  /** Initial quota in units: both the key's remain_quota and the user's quota. */
  quota: number;
  note: string;
}

export const TESTERS: TesterSpec[] = [
  { id: 't01', name: '测试员A', quota: 5_000_000, note: '约 10 美元额度' },
  { id: 't02', name: '测试员B', quota: 5_000_000, note: '约 10 美元额度' },
  { id: 't03', name: '测试员C', quota: 5_000_000, note: '约 10 美元额度' },
  { id: 't04', name: '测试员D', quota: 5_000_000, note: '约 10 美元额度' },
  { id: 't05', name: '测试员E', quota: 5_000_000, note: '约 10 美元额度' },
  {
    id: 't06',
    name: '测试员F',
    quota: 1,
    note: '只剩 1 单位额度,专门演示 402 → 兑换 → 提额 → 重试',
  },
];

export const OPERATOR = { id: 'operator', name: '平台运营' };
export const OBSERVER = { id: 'observer', name: '只读观察员' };

/** Redemption codes minted in the default tenant. */
export const CODE_NAME = 'uat-code';
export const CODE_COUNT = 10;
export const CODE_QUOTA = 500_000; // 1 US dollar

export const EMPLOYEE_KEY_QUOTA = 1_000_000; // 2 US dollars
/** Fault-simulator success model (model name starting "ok"): answers 200, usage 1000/500. */
export const FAULTSIM_OK_MODEL = 'ok-chat';
export const FAULT_MODELS = [
  'http_500',
  'rate_limit_429',
  'mid_stream_abort',
  'slow_headers',
] as const;

// ---------------------------------------------------------------------------
// Options
// ---------------------------------------------------------------------------

export interface Options {
  baseUrl: string;
  bridgeToken: string;
  internalKey: string;
  mintInternalKey: boolean;
  faultsimToken: string;
  upstreamKey: string;
  upstreamBaseUrl: string;
  upstreamType: number;
  demoModel: string;
  /** The vendor's own model name when it differs from demoModel ("" = same). */
  upstreamModel: string;
  modelRatio: number;
  completionRatio: number;
  allowPrivateUpstream: boolean;
  skipProbe: boolean;
  probeTimeoutMs: number;
  out: string;
  dryRun: boolean;
}

export interface Deps {
  fetch: (url: string, init?: RequestInit) => Promise<Response>;
  sleep: (ms: number) => Promise<void>;
  log: (line: string) => void;
}

export class SeedError extends Error {
  constructor(
    public step: string,
    message: string,
    public manual?: string,
  ) {
    super(`[${step}] ${message}`);
  }
}

// ---------------------------------------------------------------------------
// HTTP session (cookie jar, JSON, request id capture)
// ---------------------------------------------------------------------------

interface Reply {
  status: number;
  body: any;
  text: string;
  requestId: string;
}

export class Session {
  private jar = new Map<string, string>();
  constructor(
    private base: string,
    private deps: Deps,
    private extra: Record<string, string> = {},
    public label = 'anon',
  ) {}

  async req(
    method: string,
    path: string,
    json?: unknown,
    headers: Record<string, string> = {},
  ): Promise<Reply> {
    const h: Record<string, string> = { ...this.extra, ...headers };
    if (json !== undefined) h['content-type'] = 'application/json';
    if (this.jar.size) {
      h.cookie = [...this.jar].map(([k, v]) => `${k}=${v}`).join('; ');
    }
    let res: Response;
    for (let attempt = 0; ; attempt++) {
      res = await this.deps.fetch(`${this.base}${path}`, {
        method,
        headers: h,
        body: json === undefined ? undefined : JSON.stringify(json),
        redirect: 'manual',
      });
      if (res.status !== 429 || attempt >= 6) break;
      const ra = Number(res.headers.get('retry-after'));
      await this.deps.sleep(Math.min(Math.max(ra || 15, 1), 65) * 1000);
    }
    const setCookies: string[] =
      typeof (res.headers as any).getSetCookie === 'function'
        ? (res.headers as any).getSetCookie()
        : [];
    for (const sc of setCookies) {
      const [pair] = sc.split(';');
      const i = pair.indexOf('=');
      if (i > 0) this.jar.set(pair.slice(0, i).trim(), pair.slice(i + 1));
    }
    const text = await res.text();
    let body: any = undefined;
    try {
      body = JSON.parse(text);
    } catch {
      /* non-JSON body: keep text */
    }
    return {
      status: res.status,
      body,
      text,
      requestId: res.headers.get('x-request-id') || '',
    };
  }

  /** Requires one of `statuses` and (when the body is JSON) success !== false. */
  async ok(
    what: string,
    method: string,
    path: string,
    json?: unknown,
    statuses: number[] = [200],
    headers: Record<string, string> = {},
  ): Promise<any> {
    const r = await this.req(method, path, json, headers);
    if (!statuses.includes(r.status) || r.body?.success === false) {
      throw new SeedError(
        what,
        `${method} ${path} -> HTTP ${r.status} ${r.text.slice(0, 400)}` +
          (r.requestId ? ` (X-Request-Id ${r.requestId})` : '') +
          ` [session ${this.label}]`,
      );
    }
    return r.body;
  }
}

// ---------------------------------------------------------------------------
// Report model
// ---------------------------------------------------------------------------

export interface Account {
  name: string;
  role: string;
  tenant: string;
  userId: number | null;
  login: string;
  keyName: string;
  keyMasked: string;
  note: string;
}
export interface Secret {
  label: string;
  kind: string;
  value: string;
  note: string;
}
export interface StepResult {
  step: string;
  status: 'ok' | 'skipped' | 'failed';
  detail: string;
  manual?: string;
}
export interface Report {
  accounts: Account[];
  secrets: Secret[];
  steps: StepResult[];
  created: Record<string, number>;
}

export function maskKey(k: string): string {
  if (!k) return '';
  if (k.length <= 10) return `${k.slice(0, 2)}****`;
  return `${k.slice(0, 5)}****${k.slice(-4)}`;
}

// ---------------------------------------------------------------------------
// CSV helpers
// ---------------------------------------------------------------------------

export function csvCell(v: unknown): string {
  const s = v === null || v === undefined ? '' : String(v);
  return /[",\r\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}
export function toCsv(rows: unknown[][]): string {
  return rows.map((r) => r.map(csvCell).join(',')).join('\r\n') + '\r\n';
}
export function parseCsv(text: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = '';
  let q = false;
  const t = text.replace(/^﻿/, '');
  for (let i = 0; i < t.length; i++) {
    const ch = t[i];
    if (q) {
      if (ch === '"' && t[i + 1] === '"') {
        cell += '"';
        i++;
      } else if (ch === '"') q = false;
      else cell += ch;
    } else if (ch === '"') q = true;
    else if (ch === ',') {
      row.push(cell);
      cell = '';
    } else if (ch === '\n' || ch === '\r') {
      if (ch === '\r' && t[i + 1] === '\n') i++;
      row.push(cell);
      rows.push(row);
      row = [];
      cell = '';
    } else cell += ch;
  }
  if (cell !== '' || row.length) {
    row.push(cell);
    rows.push(row);
  }
  return rows.filter((r) => r.some((c) => c !== ''));
}

// ---------------------------------------------------------------------------
// The seed
// ---------------------------------------------------------------------------

function listOf(data: any, ...keys: string[]): any[] {
  if (Array.isArray(data)) return data;
  for (const k of keys) if (Array.isArray(data?.[k])) return data[k];
  return [];
}

export function planLines(o: Options): string[] {
  const L: string[] = [];
  L.push(`target: ${o.baseUrl}`);
  L.push('login as the root user (bridge user_id=1)');
  if (o.mintInternalKey) L.push('mint an internal API key (user:read,user:write)');
  L.push(`ensure credit pool for tenant ${DEFAULT_TENANT_ID}`);
  if (o.allowPrivateUpstream) L.push('allow private upstream addresses (local stack only)');
  if (o.faultsimToken) {
    L.push(
      `ensure channel uat-faultsim -> ${o.baseUrl}/api/v2/faultsim models=${FAULT_MODELS.join(',')}`,
    );
  } else {
    L.push('SKIP fault-simulator channel (FAULTSIM_TOKEN not set)');
  }
  if (o.upstreamBaseUrl) {
    L.push(
      `ensure channel uat-demo-upstream -> ${o.upstreamBaseUrl} model=${o.demoModel}`,
    );
  } else if (o.faultsimToken) {
    L.push(
      `ensure channel uat-demo-upstream -> ${o.baseUrl}/api/v2/faultsim model=${o.demoModel} (mapped to ${FAULTSIM_OK_MODEL}: success mode, 1000/500 tokens)`,
    );
  } else {
    L.push('SKIP demo upstream channel (neither FAULTSIM_TOKEN nor --upstream-base-url set): no model can answer 200');
  }
  if (o.faultsimToken || o.upstreamBaseUrl) {
    L.push(`ensure prices (model_ratio=${o.modelRatio}, completion_ratio=${o.completionRatio})`);
    if (!o.skipProbe) L.push('create/rotate probe key and probe a route');
  }
  for (const t of TENANTS) {
    L.push(`tenant ${t.slug} (${t.name}): create + credit pool`);
    L.push(`  provision admin/lead via internal API; set tenant_role admin+payer`);
    L.push(`  departments: ${t.depts.map((d) => `${d.name}[${d.code}]`).join(', ')}`);
    L.push(`  lead -> ${t.depts[t.lead.dept].name}; employees: ${t.employees.map((e) => e.ref).join(', ')}`);
    L.push(`  keys: gateway (trusted headers), plain, ${t.employees.length} employee keys via batch`);
  }
  L.push(`operator ${OPERATOR.id} (role 100), observer ${OBSERVER.id} (role 10 + audit:read grant)`);
  for (const t of TESTERS) L.push(`tester ${t.id} ${t.name}: key quota ${t.quota}`);
  L.push(`redemption codes: ${CODE_COUNT} x ${CODE_QUOTA} units named ${CODE_NAME}`);
  L.push(`write accounts.md accounts.csv secrets.csv to ${o.out}`);
  return L;
}

export async function runSeed(o: Options, deps: Deps): Promise<Report> {
  const rep: Report = { accounts: [], secrets: [], steps: [], created: {} };
  const bump = (k: string, n = 1) => {
    if (n > 0) rep.created[k] = (rep.created[k] || 0) + n;
  };
  const step = async (name: string, fn: () => Promise<string | void>, manual?: string) => {
    try {
      const detail = (await fn()) || '';
      rep.steps.push({ step: name, status: 'ok', detail });
      deps.log(`ok      ${name} ${detail}`);
    } catch (e) {
      const msg = e instanceof SeedError ? e.message : `[${name}] ${(e as Error).message}`;
      rep.steps.push({
        step: name,
        status: 'failed',
        detail: msg,
        manual: (e instanceof SeedError && e.manual) || manual,
      });
      deps.log(`FAILED  ${msg}`);
      throw e;
    }
  };
  const skip = (name: string, detail: string, manual: string) => {
    rep.steps.push({ step: name, status: 'skipped', detail, manual });
    deps.log(`skipped ${name}: ${detail}`);
  };

  // Plaintext from the previous run (a key is shown once, ever).
  const known = loadSecrets(o.out);
  const secretOf = (label: string) => known.get(label)?.value || '';
  const keep = (s: Secret) => {
    known.set(s.label, s);
  };
  for (const s of known.values()) rep.secrets.push(s);
  const recordSecret = (s: Secret) => {
    keep(s);
    const i = rep.secrets.findIndex((x) => x.label === s.label);
    if (i >= 0) rep.secrets[i] = s;
    else rep.secrets.push(s);
  };

  const mk = (label: string, extra: Record<string, string> = {}) =>
    new Session(o.baseUrl, deps, extra, label);
  const bridgeLogin = async (userId: number, label: string): Promise<Session> => {
    const s = mk(label);
    const r = await s.ok(
      `bridge login ${label}`,
      'POST',
      `/api/v2/bridge/exchange?token=${encodeURIComponent(o.bridgeToken)}&user_id=${userId}`,
    );
    deps.log(`        logged in as user ${userId} (${r?.data?.username ?? '?'}) [${label}]`);
    return s;
  };

  // --- root session --------------------------------------------------------
  let root!: Session;
  await step(
    'bridge-root',
    async () => {
      root = await bridgeLogin(1, 'root');
    },
    'Open /bridge-login#t=<token> in a browser, or fix E2E_BRIDGE_TOKEN; the seed needs the root session.',
  );

  // --- internal key --------------------------------------------------------
  let internalKey = o.internalKey;
  if (!internalKey) {
    if (!o.mintInternalKey) {
      throw new SeedError(
        'internal-key',
        'UAT_INTERNAL_KEY is not set and --mint-internal-key was not given',
        'Create a key in the console (Admin > Internal API keys, scopes user:read,user:write) and export UAT_INTERNAL_KEY, or rerun with --mint-internal-key.',
      );
    }
    await step('mint-internal-key', async () => {
      const prior = secretOf('uat-seed-internal-key');
      if (prior) {
        internalKey = prior;
        return 'reusing the key from the previous run';
      }
      const b = await root.ok(
        'mint internal key',
        'POST',
        '/api/api-keys/',
        { name: 'uat-seed', scopes: ['user:read', 'user:write'] },
        [200, 201],
      );
      internalKey = b.data.key;
      recordSecret({
        label: 'uat-seed-internal-key',
        kind: 'internal_key',
        value: internalKey,
        note: 'minted by seed.ts; revoke after UAT',
      });
      bump('internal_key');
      return 'minted';
    });
  }
  const internal = mk('internal', { 'X-API-Key': internalKey });

  const provision = async (
    subject: string,
    display: string,
    tenantId: string,
    extra: Record<string, unknown> = {},
  ): Promise<{ userId: number; token?: { key: string } ; isExisting: boolean }> => {
    const b = await internal.ok(
      `provision ${subject}`,
      'POST',
      '/internal/user/provision',
      {
        idp_subject: subject,
        email: `${subject}@uat.lurus.test`,
        display_name: display,
        tenant_id: tenantId,
        ...extra,
      },
      [200, 201],
    );
    if (!b.data?.user_id) {
      throw new SeedError(`provision ${subject}`, `no user_id in ${JSON.stringify(b).slice(0, 300)}`);
    }
    return { userId: b.data.user_id, token: b.data.token, isExisting: !!b.data.is_existing };
  };
  const loginHint = (userId: number | null) =>
    userId
      ? `${o.baseUrl}/bridge-login#u=${userId} (令牌向工程师索取)`
      : '(无登录:仅用 key 调用)';

  // --- default tenant: credit pool ----------------------------------------
  await step('default-credit-pool', async () => {
    const got = await root.req('GET', `/api/v2/admin/tenants/${DEFAULT_TENANT_ID}/credit-pool`);
    if (got.status === 200 && got.body?.data?.id) return 'already present';
    await root.ok(
      'create default credit pool',
      'POST',
      `/api/v2/admin/tenants/${DEFAULT_TENANT_ID}/credit-pool`,
      { max_balance: -1, reset_period: 'monthly', alert_threshold_pct: 80 },
      [200, 201],
    );
    bump('credit_pool');
    return 'created';
  });

  if (o.allowPrivateUpstream) {
    await step('allow-private-upstream', async () => {
      const port = new URL(o.upstreamBaseUrl || o.baseUrl).port || '443';
      const put = (key: string, value: string) =>
        root.ok(`set option ${key}`, 'PUT', '/api/option/', { key, value });
      await put('fetch_setting.allow_private_ip', 'true');
      await put('fetch_setting.ip_filter_mode', 'true');
      await put('fetch_setting.ip_list', JSON.stringify(['127.0.0.1/32']));
      await put(
        'fetch_setting.allowed_ports',
        JSON.stringify(['80', '443', '8080', '8443', port]),
      );
      return 'loopback upstream allowed (never use on UAT)';
    });
  }

  // --- channels ------------------------------------------------------------
  const ensureChannel = async (spec: {
    name: string;
    type: number;
    models: string;
    key: string;
    base_url: string;
    priority?: number;
    model_mapping?: string;
    /** Applied at creation only: a later run never flips a status an engineer toggled. */
    initialStatus?: number;
  }): Promise<string> => {
    const found = await root.ok(
      'search channels',
      'GET',
      `/api/channel/search?keyword=${encodeURIComponent(spec.name)}&p=1&page_size=50`,
    );
    const items = listOf(found.data, 'items');
    const existing = items.find((c) => c.name === spec.name);
    const { initialStatus, ...wire } = spec;
    const fields = {
      group: 'default',
      priority: 0,
      weight: 1,
      model_mapping: '',
      ...wire,
      status: existing ? existing.status : (initialStatus ?? 1),
    };
    if (existing) {
      const same =
        existing.models === spec.models &&
        existing.base_url === spec.base_url &&
        existing.type === spec.type &&
        (existing.model_mapping ?? '') === (spec.model_mapping ?? '');
      if (same) return `unchanged (id ${existing.id})`;
      await root.ok('update channel', 'PUT', '/api/channel/', { id: existing.id, ...fields });
      return `updated (id ${existing.id})`;
    }
    await root.ok('create channel', 'POST', '/api/channel/', { mode: 'single', channel: fields }, [200, 201]);
    bump('channel');
    return 'created';
  };

  if (o.faultsimToken) {
    await step(
      'channel-faultsim',
      async () =>
        ensureChannel({
          name: 'uat-faultsim',
          type: 1,
          models: FAULT_MODELS.join(','),
          key: o.faultsimToken,
          base_url: `${o.baseUrl}/api/v2/faultsim`,
        }),
      'Create the channel in the console: channel type 1 (generic chat-completions), base URL <site>/api/v2/faultsim, key = FAULTSIM_TOKEN, models http_500,rate_limit_429,mid_stream_abort,slow_headers.',
    );
    if ((o.upstreamBaseUrl && o.upstreamKey) || !o.upstreamBaseUrl) {
      await step(
        'channel-demo-faulty',
        async () =>
          ensureChannel({
            name: 'uat-demo-faulty',
            type: 1,
            models: o.demoModel,
            key: o.faultsimToken,
            base_url: `${o.baseUrl}/api/v2/faultsim`,
            priority: 10,
            model_mapping: JSON.stringify({ [o.demoModel]: 'http_500' }),
            initialStatus: 2,
          }),
        'Create a second channel for the demo model, base URL <site>/api/v2/faultsim, key = FAULTSIM_TOKEN, higher priority, model mapping {"<demo model>":"http_500"}, and leave it DISABLED until the engineer demo.',
      );
    }
  } else {
    skip(
      'channel-faultsim',
      'FAULTSIM_TOKEN is not set; the fault simulator routes only exist when the server has FAULTSIM_TOKEN',
      'Operator step (deploy/k8s/r6-uat/README.md, "Fault simulator"): generate a token on the host, add FAULTSIM_TOKEN to the UAT secret and deployment, let ArgoCD converge, then rerun with FAULTSIM_TOKEN exported. Scenario 11 (channel cooling) cannot be demonstrated until then.',
    );
  }

  if (!o.upstreamBaseUrl && o.faultsimToken) {
    // No real upstream: the fault simulator's success mode answers 200 with a
    // fixed 1000 prompt / 500 completion usage, so every billed scenario runs.
    await step(
      'channel-demo-upstream',
      async () =>
        ensureChannel({
          name: 'uat-demo-upstream',
          type: 1,
          models: o.demoModel,
          key: o.faultsimToken,
          base_url: `${o.baseUrl}/api/v2/faultsim`,
          model_mapping: JSON.stringify({ [o.demoModel]: FAULTSIM_OK_MODEL }),
        }),
      `Create the channel in the console: channel type 1, base URL <site>/api/v2/faultsim, key = FAULTSIM_TOKEN, model ${o.demoModel}, model mapping {"${o.demoModel}":"${FAULTSIM_OK_MODEL}"}.`,
    );
  } else if (o.upstreamBaseUrl) {
    await step(
      'channel-demo-upstream',
      async () => {
        if (!o.upstreamKey) {
          throw new SeedError(
            'channel-demo-upstream',
            'UAT_UPSTREAM_KEY is not set',
            'Export UAT_UPSTREAM_KEY (the vendor key, or the fake upstream key on the local stack).',
          );
        }
        return ensureChannel({
          name: 'uat-demo-upstream',
          type: o.upstreamType,
          models: o.demoModel,
          key: o.upstreamKey,
          base_url: o.upstreamBaseUrl,
          model_mapping:
            o.upstreamModel && o.upstreamModel !== o.demoModel
              ? JSON.stringify({ [o.demoModel]: o.upstreamModel })
              : '',
        });
      },
      'Create the channel in the console with a real, funded upstream key.',
    );
  } else {
    skip(
      'channel-demo-upstream',
      'neither FAULTSIM_TOKEN nor --upstream-base-url is set, so no demo model can answer 200 and nothing is billed',
      'Scenarios that need a successful, billed call (4-8, 10, 12, 13) need an upstream. Preferred: set FAULTSIM_TOKEN on the UAT deployment (operator step, deploy/k8s/r6-uat/README.md "Fault simulator") and rerun with FAULTSIM_TOKEN exported; the seed then maps the demo model to the simulator success model ok-chat. Alternatively pass --upstream-base-url (+ UAT_UPSTREAM_KEY), or create a channel by hand in the console and price its model (Admin > Pricing). On the local acceptance stack use ACCEPT_FAKE_URL.',
    );
  }

  // --- prices --------------------------------------------------------------
  const models: string[] = [];
  if (o.faultsimToken) models.push(...FAULT_MODELS);
  if (o.upstreamBaseUrl || o.faultsimToken) models.push(o.demoModel);
  if (models.length) {
    await step(
      'pricing',
      async () => {
        const g = await root.ok('read pricing', 'GET', `/api/v2/${DEFAULT_TENANT_SLUG}/pricing`);
        const have = new Map<string, any>();
        for (const p of listOf(g.data, 'pricing')) have.set(p.model_name, p);
        const missing = models.filter((m) => {
          const p = have.get(m);
          return !p || !((p.model_ratio ?? 0) > 0 || (p.model_price ?? 0) > 0);
        });
        if (!missing.length) return `all ${models.length} models already priced`;
        const version = g.data?.version ?? 0;
        await root.ok(
          'write pricing',
          'POST',
          `/api/v2/${DEFAULT_TENANT_SLUG}/pricing`,
          missing.map((m) => ({
            model_name: m,
            model_ratio: o.modelRatio,
            completion_ratio: o.completionRatio,
          })),
          [200],
          { 'If-Match-Pricing-Version': String(version) },
        );
        bump('priced_model', missing.length);
        return `priced ${missing.join(', ')}`;
      },
      'Admin > Pricing: set a model ratio for each demo model; an unpriced model is refused before it reaches the channel.',
    );
  }

  // --- route probe -----------------------------------------------------------
  if (models.length && !o.skipProbe) {
    await probeRoute(o, deps, root, rep, secretOf, recordSecret, models, step, skip, bump);
  }

  // --- tenants ----------------------------------------------------------------
  const tenantIds = new Map<string, string>();
  for (const t of TENANTS) {
    await seedTenant(t, o, deps, root, internal, bridgeLogin, provision, rep, tenantIds, secretOf, recordSecret, step, bump, loginHint);
  }

  // --- operator + observer ---------------------------------------------------
  let operatorId = 0;
  await step('operator', async () => {
    const p = await provision(`uat-${OPERATOR.id}`, OPERATOR.name, DEFAULT_TENANT_ID);
    operatorId = p.userId;
    await root.ok('set operator role', 'PUT', `/api/v2/admin/users/${p.userId}`, { role: 100 });
    rep.accounts.push({
      name: OPERATOR.name,
      role: '平台运营(root 级,可进运营页)',
      tenant: `默认租户 ${DEFAULT_TENANT_SLUG}`,
      userId: p.userId,
      login: loginHint(p.userId),
      keyName: '',
      keyMasked: '',
      note: '',
    });
    return `user ${p.userId}`;
  });
  await step('observer', async () => {
    const p = await provision(`uat-${OBSERVER.id}`, OBSERVER.name, DEFAULT_TENANT_ID);
    await root.ok('set observer role', 'PUT', `/api/v2/admin/users/${p.userId}`, { role: 10 });
    const grants = await root.ok('list grants', 'GET', '/api/v2/admin/authz/grants');
    const has = listOf(grants.data).some(
      (g) => g.user_id === p.userId && g.resource === 'audit' && g.action === 'read' && !g.revoked_at && !g.expired,
    );
    if (!has) {
      await root.ok(
        'grant audit read',
        'POST',
        '/api/v2/admin/authz/grants',
        { user_id: p.userId, resource: 'audit', action: 'read', tenant_id: null },
        [200, 201],
      );
      bump('grant');
    }
    rep.accounts.push({
      name: OBSERVER.name,
      role: '观察员(管理员级 role=10 + 审计只读授权)',
      tenant: `默认租户 ${DEFAULT_TENANT_SLUG}`,
      userId: p.userId,
      login: loginHint(p.userId),
      keyName: '',
      keyMasked: '',
      note: '产品没有真正的"只读"角色:这是最接近的近似,可看审计,不能管理租户/渠道',
    });
    return `user ${p.userId}`;
  });
  void operatorId;

  // --- testers -----------------------------------------------------------------
  for (const t of TESTERS) {
    await step(`tester-${t.id}`, async () => {
      const label = `uat-key-${t.id}`;
      const p = await provision(`uat-${t.id}`, t.name, DEFAULT_TENANT_ID, {
        create_initial_token: true,
        initial_token_name: label,
        initial_quota: t.quota,
      });
      if (p.token?.key) {
        recordSecret({
          label,
          kind: 'api_key',
          value: p.token.key.startsWith('sk-') ? p.token.key : `sk-${p.token.key}`,
          note: `${t.name} quota ${t.quota} units`,
        });
        bump('tester_key');
      }
      const plain = secretOf(label);
      rep.accounts.push({
        name: t.name,
        role: '测试员(默认租户普通用户)',
        tenant: `默认租户 ${DEFAULT_TENANT_SLUG}`,
        userId: p.userId,
        login: loginHint(p.userId),
        keyName: label,
        keyMasked: plain ? maskKey(plain) : '(已存在,明文见上次 secrets.csv)',
        note: `额度 ${t.quota} 单位(约 ${(t.quota / QUOTA_PER_USD).toFixed(2)} 美元);${t.note}`,
      });
      return `user ${p.userId}${p.isExisting ? ' (existing)' : ' (created)'}`;
    });
  }

  // --- redemption codes ----------------------------------------------------------
  await step('redemption-codes', async () => {
    const path = `/api/v2/${DEFAULT_TENANT_SLUG}/redemptions`;
    const have = await root.ok(
      'list codes',
      'GET',
      `${path}?keyword=${encodeURIComponent(CODE_NAME)}&page=1&page_size=100`,
    );
    const mine = listOf(have.data, 'redemptions').filter((r) => r.name === CODE_NAME);
    const need = CODE_COUNT - mine.length;
    if (need <= 0) return `${mine.length} codes already present`;
    const b = await root.ok(
      'mint codes',
      'POST',
      path,
      { name: CODE_NAME, count: need, quota: CODE_QUOTA },
      [200, 201],
    );
    const codes: { key: string; id: number }[] = b.data?.codes ?? [];
    codes.forEach((c, i) =>
      recordSecret({
        label: `${CODE_NAME}-${c.id}`,
        kind: 'redemption_code',
        value: c.key,
        note: `${CODE_QUOTA} units (1 USD), unused at ${new Date().toISOString().slice(0, 10)} #${i + 1}`,
      }),
    );
    bump('redemption_code', codes.length);
    return `minted ${codes.length}`;
  });

  return rep;
}

async function probeRoute(
  o: Options,
  deps: Deps,
  root: Session,
  rep: Report,
  secretOf: (l: string) => string,
  recordSecret: (s: Secret) => void,
  models: string[],
  step: (n: string, f: () => Promise<string | void>, m?: string) => Promise<void>,
  skip: (n: string, d: string, m: string) => void,
  bump: (k: string, n?: number) => void,
) {
  const label = 'uat-seed-probe';
  const path = `/api/v2/${DEFAULT_TENANT_SLUG}/tokens`;
  const manual =
    'Wait one channel-cache sync (about 60 s) then call the demo model with any tester key; a 200 (or the simulated 5xx/429 for fault models) proves the route.';
  await step(
    'route-probe',
    async () => {
      let key = secretOf(label);
      if (!key) {
        const list = await root.ok('list tokens', 'GET', `${path}?p=1&size=100`);
        const ex = listOf(list.data, 'items').find((t) => t.name === label);
        if (ex) {
          const r = await root.ok('rotate probe key', 'POST', `${path}/${ex.id}/rotate`, undefined, [200, 201]);
          key = r.data?.key;
        } else {
          const c = await root.ok(
            'create probe key',
            'POST',
            path,
            { name: label, expired_time: -1, unlimited_quota: true },
            [201],
          );
          key = c.data.key;
          bump('probe_key');
        }
        recordSecret({ label, kind: 'api_key', value: key, note: 'seed routing probe (root-owned, unlimited)' });
      }
      const model = models.includes(o.demoModel) ? o.demoModel : models[0];
      const deadline = Date.now() + o.probeTimeoutMs;
      let last = '';
      const relay = mkRelay(o, deps, key);
      for (;;) {
        const r = await relay(model);
        last = `${r.status} ${r.text.slice(0, 200)}`;
        const unrouted = /no available channel|无可用渠道|model_not_found|not found/i.test(r.text);
        const reached =
          model === o.demoModel ? r.status === 200 : (r.status >= 500 || r.status === 429) && !unrouted;
        if (reached) return `model ${model} reached its channel (HTTP ${r.status})`;
        if (Date.now() > deadline) {
          throw new SeedError('route-probe', `model ${model} did not route within ${o.probeTimeoutMs}ms; last: ${last}`, manual);
        }
        await deps.sleep(3000);
      }
    },
    manual,
  );
  void skip;
  void rep;
}

function mkRelay(o: Options, deps: Deps, key: string) {
  const s = new Session(o.baseUrl, deps, { authorization: `Bearer ${key}` }, 'probe');
  return (model: string) =>
    s.req('POST', '/v1/chat/completions', {
      model,
      messages: [{ role: 'user', content: 'ping' }],
      max_tokens: 5,
    });
}

async function seedTenant(
  t: TenantSpec,
  o: Options,
  deps: Deps,
  root: Session,
  internal: Session,
  bridgeLogin: (id: number, label: string) => Promise<Session>,
  provision: (
    s: string,
    d: string,
    tenantId: string,
    extra?: Record<string, unknown>,
  ) => Promise<{ userId: number; token?: { key: string }; isExisting: boolean }>,
  rep: Report,
  tenantIds: Map<string, string>,
  secretOf: (l: string) => string,
  recordSecret: (s: Secret) => void,
  step: (n: string, f: () => Promise<string | void>, m?: string) => Promise<void>,
  bump: (k: string, n?: number) => void,
  loginHint: (id: number | null) => string,
) {
  void deps;
  void internal;
  const P = `tenant-${t.code}`;
  let tenantId = '';
  await step(`${P}:create`, async () => {
    const l = await root.ok('list tenants', 'GET', '/api/v2/admin/tenants?page=1&page_size=100');
    const ex = listOf(l.data, 'tenants').find((x) => x.slug === t.slug);
    if (ex) {
      tenantId = ex.id;
      return `exists (${tenantId})`;
    }
    const b = await root.ok(
      'create tenant',
      'POST',
      '/api/v2/admin/tenants',
      { zitadel_org_id: `uat-org-${t.slug}`, slug: t.slug, name: t.name, max_users: 50 },
      [201],
    );
    tenantId = b.data.id;
    bump('tenant');
    return `created (${tenantId})`;
  });
  tenantIds.set(t.slug, tenantId);

  await step(`${P}:credit-pool`, async () => {
    const got = await root.req('GET', `/api/v2/admin/tenants/${tenantId}/credit-pool`);
    if (got.status === 200 && got.body?.data?.id) return 'already present';
    await root.ok(
      'create tenant credit pool',
      'POST',
      `/api/v2/admin/tenants/${tenantId}/credit-pool`,
      { max_balance: -1, reset_period: 'monthly', alert_threshold_pct: 80 },
      [200, 201],
    );
    bump('credit_pool');
    return 'created (unlimited)';
  });

  let adminId = 0;
  let leadId = 0;
  await step(`${P}:people`, async () => {
    const a = await provision(`uat-${t.code}-admin`, t.admin.name, tenantId);
    const l = await provision(`uat-${t.code}-lead`, t.lead.name, tenantId);
    adminId = a.userId;
    leadId = l.userId;
    await root.ok(
      'set payer admin',
      'PUT',
      `/api/v2/admin/tenants/${tenantId}/members/${adminId}/role`,
      { tenant_role: 'admin', payer: true },
    );
    return `admin ${adminId}, lead ${leadId}`;
  });

  const admin = await (async () => {
    let s!: Session;
    await step(`${P}:admin-login`, async () => {
      s = await bridgeLogin(adminId, `${t.code}-admin`);
    });
    return s;
  })();
  const V = `/api/v2/${t.slug}`;

  const deptIds: number[] = [];
  await step(`${P}:departments`, async () => {
    const l = await admin.ok('list departments', 'GET', `${V}/projects`);
    const items = listOf(l.data, 'items');
    let made = 0;
    for (const d of t.depts) {
      const ex = items.find((p) => p.name === d.name || p.external_code === d.code);
      if (ex) {
        deptIds.push(ex.id);
        continue;
      }
      const b = await admin.ok(
        `create department ${d.name}`,
        'POST',
        `${V}/projects`,
        { name: d.name, external_code: d.code },
        [201],
      );
      deptIds.push(b.data.id);
      made++;
    }
    if (made) bump('department', made);
    return `${deptIds.length} departments (${made} new)`;
  });

  await step(`${P}:lead`, async () => {
    const dept = deptIds[t.lead.dept];
    await admin.ok(
      'add dept lead',
      'POST',
      `${V}/projects/${dept}/members`,
      { user_id: leadId },
      [200, 201],
    );
    return `lead ${leadId} -> ${t.depts[t.lead.dept].name}`;
  });

  // Keys. Existing names are never re-created.
  const tokenPath = `${V}/tokens`;
  const listTokens = async (): Promise<any[]> => {
    const out: any[] = [];
    for (let p = 1; p <= 20; p++) {
      const r = await admin.ok('list tokens', 'GET', `${tokenPath}?p=${p}&size=100`);
      const items = listOf(r.data, 'items');
      out.push(...items);
      if (items.length < 100) break;
    }
    return out;
  };

  const mkKey = async (label: string, fields: Record<string, unknown>, note: string) => {
    const tokens = await listTokens();
    if (tokens.some((x) => x.name === label)) return false;
    const c = await admin.ok(
      `create key ${label}`,
      'POST',
      tokenPath,
      { expired_time: -1, name: label, ...fields },
      [201],
    );
    recordSecret({ label, kind: 'api_key', value: c.data.key, note });
    bump('tenant_key');
    return true;
  };
  await step(`${P}:gateway-key`, async () => {
    const made = await mkKey(
      `${t.code}-gateway`,
      { unlimited_quota: true, trusted_identity_headers: true },
      `${t.name} 可信网关 key:带 X-Lurus-Employee / X-Lurus-Dept 头归属到员工和部门`,
    );
    return made ? 'created' : 'exists';
  });
  await step(`${P}:plain-key`, async () => {
    const made = await mkKey(
      `${t.code}-plain`,
      { unlimited_quota: true, employee_ref: `${t.code}-plain-emp` },
      `${t.name} 普通 key:带身份头会被忽略`,
    );
    return made ? 'created' : 'exists';
  });

  await step(`${P}:employee-keys`, async () => {
    const b = await admin.ok(
      'batch employee keys',
      'POST',
      `${tokenPath}/batch`,
      {
        tokens: t.employees.map((e) => ({
          name: e.ref,
          employee_ref: e.ref,
          project_id: deptIds[e.dept],
          remain_quota: EMPLOYEE_KEY_QUOTA,
        })),
      },
      [200, 201],
    );
    for (const c of b.data?.created ?? []) {
      recordSecret({
        label: c.name,
        kind: 'api_key',
        value: c.key,
        note: `${t.name} 员工 key (${c.employee_ref}) 额度 ${EMPLOYEE_KEY_QUOTA} 单位`,
      });
    }
    bump('employee_key', (b.data?.created ?? []).length);
    return `${(b.data?.created ?? []).length} new, ${(b.data?.skipped ?? []).length} existing`;
  });

  // Account table rows (masks come from the live token list, so they are right even on a rerun).
  const tokens = await listTokens();
  const mask = (label: string) => {
    const plain = secretOf(label);
    if (plain) return maskKey(plain);
    const tk = tokens.find((x) => x.name === label);
    return tk?.key ? String(tk.key) : '(未取回)';
  };
  const T = `${t.name} (${t.slug})`;
  rep.accounts.push({
    name: t.admin.name,
    role: '企业管理员 + 付款人(tenant_role=admin)',
    tenant: T,
    userId: adminId,
    login: loginHint(adminId),
    keyName: '',
    keyMasked: '',
    note: '建部门、批量发 key、看账单',
  });
  rep.accounts.push({
    name: t.lead.name,
    role: `部门负责人(${t.depts[t.lead.dept].name})`,
    tenant: T,
    userId: leadId,
    login: loginHint(leadId),
    keyName: '',
    keyMasked: '',
    note: `只看本部门;部门编码 ${t.depts[t.lead.dept].code}`,
  });
  for (const e of t.employees) {
    rep.accounts.push({
      name: e.name,
      role: `员工(${t.depts[e.dept].name})`,
      tenant: T,
      userId: null,
      login: loginHint(null),
      keyName: e.ref,
      keyMasked: mask(e.ref),
      note: `employee_ref=${e.ref};部门编码 ${t.depts[e.dept].code};额度 ${EMPLOYEE_KEY_QUOTA} 单位`,
    });
  }
  rep.accounts.push({
    name: `${t.name} 网关`,
    role: '可信网关 key',
    tenant: T,
    userId: null,
    login: loginHint(null),
    keyName: `${t.code}-gateway`,
    keyMasked: mask(`${t.code}-gateway`),
    note: '不限额;携带 X-Lurus-Employee/X-Lurus-Dept',
  });
  rep.accounts.push({
    name: `${t.name} 普通 key`,
    role: '普通 key(对照组)',
    tenant: T,
    userId: null,
    login: loginHint(null),
    keyName: `${t.code}-plain`,
    keyMasked: mask(`${t.code}-plain`),
    note: '不限额;带身份头会被忽略',
  });
}

// ---------------------------------------------------------------------------
// Output files
// ---------------------------------------------------------------------------

const SECRET_HEADER = ['label', 'kind', 'value', 'note'];

export function loadSecrets(out: string): Map<string, Secret> {
  const m = new Map<string, Secret>();
  const f = join(out, 'secrets.csv');
  if (!existsSync(f)) return m;
  const rows = parseCsv(readFileSync(f, 'utf8'));
  for (const r of rows.slice(1)) {
    if (r.length >= 3 && r[2]) m.set(r[0], { label: r[0], kind: r[1], value: r[2], note: r[3] ?? '' });
  }
  return m;
}

export function renderAccountsCsv(accts: Account[]): string {
  return (
    '﻿' +
    toCsv([
      ['姓名', '角色', '租户', 'user_id', '登录方式', 'key 名称', 'key 掩码', '备注'],
      ...accts.map((a) => [a.name, a.role, a.tenant, a.userId ?? '', a.login, a.keyName, a.keyMasked, a.note]),
    ])
  );
}

export function renderAccountsMd(accts: Account[], o: Options, rep: Report): string {
  const esc = (s: unknown) => String(s ?? '').replace(/\|/g, '\\|');
  const lines: string[] = [];
  lines.push('# UAT 测试账号对照表', '');
  lines.push(`网址: ${o.baseUrl}`, '');
  lines.push('本表不含任何完整密钥;明文 key 与兑换码在同目录 secrets.csv(请妥善保管,不要提交到仓库、不要贴到群里)。', '');
  lines.push('| 姓名 | 角色 | 租户 | user_id | 登录方式 | key 名称 | key 掩码 | 备注 |', '|---|---|---|---|---|---|---|---|');
  for (const a of accts) {
    lines.push(`| ${[a.name, a.role, a.tenant, a.userId ?? '', a.login, a.keyName, a.keyMasked, a.note].map(esc).join(' | ')} |`);
  }
  lines.push('', '## 种子步骤结果', '', '| 步骤 | 结果 | 说明 |', '|---|---|---|');
  for (const s of rep.steps) lines.push(`| ${esc(s.step)} | ${s.status} | ${esc(s.detail)}${s.manual ? ` 手工替代: ${esc(s.manual)}` : ''} |`);
  lines.push('');
  return lines.join('\n');
}

export function writeOutputs(o: Options, rep: Report): string[] {
  mkdirSync(o.out, { recursive: true });
  const files = [join(o.out, 'accounts.md'), join(o.out, 'accounts.csv'), join(o.out, 'secrets.csv')];
  writeFileSync(files[0], renderAccountsMd(rep.accounts, o, rep));
  writeFileSync(files[1], renderAccountsCsv(rep.accounts));
  writeFileSync(
    files[2],
    '﻿' +
      toCsv([
        SECRET_HEADER,
        ...rep.secrets.map((s) => [s.label, s.kind, s.value, s.note]),
      ]),
    { mode: 0o600 },
  );
  return files;
}

// ---------------------------------------------------------------------------
// CLI
// ---------------------------------------------------------------------------

export function parseArgs(argv: string[], env: Record<string, string | undefined>): Options {
  const flags = new Map<string, string>();
  const bools = new Set<string>();
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (!a.startsWith('--')) throw new Error(`unexpected argument ${a}`);
    const [k, inline] = a.slice(2).split(/=(.*)/s);
    if (['dry-run', 'mint-internal-key', 'allow-private-upstream', 'skip-probe'].includes(k)) {
      bools.add(k);
    } else if (inline !== undefined) flags.set(k, inline);
    else flags.set(k, argv[++i] ?? '');
  }
  for (const secretFlag of ['bridge-token', 'internal-key', 'faultsim-token', 'upstream-key']) {
    if (flags.has(secretFlag) || bools.has(secretFlag)) {
      throw new Error(
        `--${secretFlag} is refused: secrets are read from the environment (arguments leak into process lists). ` +
          `Use ${secretFlag === 'bridge-token' ? 'E2E_BRIDGE_TOKEN' : secretFlag === 'internal-key' ? 'UAT_INTERNAL_KEY' : secretFlag === 'faultsim-token' ? 'FAULTSIM_TOKEN' : 'UAT_UPSTREAM_KEY'} (or --${secretFlag}-env NAME).`,
      );
    }
  }
  const envName = (flag: string, dflt: string) => flags.get(`${flag}-env`) || dflt;
  const get = (n: string) => env[n] || '';
  const baseUrl = (flags.get('base-url') || env.UAT_BASE_URL || 'https://test-newhub.lurus.cn').replace(/\/+$/, '');
  const dryRun = bools.has('dry-run');
  const bridgeToken = get(envName('bridge-token', 'E2E_BRIDGE_TOKEN'));
  if (!bridgeToken && !dryRun) {
    throw new Error(`bridge token missing: export ${envName('bridge-token', 'E2E_BRIDGE_TOKEN')}`);
  }
  return {
    baseUrl,
    bridgeToken,
    internalKey: get(envName('internal-key', 'UAT_INTERNAL_KEY')),
    mintInternalKey: bools.has('mint-internal-key'),
    faultsimToken: get(envName('faultsim-token', 'FAULTSIM_TOKEN')),
    upstreamKey: get(envName('upstream-key', 'UAT_UPSTREAM_KEY')),
    upstreamBaseUrl: (flags.get('upstream-base-url') || '').replace(/\/+$/, ''),
    upstreamType: Number(flags.get('upstream-type') || 1),
    demoModel: flags.get('demo-model') || 'uat-demo-chat',
    upstreamModel: flags.get('upstream-model') || '',
    modelRatio: Number(flags.get('model-ratio') || 0.15),
    completionRatio: Number(flags.get('completion-ratio') || 4),
    allowPrivateUpstream: bools.has('allow-private-upstream'),
    skipProbe: bools.has('skip-probe'),
    probeTimeoutMs: Number(flags.get('probe-timeout-ms') || 150_000),
    out: flags.get('out') || join(tmpdir(), 'lurus-uat-seed'),
    dryRun,
  };
}

export async function main(argv: string[], env: Record<string, string | undefined>): Promise<number> {
  let o: Options;
  try {
    o = parseArgs(argv, env);
  } catch (e) {
    console.error((e as Error).message);
    return 2;
  }
  if (o.dryRun) {
    console.log('DRY RUN: no request is sent, no file is written.');
    for (const l of planLines(o)) console.log(`  ${l}`);
    return 0;
  }
  const deps: Deps = {
    fetch: (u, i) => fetch(u, i),
    sleep: (ms) => new Promise((r) => setTimeout(r, ms)),
    log: (l) => console.log(l),
  };
  let rep: Report | undefined;
  try {
    rep = await runSeed(o, deps);
  } catch (e) {
    console.error(`\nSeed stopped: ${(e as Error).message}`);
    const m = e instanceof SeedError ? e.manual : undefined;
    if (m) console.error(`Manual alternative: ${m}`);
    console.error('Everything created before this point is kept; fix the cause and rerun (the script is idempotent).');
    return 1;
  }
  const files = writeOutputs(o, rep);
  console.log('\nCreated this run:', JSON.stringify(rep.created));
  const skipped = rep.steps.filter((s) => s.status === 'skipped');
  for (const s of skipped) console.log(`SKIPPED ${s.step}: ${s.detail}\n  manual: ${s.manual}`);
  console.log(`\nWrote:\n  ${files.join('\n  ')}`);
  console.log('secrets.csv holds plaintext keys: store it safely and never commit it.');
  return 0;
}

if (import.meta.main) {
  process.exit(await main(process.argv.slice(2), process.env));
}
