# Private inference as a first-class provider type

The demo next to this file (`README.md`) already proved that a tenant's LLM
traffic can be pinned to an on-prem endpoint using the generic
`ChannelTypeCustom = 8` escape hatch. This document covers what changed when
that capability became a **provider type of its own** —
`ChannelTypePrivateEndpoint = 57` — and why the difference matters commercially.

## What was wrong with "just use Custom"

`Custom` is a base-URL passthrough. Nothing about it says "the data stays
here". A channel typed `Custom` with `base_url = http://10.0.0.5:8000` and one
with `base_url = https://api.openai.com` are the same kind of object to the
gateway; only the string differs. So "your data never leaves your network" was
a **deployment convention**: true as long as nobody edited the URL, restored an
old backup, or ran a seed script that pointed somewhere else.

A convention cannot be demonstrated. A mechanism can.

## What type 57 is

Type 57 is not a vendor — it is a posture: *the customer's own inference server,
speaking the standard OpenAI-compatible API*. The type carries a contract the
code enforces:

> A channel of this type may only dispatch to an address inside the customer's
> network. Any publicly routable target is refused, and the request is never
> emitted.

Enforcement lives in `internal/pkg/privateendpoint` and runs at **two** points:

| Point | Where | Why both |
|---|---|---|
| Config time | `handler.validateChannel` (`internal/adapter/handler/channel.go`) | An operator who mistypes a URL gets an actionable rejection instead of silently shipping prompts to a SaaS provider. |
| Dispatch time | `openai.Adaptor.GetRequestURL` (`internal/adapter/provider/openai/adaptor.go`) | Config validation is bypassable — a seed script, a DBA, a restored backup or a migration writes the row directly. The dispatch check is on the path **every** request must traverse, and it fails closed: no URL, no connection, no prompt. |

That second row is the load-bearing one. It is also what the demo's *egress
canary* exists to prove (below).

### Address classification

`privateendpoint.ClassifyBaseURL` returns a verdict plus a human-readable
reason. Intranet by construction:

- loopback (`127.0.0.0/8`, `::1`) and `localhost`
- RFC1918 (`10/8`, `172.16/12`, `192.168/16`) and IPv6 ULA (`fc00::/7`)
- RFC6598 shared space (`100.64/10`) — overlay-VPN / tailnet peers
- link-local (`169.254/16`, `fe80::/10`)
- non-routable DNS suffixes: `.local`, `.internal`, `.intranet`, `.corp`,
  `.lan`, `.home.arpa`, `.svc`, `.svc.cluster.local`, `.cluster.local`
- single-label hostnames (no dot — resolvable only by an intranet resolver)

Everything else is refused, including `0.0.0.0`/`::` (not a reachable endpoint;
accepting it would let a typo masquerade as a working private channel).

**No DNS resolution is performed.** A name-based verdict that depended on what
a resolver answered at validation time could be flipped by a hostile or merely
flaky DNS reply. Names are judged structurally.

This is deliberately the *mirror image* of an SSRF guard — same address
classification, inverted allow-list. Do not unify the two predicates: an SSRF
guard must reject intranet targets, this must reject public ones, and a shared
"is private?" helper would eventually be used with the wrong polarity.

### Escape hatch for corporate DNS

An intranet inference host reachable only over VPN may still have a
publicly-shaped name (`inference.dc.example.com`). Set:

```
PRIVATE_ENDPOINT_EXTRA_HOSTS=inference.dc.example.com,other.internal.example.com
```

Exact hostnames, comma separated — **not** patterns. A wildcard would quietly
re-open the hole. The console attributes a pass to the allow-list explicitly
(`reason: explicitly allowed via PRIVATE_ENDPOINT_EXTRA_HOSTS`) so an auditor
can see which channels rely on operator assertion rather than address maths.

## Console: one badge, one truth source

`GET /api/v2/:tenant_slug/private-routing` (admin-gated, same as the channel
list it summarises) answers the buyer's question directly:

```json
{
  "tenant": "privacy-strict",
  "verdict": "all_traffic_stays_on_prem",
  "enforced_by_code": true,
  "private_endpoint_channels": [ { "id": 2, "base_url": "http://127.0.0.1:11434",
      "intranet": true, "reason": "loopback address (never leaves the host)",
      "will_be_blocked": false }, ... ],
  "external_channels": [],
  "blocked_channels": [ { "id": 3, "base_url": "https://api.openai.com",
      "intranet": false, "will_be_blocked": true, "reason": "..." } ]
}
```

Verdicts: `all_traffic_stays_on_prem` · `mixed_private_and_external` ·
`no_private_endpoint_configured`.

Two design points worth keeping:

1. **The console calls the same classifier the guard enforces with.** The older
   `shot-console.mjs` re-implemented the test as a front-end regex
   (`127.0.0.1|localhost|10.|172.|192.168.`) which accepts `172.32.x` (outside
   RFC1918) and misses `.svc` / tailnet / IPv6-ULA. A green badge that
   disagrees with runtime behaviour is worse than no badge.
