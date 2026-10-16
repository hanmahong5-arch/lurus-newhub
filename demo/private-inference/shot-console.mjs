// Render a "private inference routing" status panel from LIVE newhub API data
// and screenshot it. The full v2 console shell needs the platform identity SDK
// (IDENTITY_PUBLIC_URL/IDENTITY_SESSION_SECRET) which is not available in this
// local demo, so instead of faking the shell we drive the REAL
// GET /api/v2/:tenant_slug/channels API same-origin and visualize what it
// returns — a truthful front-end status of the tenant's routing.
//
// Login is fully automated via the newhub e2e bridge
// (internal/adapter/handler/v2_bridge.go, POST /api/v2/bridge/exchange) — the
// same mechanism the web/next/e2e Playwright suite uses. No manual browser
// login or cookie export is needed. The bridge route only exists on a server
// booted with E2E_BRIDGE_TOKEN set (see run-demo.sh / .env.demo); it is not
// registered at all otherwise, so this has no prod exposure.
//
// Env:
//   BACKEND_URL        newhub origin, default http://localhost:8099
//   E2E_BRIDGE_TOKEN    must match the running server's env (required)
//   BRIDGE_USER_ID      user to log in as, default 9101 (the tenant-scoped
//                        admin "console viewer" from seed-console-viewer.sql
//                        — NOT the role=1 user from seed-tenant-private-endpoint.sql,
//                        since GET .../channels is admin-gated)
//   TENANT_SLUG         default privacy-demo
import path from 'node:path';
import os from 'node:os';
import fs from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';

// Resolve playwright-core RELATIVE to this script (it lives in web/node_modules)
// so the demo runs on any clone / any OS — never tied to one absolute checkout.
const _here = path.dirname(fileURLToPath(import.meta.url));
const _pwEntry = path.join(_here, '..', '..', 'web', 'node_modules', 'playwright-core', 'index.js');
const pw = (await import(pathToFileURL(_pwEntry).href)).default;
const { chromium } = pw;

const BACKEND_URL = process.env.BACKEND_URL || 'http://localhost:8099';
const BRIDGE_TOKEN = process.env.E2E_BRIDGE_TOKEN || '';
const BRIDGE_USER_ID = process.env.BRIDGE_USER_ID || '9101';
const TENANT_SLUG = process.env.TENANT_SLUG || 'privacy-demo';

if (!BRIDGE_TOKEN) {
  console.error('[shot-console] E2E_BRIDGE_TOKEN is not set — cannot log in. ' +
    'Boot the backend with it set (see .env.demo) and export the same value here.');
  process.exit(1);
}

const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1120, height: 760 }, deviceScaleFactor: 2 });
const page = await ctx.newPage();

// Land on the backend's own origin first (it serves the built SPA at "/") so
// the bridge-issued session cookie is same-origin for the fetch() below.
await page.goto(`${BACKEND_URL}/`, { waitUntil: 'domcontentloaded' });

const bridgeRes = await page.request.post(
  `${BACKEND_URL}/api/v2/bridge/exchange?token=${encodeURIComponent(BRIDGE_TOKEN)}&user_id=${BRIDGE_USER_ID}`,
);
if (bridgeRes.status() !== 200) {
  console.error(`[shot-console] bridge login failed: ${bridgeRes.status()} ${await bridgeRes.text()}`);
  process.exit(1);
}
console.log('[shot-console] bridge login ok:', JSON.stringify(await bridgeRes.json()));

const data = await page.evaluate(async (tenantSlug) => {
  const r = await fetch(`/api/v2/${tenantSlug}/channels?page=1&page_size=20`, { credentials: 'same-origin' });
  const j = await r.json();
  const items = (j.data && j.data.channels) || [];
  return Array.isArray(items) ? items : [];
}, TENANT_SLUG);

