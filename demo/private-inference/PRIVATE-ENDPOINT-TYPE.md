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

## The refusal is on the record

A fail-closed guard that leaves no trace is unauditable: an empty log is
indistinguishable from a guard that never ran. Every refused dispatch now writes
one `security.egress_blocked` audit event.

| Field | Value |
|---|---|
| `action` | `security.egress_blocked` |
| `tenant_id` | the tenant whose relay token made the call, from the auth context |
| `resource` / `resource_id` | `channel` / the offending channel id |
| actor | `token`, plus the calling user id |
| `details` | `attempted_host`, `reason`, `channel_type`, `model`, `token_id`, `request_sent: false` |

Three design points worth keeping:

1. **Recorded once, at one funnel.** All three dispatch entry points
   (`DoApiRequest`, `DoFormRequest`, `DoWssRequest`) resolve their upstream URL
   through a single `resolveRequestURL` helper in
   `internal/adapter/provider/api_request.go`, so a refusal is recorded in one
   place whatever the transport — instead of three copies of the same audit call
   drifting apart.
2. **The host, not the base URL.** A base URL may embed credentials
   (`http://user:pass@host`), and the host is the whole of the security-relevant
   fact. A test asserts an embedded password never reaches the audit details.
3. **Narrow on purpose.** Only a public-target refusal qualifies
   (`*privateendpoint.BlockedError`, recovered with `errors.As`). A malformed
   base URL is a configuration typo, not an attempted egress; filing it under a
   security action would dilute the trail into uselessness.

The event is linked into the existing per-tenant tamper-evidence hash chain
(`audit_events.row_hash`). The demo asserts `row_hash <> ''`: a security event
stored outside the chain is weaker evidence than one inside it, and the chain
writer is deliberately fail-open, so "it went in unchained" is a real outcome
worth failing on rather than assuming away.

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

## Proving it against a real model — the second command

The proof above has a structural weakness that is not a bug: its "on-prem
endpoint" is a mock written by the same hand as the guard it exercises.
Same-source evidence can demonstrate plumbing, never the product claim. A canned
responder would satisfy every assertion in that script while proving nothing
about whether a model can actually serve the tenant.

```bash
bash demo/private-inference/verify-real-engine.sh
```

This second script routes the same tenant, through the same type-57 channel
machinery, to an OpenAI-compatible inference server already running on the
operator's own machine with real quantized weights on local disk. It refuses to
trust any endpoint it has not first proven is a real model:

| Anti-mock test | Why a canned responder fails it |
|---|---|
| Two different prompts must yield two **different** answers | A mock returns one constant string |
| Each answer must be **independently correct** (an arithmetic question and a geography question) | A mock cannot answer either |
| `prompt_tokens` must **track the input** | A mock reports a fixed count |

Discovery and validation are fused: the script sweeps loopback ports and only
accepts one that passes all three. That is how the demo mock on `:11434` is
auto-rejected rather than silently accepted.

That auto-rejection is itself verified, not assumed — a detector never observed
rejecting anything is not a detector:

```
$ PRIVATE_ENGINE_PORTS=11434 bash demo/private-inference/verify-real-engine.sh
== 1/6 locate a real inference engine and prove it is not a canned responder
   probing http://127.0.0.1:11434 ...
     rejected: identical answer to two different prompts (canned responder)
FAIL no real inference engine found on: http://127.0.0.1:11434
$ echo $?
1
```

Two further assertions become possible once a real engine is present, and
neither has any meaning with a mock:

- **The engine binds loopback only and holds zero non-loopback outbound TCP
  connections** — so it is serving weights from this disk, not quietly proxying
  to a remote provider. Without this, "the request reached my endpoint" says
  nothing about where inference actually happened.
- **The egress canary stays refused with a real model sitting next to it** in
  the same tenant, so the guard is not merely rejecting everything.

Measured this session (8B-class open-weights chat model, ~4.7 GB quantized, CPU):

```
Q: What is 17 multiplied by 23? Reply with the number only.
A: 391                                       (prompt_tokens=26)
Q: Name the capital city of Japan. One word only.
A: Tokyo                                     (prompt_tokens=22)

stream  → 4 SSE frames, reassembled answer 391, terminated with [DONE]

gateway PID 44632 — zero non-loopback outbound TCP
engine  PID  2076 — zero non-loopback outbound TCP, bound 127.0.0.1:11400 only
canary  → HTTP 500 "blocked before dispatch, no request was sent"
canary, streamed → HTTP 500, and NOT ONE SSE frame emitted first
audit   → security.egress_blocked, tenant privacy-strict: 0 -> 2 events,
          details {"attempted_host":"...","request_sent":false,...},
          row_hash set (inside the tamper-evidence chain)
console → verdict all_traffic_stays_on_prem, real engine listed as intranet
```

