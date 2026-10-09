# UAT seed (`scripts/uat/seed.ts`)

Builds the demo world that `doc/uat/business-test-guide.md` walks through, using
only the management API (bridge session for root and for each tenant admin,
internal API key for user provisioning). No SQL, no kubectl. Run with Bun.

## What it creates

| Group | Content |
|---|---|
| Channels | `uat-faultsim` (fault simulator, needs `FAULTSIM_TOKEN`), `uat-demo-upstream` (the model that answers 200: with `FAULTSIM_TOKEN` it is mapped to the simulator success model `ok-chat`; `--upstream-base-url` points it at a real or fake upstream instead), `uat-demo-faulty` (created **disabled**; the engineer enables it to demo cooling and failover) |
| Prices | model ratio for every demo model (default 0.15 / completion 4 = USD 0.30 / 1.20 per million tokens) |
| Probe | one root-owned unlimited key, one routing call |
| Tenants | `demo-tech` (演示科技) and `demo-mfg` (传统制造): payer admin, 2 departments with `external_code`, 1 department lead, 3 employee keys (batch), 1 trusted gateway key, 1 plain key, unlimited credit pool |
| Platform | `operator` (role 100), `observer` (role 10 + `audit:read` grant) |
| Testers | `t01`..`t06` in the default tenant, each with one key of a known quota; `t06` has quota 1 for the 402 demo |
| Codes | 10 redemption codes, 500 000 units (USD 1) each |

Known values live in the `TENANTS`, `TESTERS` and `CODE_*` constants at the top
of `seed.ts`; the guide quotes them.

## Run

```bash
export E2E_BRIDGE_TOKEN=...          # UAT bridge token (never on the command line)
export UAT_INTERNAL_KEY=lurus_ik_... # scopes user:read,user:write   (or add --mint-internal-key)
export FAULTSIM_TOKEN=...            # optional; enables fault channels AND the success-mode demo upstream
export UAT_UPSTREAM_KEY=...          # optional, only with --upstream-base-url

bun scripts/uat/seed.ts --dry-run                       # prints the plan, sends nothing
bun scripts/uat/seed.ts --base-url https://test-newhub.lurus.cn \
    --upstream-base-url https://upstream.example --upstream-model <vendor model name> \
    --out ~/uat-seed
```

Options: `--base-url`, `--out` (default: system temp dir `lurus-uat-seed`),
`--dry-run`, `--mint-internal-key`, `--upstream-base-url`, `--upstream-type`
(channel type, default 1 = the generic chat-completions type), `--demo-model` (name users see, default `uat-demo-chat`), `--upstream-model` (the vendor's own model name, mapped onto the demo name), `--model-ratio`, `--completion-ratio`, `--skip-probe`, `--probe-timeout-ms`,
`--allow-private-upstream` (local stack only). Secrets passed as `--bridge-token`
and the like are refused on purpose; `--bridge-token-env NAME` (and the
`-internal-key-env`, `-faultsim-token-env`, `-upstream-key-env` forms) read a
differently named variable.

Rate limit: the bridge allows 5 logins per minute per IP. A run uses 3
(root + two tenant admins); a 429 is waited out. Do not run it while the nightly
e2e is running (19:00 UTC).

## Output (`--out`)

- `accounts.md`, `accounts.csv`: name, role, tenant, user_id, login, key name,
  key **mask**. Safe to share with testers.
- `secrets.csv`: plaintext keys and redemption codes, mode 0600. A key is shown
  by the server exactly once, so the script merges the previous `secrets.csv`
  on every run; delete that file and the plaintext of existing keys is gone
  (rotate the key in the console to get a new one).
- Nothing is written into the repository. Never commit `secrets.csv`.

## Idempotency

Every step looks first: tenants by slug, departments by name/code, keys by
name, employees by `employee_ref` (batch endpoint skips existing), users by
IdP subject (provisioning is idempotent), codes by name (tops up to 10),
grants and pricing by state. A second run prints `Created this run: {}`.
Channel status set by an engineer is never overwritten.

## Backend gaps (reported, not faked)

| Need | Reality | What the script does |
|---|---|---|
| A fault-free demo model | The fault simulator has a success mode: model `ok` / `ok-*` answers 200 with usage 1000/500 (header `X-Faultsim-Usage: "in,out"` overrides). | With `FAULTSIM_TOKEN` exported the demo model is mapped to `ok-chat`; no real upstream needed. Without the token (and without `--upstream-base-url`) the step is SKIPPED with a manual alternative. The UAT deployment must have `FAULTSIM_TOKEN` set (operator step). |
| Fault simulator on live UAT | Routes exist only when the server has `FAULTSIM_TOKEN`; the UAT deployment does not set it (see `deploy/k8s/r6-uat/README.md`). | Step SKIPPED unless `FAULTSIM_TOKEN` is exported; enabling it is an operator step. |
| True read-only role | None exists. | Observer = role 10 + `audit:read` grant, documented as an approximation. |
| Tenant admin without SQL | Exists: root `PUT /api/v2/admin/tenants/:id/members/:uid/role` (the acceptance suite uses SQL because it predates that route). | Used. |
| Employee login | Employees in the batch-key flow are labels (`employee_ref`), not users. | They have keys only; no login. |
| Plaintext of an existing key | Never returned again. | Taken from the previous `secrets.csv`, else marked "not retrieved". |

## Tests

```bash
bun test scripts/uat                                 # request orchestration against an in-memory fake API
bun build --target=bun scripts/uat/seed.ts --outfile "$TEMP/seed.js"   # compile check
```

## Against the local acceptance stack

`bash scripts/acceptance-stack.sh` with `ACCEPT_UP_ONLY=1` leaves a stack up and
prints its `ACCEPT_*` variables. Then:

```bash
export ACCEPT_BRIDGE_TOKEN=... ACCEPT_BASE_URL=... ACCEPT_FAKE_URL=...   # from the stack output
export UAT_UPSTREAM_KEY="${ACCEPT_VENDOR_KEY:-unused}"
bun scripts/uat/seed.ts --base-url "$ACCEPT_BASE_URL" --bridge-token-env ACCEPT_BRIDGE_TOKEN \
    --mint-internal-key --upstream-base-url "$ACCEPT_FAKE_URL" --allow-private-upstream \
    --out "$TEMP/uat-local"
```

The fake upstream answers 200 with 1000 prompt / 500 completion tokens, so one
call to `uat-demo-chat` at the seeded price costs round((1000x0.3 + 500x1.2) /
1e6 x 500000) = 450 units. This route is written but has not been run against a
live stack in this change; run it there first before touching UAT. The stack
enables unified billing against a fake platform, so wallet-related numbers
differ from UAT (see the guide's "能测与不能测").
