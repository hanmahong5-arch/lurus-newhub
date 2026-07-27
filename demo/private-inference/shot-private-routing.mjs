// Render the tenant "private inference routing" console panel from LIVE newhub
// API data and screenshot it.
//
// Difference from the sibling `shot-console.mjs`: that script reads the generic
// channel list and RE-IMPLEMENTS the "is this address internal?" test in a
// front-end regex (`127.0.0.1|localhost|10.|172.|192.168.`). That regex is
// subtly wrong — it accepts 172.32.x (outside RFC1918, which stops at 172.31)
// and misses Kubernetes `.svc` names, RFC6598 tailnet peers and IPv6
// unique-local addresses. This script instead reads
// GET /api/v2/:tenant_slug/private-routing, whose verdicts come from the SAME
// classifier the dispatch guard enforces with
// (internal/pkg/privateendpoint). The console therefore cannot claim
// "on-prem" about a channel the backend would treat differently.
//
// Login is automated through the newhub e2e bridge
// (internal/adapter/handler/v2_bridge.go), the same mechanism the
// web/tests/e2e suite uses; the route only exists when the server is booted
// with E2E_BRIDGE_TOKEN set (.env.demo), so there is no prod exposure.
//
// RUN WITH `node`, NOT `bun` — see README: chromium.launch() hangs for the full
// launch timeout under bun on this host (Playwright pipe-transport issue).
//
// Env:
//   BACKEND_URL       newhub origin, default http://localhost:8099
//   E2E_BRIDGE_TOKEN  must match the running server (required)
//   BRIDGE_USER_ID    default 9201 — the role=10 viewer from
//                     seed-strict-console-viewer.sql (the status endpoint is
//                     admin-gated, so the role=1 proof user cannot call it)
//   TENANT_SLUG       default privacy-strict
//   SHOT_PATH         override the output png path
import path from 'node:path';
import os from 'node:os';
import fs from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';

const _here = path.dirname(fileURLToPath(import.meta.url));
const _pwEntry = path.join(_here, '..', '..', 'web', 'node_modules', 'playwright-core', 'index.js');
const pw = (await import(pathToFileURL(_pwEntry).href)).default;
const { chromium } = pw;

const BACKEND_URL = process.env.BACKEND_URL || 'http://localhost:8099';
const BRIDGE_TOKEN = process.env.E2E_BRIDGE_TOKEN || '';
const BRIDGE_USER_ID = process.env.BRIDGE_USER_ID || '9201';
const TENANT_SLUG = process.env.TENANT_SLUG || 'privacy-strict';

if (!BRIDGE_TOKEN) {
  console.error('[shot-private-routing] E2E_BRIDGE_TOKEN is not set — cannot log in.');
  process.exit(1);
}

const VERDICT_COPY = {
  all_traffic_stays_on_prem: {
    text: 'ALL TRAFFIC STAYS ON-PREM',
    bg: '#1f7a4f',
    note: 'Every usable channel for this tenant is a private endpoint on an intranet address. No external provider is configured.',
  },
  mixed_private_and_external: {
    text: 'MIXED · EXTERNAL EGRESS PRESENT',
    bg: '#b06b00',
    note: 'This tenant has a private endpoint AND channels that reach external providers — traffic can still leave the network.',
  },
  no_private_endpoint_configured: {
    text: 'NO PRIVATE ENDPOINT',
    bg: '#a32020',
    note: 'No usable private-endpoint channel is configured for this tenant.',
  },
};

const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1180, height: 900 }, deviceScaleFactor: 2 });
const page = await ctx.newPage();

// Same-origin first so the bridge cookie applies to the fetch() below.
await page.goto(`${BACKEND_URL}/`, { waitUntil: 'domcontentloaded' });

const bridgeRes = await page.request.post(
  `${BACKEND_URL}/api/v2/bridge/exchange?token=${encodeURIComponent(BRIDGE_TOKEN)}&user_id=${BRIDGE_USER_ID}`,
);
if (bridgeRes.status() !== 200) {
  console.error(`[shot-private-routing] bridge login failed: ${bridgeRes.status()} ${await bridgeRes.text()}`);
  process.exit(1);
}
console.log('[shot-private-routing] bridge login ok:', JSON.stringify(await bridgeRes.json()));

const status = await page.evaluate(async (tenantSlug) => {
  const r = await fetch(`/api/v2/${tenantSlug}/private-routing`, { credentials: 'same-origin' });
  const j = await r.json();
  return { httpStatus: r.status, body: j };
}, TENANT_SLUG);

if (status.httpStatus !== 200 || !status.body || !status.body.data) {
  console.error('[shot-private-routing] status API failed:', JSON.stringify(status).slice(0, 500));
  process.exit(1);
}
const data = status.body.data;
const verdict = VERDICT_COPY[data.verdict] || {
  text: String(data.verdict || 'UNKNOWN').toUpperCase(),
  bg: '#807a6a',
  note: '',
};