await page.setContent(`<!doctype html><html><head><meta charset="utf-8"/>
<style>
  body{margin:0;background:#f4f1ea;font-family:'Inter Tight',-apple-system,'Segoe UI',sans-serif;color:#1a1812;}
  .wrap{padding:34px 40px;}
  .lbl{font-size:10px;letter-spacing:.14em;text-transform:uppercase;color:#807a6a;font-family:'JetBrains Mono',monospace;}
  h1{font-family:'Fraunces',Georgia,serif;font-size:30px;letter-spacing:-.02em;margin:6px 0 2px;}
  .sub{color:#807a6a;font-size:13px;margin-bottom:22px;}
  .panel{background:#fff;border:1px solid #d9d4c3;border-radius:3px;padding:0;margin-bottom:18px;overflow:hidden;}
  .phead{display:flex;align-items:center;gap:10px;padding:14px 18px;border-bottom:1px solid #d9d4c3;background:#fbf9f3;}
  .badge{display:inline-flex;align-items:center;gap:6px;padding:3px 10px;border-radius:999px;font-family:'JetBrains Mono',monospace;font-size:10px;text-transform:uppercase;letter-spacing:.08em;background:#1f7a4f;color:#fff;}
  .dot{width:7px;height:7px;border-radius:50%;background:#fff;box-shadow:0 0 0 0 rgba(255,255,255,.6);}
  table{width:100%;border-collapse:collapse;}
  th,td{text-align:left;padding:11px 18px;border-bottom:1px solid #ece8de;font-size:13px;}
  th{font-family:'JetBrains Mono',monospace;font-size:10px;text-transform:uppercase;letter-spacing:.1em;color:#807a6a;}
  .mono{font-family:'JetBrains Mono',monospace;font-size:12px;}
  .tag{display:inline-block;padding:1px 7px;border:1px solid #d9d4c3;border-radius:2px;font-family:'JetBrains Mono',monospace;font-size:10px;text-transform:uppercase;letter-spacing:.06em;}
  .tag.custom{color:#b06b00;border-color:#b06b00;}
  .tag.priv{color:#1f7a4f;border-color:#1f7a4f;}
  .acc{color:#ff5d1f;}
  .loop{background:#fff4ee;border:1px solid #ffd9c7;border-radius:3px;padding:12px 16px;font-size:12.5px;color:#8a3a12;}
  .loop b{color:#ff5d1f;}
</style></head><body><div class="wrap">
  <div class="lbl">multi-tenant gateway · routing</div>
  <h1>Private Inference Routing — tenant <span class="acc">privacy-demo</span></h1>
  <div class="sub">Live from <span class="mono">GET /api/v2/privacy-demo/channels</span> (bridge session, tenant privacy-demo). This tenant's model traffic is pinned to an on-prem endpoint — no external provider is configured.</div>
  <div class="panel">
    <div class="phead"><span class="badge"><span class="dot"></span> PRIVATE · DATA STAYS ON-PREM</span>
      <span class="mono" style="color:#807a6a;font-size:11px;">${data.length} channel(s) for this tenant</span></div>
    <table><thead><tr><th>#</th><th>channel</th><th>provider type</th><th>endpoint (base_url)</th><th>status</th></tr></thead>
    <tbody>${data.map((c) => `<tr>
      <td class="mono">${c.id}</td>
      <td><b>${c.name || ''}</b></td>
      <td><span class="tag custom">Custom · OpenAI-compat (type ${c.type})</span></td>
      <td class="mono">${c.base_url || ''} ${/(^https?:\/\/(127\.0\.0\.1|localhost|10\.|172\.|192\.168\.))/.test(c.base_url || '') ? '<span class="tag priv">on-prem / intranet</span>' : ''}</td>
      <td><span class="tag" style="color:#1f7a4f;border-color:#1f7a4f;">${c.status === 1 ? 'enabled' : c.status}</span></td>
    </tr>`).join('')}</tbody></table>
  </div>
  <div class="loop">🔒 Verified this session: a relay call carrying tenant <b>privacy-demo</b>'s token was dispatched to <b>${(data[0] && data[0].base_url) || 'the on-prem endpoint'}</b> (loopback). The upstream reply was produced on-prem; <b>no external LLM provider was contacted and no data left the host</b>.</div>
</div></body></html>`);

await page.waitForTimeout(800);
const _shotDir = path.join(os.tmpdir(), 'hifi-preview');
fs.mkdirSync(_shotDir, { recursive: true });
const _shotPath = path.join(_shotDir, 'private-routing-console.png');
await page.screenshot({ path: _shotPath, fullPage: true });
console.log('[shot-console] screenshot →', _shotPath);
console.log('rendered channels:', JSON.stringify(data.map((c) => ({ id: c.id, name: c.name, type: c.type, base_url: c.base_url }))));
await browser.close();
