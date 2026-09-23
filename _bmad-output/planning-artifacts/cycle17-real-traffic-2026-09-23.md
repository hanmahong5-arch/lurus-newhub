# Cycle 17 — real traffic first (2026-09-23)

## Why this cycle changed direction

Cycles 15–16 polished the console against OpenRouter. The product review this
cycle asked a different question — who uses a gateway and why — and the
answer in our own data was blunt: **hub had no real traffic**. Not because the
product was missing features, but because every Lurus product still called
the retiring gateway, `newapi.lurus.cn` (R1).

Measured on newapi, last 30 days (R1 PG `newapi.logs`): 43,906 calls, all from
one account (root), 10 keys, almost all DeepSeek. So neither gateway had an
external customer; the only real traffic was our own, on the wrong gateway.

Strategy decided with the operator (full delegation): make hub the one place
real traffic goes, first by moving our own products (dogfood), then aim at
mid-size Chinese companies with in-house dev teams that cannot buy the vendors'
enterprise plans (see the positioning notes in the session report). Console
polish pauses until real users say what to change.

## Delivered

| Item | PR | Evidence |
|---|---|---|
| Rankings by API key / member / calling product | #208 | SQLite + real PG (WSL) pass; mutation red→green |
| Upstream URLs a rebrand broke (7 links 404) | #209 | each URL checked by hand; gate `web/src/upstream_links.test.js` |
| **Wallet charged CNY 1 per $1 of usage (7.3× undercharge)** | #210 | see below |
| newapi.lurus.cn relay traffic cut over to hub | lurus-newapi#2, LurusTech/lurus#26 | see below |

### #210 — the money bug

Quota is priced in USD (model ratios are the upstream USD presets; prod
`deepseek-chat` 0.135 = $0.27/1M). The platform wallet is in CNY (a CNY 1
topup credits 1.0). Twelve boundary lines converted with `quota/QuotaPerUnit`,
i.e. CNY 1 for $1. Live since `BILLING_UNIFIED_ENABLED=true`; the platform
ledger had 42 `preauth_settle` rows from it, all internal root traffic.
Fix: one bridge, `currency.LucToLut() = QuotaPerUnit / USDExchangeRate`, and
every boundary through `QuotaToCNY`/`CNYToQuota`. New tests pin the debit and
pre-auth amounts (no test looked at the amount before); gate
`internal/pkg/gates/cny_quota_boundary_test.go` flags all 12 original lines on
unfixed main. Deployed digest `61f6d8e9`.

### The cutover

- Imported into hub prod (one transaction, dry-run with ROLLBACK first): user
  `lurus-internal` (no platform account ⇒ local quota only, never a wallet
  debit), the two channels that carried traffic (DeepSeek, GLM — the other six
  newapi channels were 100% failing or unreachable), 30 active tokens with
  their keys verbatim, 18 abilities; three missing model ratios copied from
  newapi (`deepseek-v4-flash`, `deepseek-v4-pro`, `deepseek-flash`).
- R1 bridge (`lurus-newapi` repo `deploy/hub-bridge.yaml`): `/v1/`, `/v1beta/`
  → hub; a key hub rejects with 401 is replayed on newapi; `/api/*` (the admin
  API platform-core still calls), the console and `/v1/audio/` (CosyVoice is on
  the tailnet) stay on newapi.
- Switched 2026-09-23 09:03 UTC. First 30 min: hub 97 ok / 1 error (a TTS call
  before `/v1/audio/` was kept on newapi); newapi relay calls after the switch
  window: 0. Rollback: IngressRoute back to `lurus-newapi:3000`.

## Found, not fixed (next)

1. **Output tokens billed at input price.** Hub has no completion-ratio rule for
   DeepSeek or GLM, so both are 1; DeepSeek's output is ~4× its input price.
   Cache hits are billed at full input price. Must be fixed from the vendors'
   official price pages before the first paying customer — the two sources on
   hand (newapi options, the upstream preset) disagree.
2. **Official ratio preset sync still fails.** #209 fixed the 404, but upstream
   changed the file to `billing_expr` (tiered price expressions); hub parses
   neither shape now. Correction posted on #209.
3. **`tenant_configs` rate limits are decorative.** `rate_limit.requests_per_minute`
   / `requests_per_day` are seeded per tenant and read nowhere; the real limits
   are `tenants.rpm_limit/tpm_limit`.
4. **platform-core still provisions users/keys on newapi** (`NEWAPI_INTERNAL_URL
   = https://newapi.lurus.cn`) and its `newapi_sync` hard-codes CNY 1 = 500,000
   quota (the same bug as #210, on the old gateway). Keys minted there after the
   import only work through the bridge's 401 fallback.
5. **GitHub Actions for every private repo has been blocked since ~2026-09-13**
   ("recent account payments have failed or your spending limit needs to be
   increased"): platform-core's CI/CD, lurus-newapi, the governance repo.
   Owner action.
6. R1 ArgoCD cannot fetch GitHub (EOF) since 2026-06-02; the newapi app has not
   synced since. The cutover was applied with kubectl and recorded in git.
7. newapi flushes perf metrics with an ambiguous column (`generation_ms`),
   failing every bucket — moot once it is archived (R-5).
8. A New API → hub importer is medium work (~1.5–3k lines): tenant placement,
   id remapping, identity linking, and upstream quota is USD like ours (after
   #210 that is a straight copy). The cutover SQL is its first real run.
