# ADR: Decision-model routing (tenant policy, opt-in)

**Status**: Accepted (implemented behind per-tenant policy rows, default empty) · **Date**: 2026-10-10
**Relates to**: `doc/decisions/2026-05-09-cost-aware-routing.md` (the prohibited zone this ADR stays inside); migration `054_routing_policies.sql`; `doc/runbook/systemone-channels.md`

## Context

Tenants asked for "send simple questions to the cheap model, hard ones to the strong model" without changing their client code. The 2026-05-09 ADR already ruled on auto-routing: it is **permanent opt-in**, **always announced** (`X-Routed-Model`, `X-Routing-Reason`), and **never** applied to mid-conversation traffic, tool use, `audit:true` / `pin:<model>` requests or reasoning models. A System One style decision model (one typed `choice` question in, a probability distribution out) is a cheap, auditable way to make the call, because the answer is a number we can threshold, log and bill, not free text we would have to trust.

## Decision

### 1. The opt-in carrier is a tenant policy row

Nothing routes unless the tenant admin has created and **enabled** a `routing_policies` row for `(tenant, public model)`. A policy is born `enabled=false`. No row, or a disabled row, means the request path is byte-identical to today: one in-memory index lookup (15 s TTL, whole enabled set cached, zero database access per request), no header, no metric, no body parsing.

A policy names: the evaluator model, 1-32 candidates `{id, model, criteria}` (criteria <= 2048 B, each candidate model must be a public model the tenant can route today), free-text instructions (<= 4096 B), `min_confidence` (0-1, default 0.65) and a `default_candidate`. Validation is all-or-nothing. If `default_candidate` is empty the fallback is the requested model, which must then be a candidate.

### 2. The evaluator is provider-agnostic

The evaluator is "any model the tenant can reach through a System One channel" (hosted TypeSafe or a self-hosted compatible server). It is selected with the same tenant-scoped, weighted, cooldown-aware selector as any request, but with a **scratch request context**: the caller's per-request selection state (modality, provider filter, already-tried channels, affinity) must neither constrain nor be mutated by the evaluator's own routing. The call goes straight through the System One adaptor, not through the relay handler, so it can never recurse into decision routing and has its own timeout and billing line.

The question is a single `choice`: criteria = each candidate's criteria plus an always-present `no_preference` option; instructions = the policy's instructions. The input is the **last plain-text user message only**, truncated to 8192 bytes on a rune boundary. System prompts, earlier turns and attachments are never sent.

### 3. Failure falls back to the default candidate, never to an error

Timeout (`ROUTING_DECISION_TIMEOUT_MS`, default 1000), no channel, upstream error, an unparsable or unknown answer, an evaluator panic, or the process-wide concurrency cap (8 in flight, non-blocking) all end in `evaluator_unavailable` and the default route. The evaluator is never retried and never queued: routing is an optimisation in front of the relay, and a slow or broken optimiser must cost the caller nothing.

A pick is applied only when the chosen option's probability is `>= min_confidence` (`applied`); otherwise `low_confidence` goes to the default. `no_preference` also goes to the default.

### 4. Where it must not run (reason `ineligible`)

Exactly the 2026-05-09 anti-rules, made mechanical: not a first turn (any assistant/tool/function history or `tool_calls`), any `tools` / `tool_choice` / legacy `functions`, `previous_response_id`, any session-affinity key (`X-Session-Id`, `prompt_cache_key`, `metadata.user_id`), `reasoning_effort` / a `reasoning` object / a reasoning-model name, `metadata` containing `pin:` or `audit:true`, and a last user message that is not plain text. Only OpenAI chat completions and Responses are considered; every other wire format is skipped silently. If narrowing candidates (below) leaves one, the request goes there without an evaluation (`single_candidate`).

Candidates the caller could not have asked for directly are removed before deciding: the token's model limit, the platform allow-list (enforce mode) and the tenant's own selection are re-applied to every rewrite target. A rewrite can never reach a model the caller was forbidden.

### 5. Transparency, audit, billing

- Every request that matched a policy carries `X-Routed-Model: <final model>` and `X-Routing-Reason: decision:<applied|low_confidence|no_preference|evaluator_unavailable|ineligible|single_candidate>`. They appear **only** when a policy matched.
- Metrics: `lurus_routing_decision_total{reason}` (closed enum, zero-initialised) and `lurus_routing_decision_latency_seconds`.
- Audit `routing.decision` is written for every outcome that involved the evaluator: reason, requested and routed model, candidate id, probability distribution, confidence, evaluator token usage, latency and a coarse error kind. **It never contains user text**, nor the evaluator's error message. `ineligible` / `single_candidate` are header + metric only: they are the bulk of an opted-in tenant's traffic (every tool turn) and carry nothing the counter does not. Policy writes audit as `routing.policy_set` / `routing.policy_deleted` (shape of the change, not the instructions text).
- Billing: the evaluation is settled as its **own consume row** (`model = evaluator_model`, `other.routing_eval = true`, same `request_id`), priced at the evaluator's own price for the caller's group, input tokens only (as System One is priced on the relay path). An unpriced evaluator costs 0 and still writes the row; routing is not refused over a billing gap. The routed request itself is billed as usual at the routed model's price.

## Consequences

(+) tenants get cost routing that is opt-in, announced, audited and bounded in latency; failure modes degrade to today's behaviour; the evaluator is swappable (hosted or self-hosted) with no code change; the closed state costs one map lookup.
(-) every evaluated request pays up to the timeout in the worst case (1 s default) and an extra upstream call; rewrites change the model a client sees in the response (announced in the headers); policy changes converge on other replicas within the 15 s index TTL.

## Out of scope / follow-ups

- Response validation of the System One reply (probability sum, legend consistency) belongs to the contract-robustness lane; the evaluator treats any unusable reply as `evaluator_unavailable`.
- The evaluator call does not feed the channel circuit breaker or auto-disable logic; a persistently failing evaluator channel shows up as `evaluator_unavailable` volume.
- Platform-wallet (unified billing) settlement of the evaluation row follows the local-ledger path only; see the lane notes.
- Tenant erasure does not currently cascade to `routing_policies` (there is no tenant-level erasure cascade in the repo yet).