2. **A blocked channel does not count as "on-prem".** A tenant whose only
   private endpoint is misconfigured reports `no_private_endpoint_configured`,
   not a green badge over a channel that 500s every request. Blocked rows are
   *shown*, not hidden, so the misconfiguration is fixable rather than
   mysterious.

The channel type also appears in the existing console channel list and
create-channel dropdown as **私有推理端点（内网自托管）** (green), via
`web/src/constants/channel.constants.js`. It intentionally has **no**
`CHANNEL_PRESETS` entry: a preset's `tip` is an i18n key, the locale files were
being edited by another session, and there is no correct default `base_url` for
a customer's own host anyway.

## Proving it — one command

```bash
bash demo/private-inference/verify-private-endpoint-type.sh
# stop when done:
bash demo/private-inference/verify-private-endpoint-type.sh stop
```

Six steps, each of which fails the script if it does not hold:

1. demo Postgres up (`newhub-pgdemo`, :5435)
2. backend restarted from source on :8099 (so the build under test is the one serving)
3. seed applied — `privacy-strict` tenant, a role=1 (non-admin) user, a relay
   token, and **two** type-57 channels: one intranet, one egress canary
4. mock on-prem endpoint bound to **127.0.0.1 only** on :11434 — a request that
   arrives there provably did not leave the host
5. the two relay calls:
   - **forward**: `qwen2.5-7b-instruct-strict` → HTTP 200 **and** the mock's hit
     counter increments, with the prompt visible in the mock log
   - **negative**: `egress-canary-model` → refused, error contains
     `no request was sent`, mock hit counter **unchanged**
   - **no egress**: the backend process holds zero non-loopback outbound TCP
     connections (checked via `netstat -ano` filtered to its PID)
6. console: verdict `all_traffic_stays_on_prem` and the canary flagged
   `will_be_blocked`; optional Playwright screenshot of the panel

### The egress canary

`seed-private-endpoint-type.sql` inserts a type-57 channel whose `base_url` is
`https://api.openai.com` **directly into the database**, precisely because that
path bypasses `handler.validateChannel`. It is how a bad row actually arrives in
production. If that channel ever returns a completion, the whole claim is false
and the script fails loudly. Verified this session: refused with HTTP 500,
`private-endpoint channel 3 blocked before dispatch, no request was sent`, and
the mock hit counter did not move.

## Files

| File | Role | New? |
|---|---|---|
| `internal/pkg/privateendpoint/guard.go` | Address classifier + fail-closed validator | new |
| `internal/pkg/privateendpoint/guard_test.go` | 10 real SaaS endpoints must be rejected; 16 intranet shapes accepted | new |
| `internal/pkg/constant/channel.go` | `ChannelTypePrivateEndpoint = 57`, name, empty default base URL | edited (3 hunks) |
| `internal/pkg/common/api_type.go` | Explicit map to `APITypeOpenAI` (so `ok=true` — an implicit fallback would exclude the type from the model catalogue) | edited (1 hunk) |
| `internal/adapter/handler/channel.go` | Config-time guard, beside the existing VertexAI special case | edited (2 hunks) |
| `internal/adapter/provider/openai/adaptor.go` | Dispatch-time fail-closed guard + standard OpenAI path | edited (2 hunks) |
| `internal/adapter/provider/openai/adaptor_private_endpoint_test.go` | Dispatch guard + first-class-registration tests | new |
| `internal/adapter/handler/v2_private_routing.go` | `GET /api/v2/:tenant/private-routing` | new |
| `internal/adapter/handler/v2_private_routing_test.go` | Verdict derivation (incl. "blocked ≠ on-prem") | new |
| `internal/adapter/handler/router/api-v2-router.go` | Route registration | edited (1 hunk) |
| `web/src/constants/channel.constants.js` | Console label for type 57 | edited (1 hunk) |
| `demo/private-inference/seed-private-endpoint-type.sql` | Tenant + intranet channel + egress canary | new |
| `demo/private-inference/seed-strict-console-viewer.sql` | role=10 viewer for the admin-gated status API | new |
| `demo/private-inference/shot-private-routing.mjs` | Console panel screenshot (run with `node`, not `bun`) | new |
| `demo/private-inference/verify-private-endpoint-type.sh` | The one-command proof | new |

## What this does not cover

- **Private RAG / vector-store recall** — a separate egress surface, untouched.
- **Streaming and non-chat routes** were not individually exercised in this
  session; the guard sits in `GetRequestURL`, which every OpenAI-adaptor route
  traverses, so it applies by construction, but only `/v1/chat/completions` has
  a live end-to-end proof here.
- **Model-name mapping / pricing** for private endpoints uses the standard
  channel machinery; nothing bespoke was added.
- No TLS/mTLS requirement is imposed on the private endpoint. Plain `http://`
  to an intranet host is accepted deliberately (self-hosted inference servers
  commonly run without certificates), so confidentiality *inside* the network is
  the customer's own segmentation problem, not something this type asserts.