### Why the streaming canary is the assertion that matters

Streaming is where a "check then dispatch" guard most plausibly degrades into
"dispatch then check": open the upstream connection, start relaying, and only
then notice the target was public. In that shape the prompt is already on the
wire when the 500 arrives, and a refusal is indistinguishable from a leak from
the client's point of view. So the assertion is not merely *refused* — it is
**refused before a single SSE frame was emitted**.

### Why both scripts are kept

They answer different questions and neither subsumes the other:

| Script | Question | Needs weights? | CI-able |
|---|---|---|---|
| `verify-private-endpoint-type.sh` | Does the **guard** work? | no | yes |
| `verify-real-engine.sh` | Does the **product claim** hold? | yes | no |

The mock-based script stays deterministic and runnable on a build agent with no
model on disk. The real-engine script cannot run there, and buying CI-ability by
deleting it would trade the only same-source-free evidence for convenience.

The tenant-facing model name is an **alias** (`onprem-chat-8b`), decoupled from
whichever upstream build is installed, so the customer's model catalogue does
not churn when the operator swaps the underlying weights. Override discovery
with `PRIVATE_ENGINE_URL` / `PRIVATE_ENGINE_MODEL` / `PRIVATE_ENGINE_PORTS`.

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
| `internal/pkg/privateendpoint/guard.go` (again) | `BlockedError` carries the Verdict so callers can audit *why*, not just *that* | edited |
| `internal/app/governance/audit_action.go` | `security.egress_blocked` action + registry entry | edited (2 hunks) |
| `internal/adapter/provider/api_request.go` | `resolveRequestURL` funnel + `recordDispatchBlocked` | edited (4 hunks) |
| `internal/adapter/provider/egress_audit_test.go` | Tenant attribution, credential redaction, narrowness, nil-ChannelMeta survival | new |
| `internal/adapter/handler/v2_private_routing.go` | `GET /api/v2/:tenant/private-routing` | new |
| `internal/adapter/handler/v2_private_routing_test.go` | Verdict derivation (incl. "blocked ≠ on-prem") | new |
| `internal/adapter/handler/router/api-v2-router.go` | Route registration | edited (1 hunk) |
| `web/src/constants/channel.constants.js` | Console label for type 57 | edited (1 hunk) |
| `demo/private-inference/seed-private-endpoint-type.sql` | Tenant + intranet channel + egress canary | new |
| `demo/private-inference/seed-strict-console-viewer.sql` | role=10 viewer for the admin-gated status API | new |
| `demo/private-inference/shot-private-routing.mjs` | Console panel screenshot (run with `node`, not `bun`) | new |
| `demo/private-inference/verify-private-endpoint-type.sh` | The one-command proof, mock endpoint (guard-level, CI-able) | new |
| `demo/private-inference/seed-real-engine.sql` | Points the strict tenant at a real local engine; `-v engine_base_url` / `-v engine_model` | new |
| `demo/private-inference/verify-real-engine.sh` | The one-command proof against a real model (product-level, needs weights) | new |

## What this does not cover

- **Private RAG / vector-store recall** — a separate egress surface, untouched.
- **Non-chat routes** (embeddings, images, websocket) are covered by
  construction, not by a live run. All three dispatch entry points —
  `DoApiRequest`, `DoFormRequest`, `DoWssRequest` in
  `internal/adapter/provider/api_request.go` — resolve the URL through
  `GetRequestURL` *before* building the request, so a refusal there stops every
  one of them. Streaming used to be listed here too; it now has its own live
  proof (see below), because "by construction" is worth spending one run to
  check.
- **Model-name mapping / pricing** for private endpoints uses the standard
  channel machinery; nothing bespoke was added.
- No TLS/mTLS requirement is imposed on the private endpoint. Plain `http://`
  to an intranet host is accepted deliberately (self-hosted inference servers
  commonly run without certificates), so confidentiality *inside* the network is
  the customer's own segmentation problem, not something this type asserts.
- **The real-engine run is single-host.** The gateway and the engine sit on the
  same machine, on loopback. A customer deployment puts the engine on a separate
  intranet host; the classifier accepts those RFC1918/ULA/tailnet shapes and is
  unit-tested on them, but there is no live cross-host run in this repo.
- **Model quality is not asserted.** The two checkable answers are an anti-mock
  oracle, not a benchmark — they establish that a model reasoned over the prompt,
  and say nothing about whether it is good enough for a given workload.
- **The inference engine is operator-supplied.** Nothing here installs, pins or
  manages it; the demo only proves the gateway routes to whatever is running and
  refuses everything that is not on-prem.
- **Zero-egress is sampled, not captured.** The assertions read the OS TCP table
  at defined points, so a connection opened and closed entirely between two
  samples would not appear. The dispatch-time guard is what makes the claim
  structural; the connection-table check is corroboration, not the proof.