const esc = (s) => String(s ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

const row = (c) => `<tr class="${c.will_be_blocked ? 'blocked' : ''}">
  <td class="mono">${esc(c.id)}</td>
  <td><b>${esc(c.name)}</b><div class="mono models">${esc(c.models)}</div></td>
  <td><span class="tag ${c.type === 57 ? 'priv' : 'ext'}">${esc(c.type_name)} · type ${esc(c.type)}</span></td>
  <td class="mono">${esc(c.base_url)}</td>
  <td>${
    c.intranet
      ? '<span class="tag priv">intranet</span>'
      : '<span class="tag danger">public egress</span>'
  }${c.will_be_blocked ? '<div class="mono blockedmsg">blocked at dispatch</div>' : ''}</td>
  <td class="reason">${esc(c.reason)}</td>
</tr>`;

const allRows = [...(data.private_endpoint_channels || []), ...(data.external_channels || [])];

await page.setContent(`<!doctype html><html><head><meta charset="utf-8"/>
<style>
  body{margin:0;background:#f4f1ea;font-family:'Inter Tight',-apple-system,'Segoe UI',sans-serif;color:#1a1812;}
  .wrap{padding:34px 40px;}
  .lbl{font-size:10px;letter-spacing:.14em;text-transform:uppercase;color:#807a6a;font-family:'JetBrains Mono',monospace;}
  h1{font-family:'Fraunces',Georgia,serif;font-size:30px;letter-spacing:-.02em;margin:6px 0 2px;}
  .sub{color:#807a6a;font-size:13px;margin-bottom:20px;}
  .panel{background:#fff;border:1px solid #d9d4c3;border-radius:3px;margin-bottom:18px;overflow:hidden;}
  .phead{display:flex;align-items:center;gap:12px;padding:14px 18px;border-bottom:1px solid #d9d4c3;background:#fbf9f3;flex-wrap:wrap;}
  .badge{display:inline-flex;align-items:center;gap:6px;padding:4px 12px;border-radius:999px;font-family:'JetBrains Mono',monospace;font-size:10px;text-transform:uppercase;letter-spacing:.08em;background:${verdict.bg};color:#fff;}
  .dot{width:7px;height:7px;border-radius:50%;background:#fff;}
  .enforced{font-family:'JetBrains Mono',monospace;font-size:10px;text-transform:uppercase;letter-spacing:.08em;color:#1f7a4f;border:1px solid #1f7a4f;border-radius:2px;padding:3px 8px;}
  table{width:100%;border-collapse:collapse;}
  th,td{text-align:left;padding:10px 18px;border-bottom:1px solid #ece8de;font-size:12.5px;vertical-align:top;}
  th{font-family:'JetBrains Mono',monospace;font-size:10px;text-transform:uppercase;letter-spacing:.1em;color:#807a6a;}
  .mono{font-family:'JetBrains Mono',monospace;font-size:11.5px;}
  .models{color:#807a6a;margin-top:3px;}
  .tag{display:inline-block;padding:1px 7px;border:1px solid #d9d4c3;border-radius:2px;font-family:'JetBrains Mono',monospace;font-size:10px;text-transform:uppercase;letter-spacing:.06em;}
  .tag.priv{color:#1f7a4f;border-color:#1f7a4f;}
  .tag.ext{color:#b06b00;border-color:#b06b00;}
  .tag.danger{color:#a32020;border-color:#a32020;}
  .reason{color:#5c5648;font-size:11.5px;max-width:330px;line-height:1.45;}
  tr.blocked{background:#fdf3f3;}
  .blockedmsg{color:#a32020;margin-top:4px;}
  .acc{color:#ff5d1f;}
  .note{background:#fff4ee;border:1px solid #ffd9c7;border-radius:3px;padding:12px 16px;font-size:12.5px;color:#8a3a12;margin-bottom:14px;}
  .note b{color:#ff5d1f;}
  .src{font-family:'JetBrains Mono',monospace;font-size:10.5px;color:#807a6a;}
</style></head><body><div class="wrap">
  <div class="lbl">multi-tenant gateway · private inference routing</div>
  <h1>Tenant <span class="acc">${esc(data.tenant)}</span> — where does the data go?</h1>
  <div class="sub">Live from <span class="mono">GET /api/v2/${esc(data.tenant)}/private-routing</span>. Every verdict below is produced by the same classifier the gateway enforces with at dispatch time — the console cannot show "intranet" for a channel the backend would refuse.</div>

  <div class="panel">
    <div class="phead">
      <span class="badge"><span class="dot"></span> ${esc(verdict.text)}</span>
      ${data.enforced_by_code ? '<span class="enforced">enforced by code · not a convention</span>' : ''}
      <span class="src">${allRows.length} channel(s) · ${(data.private_endpoint_channels || []).length} private · ${(data.external_channels || []).length} external · ${(data.blocked_channels || []).length} blocked</span>
    </div>
    <table>
      <thead><tr><th>#</th><th>channel</th><th>provider type</th><th>endpoint (base_url)</th><th>classification</th><th>why</th></tr></thead>
      <tbody>${allRows.map(row).join('')}</tbody>
    </table>
  </div>

  <div class="note">${esc(verdict.note)}</div>
  ${
    (data.blocked_channels || []).length
      ? `<div class="note">⛔ <b>${(data.blocked_channels || []).length} channel(s) will be refused at dispatch.</b> A private-endpoint channel pointing at a public address cannot serve traffic: the gateway fails closed before opening a connection, so no prompt is emitted. Shown here rather than hidden, so the misconfigured row is fixable instead of mysterious.</div>`
      : ''
  }
</div></body></html>`);

await page.waitForTimeout(600);
const shotPath = process.env.SHOT_PATH || path.join(os.tmpdir(), 'hifi-preview', 'private-routing-strict-console.png');
fs.mkdirSync(path.dirname(shotPath), { recursive: true });
await page.screenshot({ path: shotPath, fullPage: true });
console.log('[shot-private-routing] screenshot →', shotPath);
console.log('[shot-private-routing] verdict:', data.verdict, '| enforced_by_code:', data.enforced_by_code);
console.log('[shot-private-routing] channels:', JSON.stringify(allRows.map((c) => ({
  id: c.id, name: c.name, type: c.type, base_url: c.base_url, intranet: c.intranet, blocked: c.will_be_blocked,
}))));
await browser.close();
