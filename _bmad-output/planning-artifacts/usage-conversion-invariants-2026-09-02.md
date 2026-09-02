# Usage conversion invariants — sweep record and remaining plan

_Date: 2026-09-02. Scope: how upstream usage figures reach settlement and the caller across protocol conversions._

## 1. The defect class

Every protocol converter and every provider handler that builds a `dto.Usage` by hand
is a place where a usage field can be dropped or mis-scoped. The three shipped fixes
share one shape: **the charge was computed from the right or wrong number depending on
which converter the request happened to pass through**, and no test compared converters
against each other.

| PR | Direction | What was wrong | Money direction |
|----|-----------|----------------|-----------------|
| #122 | OpenAI-wire → Claude-wire client | non-stream literal lacked cache fields; two stream copies | figures wrong, money right |
| #125 | Gemini upstream → any client | DTO had no `cachedContentTokenCount` at all | cache hit billed at full input price |
| #125 | OpenAI-wire → Gemini-wire client | thoughts and cache not carried | figures wrong |
| #126 | xAI upstream, non-stream | cached parsed, wire flag missing | full prompt **plus** cache price |
| #126 | xAI upstream, stream | three-field copy dropped `prompt_tokens_details` | cache hit billed at full input price |

The two wire semantics that decide the billing base (`dto.Usage.PromptTokensIncludeCached`):

- **prompt includes cached**: OpenAI chat, OpenAI Responses, Gemini (`promptTokenCount`
  "includes the number of tokens in the cached content"), xAI (docs: turn 2
  `prompt_tokens=120, cached_tokens=50`).
- **prompt excludes cached**: Anthropic wire (`input_tokens` excludes cache reads and
  creations).

Each of these was confirmed against the provider's own documentation before the flag was
stamped. Do not infer the semantics from the shape of the struct.

## 2. Locks now in place

- **Single conversion point per direction**: `claudeTerminalUsage`, `geminiUsageMetadata`,
  `buildUsageFromGeminiMetadata`. Stream and non-stream call the same function.
- **Transport parity**: same input, `reflect.DeepEqual` across stream/non-stream
  (Claude, Gemini, xAI).
- **Field census**: reflect over the target struct; every field is either carried or
  listed with a reason. A new wire field cannot ship as a silent zero.
- **Round trip**: OpenAI → Gemini → OpenAI reproduces prompt/completion/total/
  reasoning/cached (pins the two Gemini converters as inverses).
- **Metadata to money**: provider handler parses a real body, `postConsumeQuota` settles
  it, the test asserts the quota and names the pre-fix figure. The pre-fix figure is
  measured by mutation, not derived (a derived 175 turned out to be 155).
- **Whole-value assignment** in stream merges (`*usage = built`) instead of hand-copied
  field lists.
- **Billing invariance matrix** (`internal/app/relay/billing_invariance_matrix_test.go`,
  2026-09-02): one semantic event (prompt 120 with 50 cached, 30 output) through the
  production `Adaptor.DoResponse` of eight upstream wires × both transports × both
  settlement paths; every cell charges 105. A second table asserts the caller sees the
  cache hit in its own wire's field for every client format the handler serves.
- **Stream terminal shape per client wire** (`provider/openai/stream_terminal_crosswire_test.go`):
  Claude-wire clients get exactly one `message_delta` (stop_reason + billed usage) and one
  `message_stop`; Gemini-wire clients get exactly one STOP frame and it carries the billed
  `usageMetadata`. Locked over the `stream_options.include_usage` frame order the relay
  itself requests (finish chunk, then usage-only chunk).

## 2a. Found by the matrix (2026-09-02)

The 32 settlement cells were green on the first run: money is invariant. The
caller-visibility half found two streaming defects, both in the frame order the relay
requests from every channel in `streamSupportedChannels` (finish_reason chunk, then a
usage-only chunk):

| Client wire | What the caller got | Cause | Fix |
|---|---|---|---|
| Claude on OpenAI-wire upstream, stream | no `message_delta` at all: no stop_reason, no usage, no cache figures | finish chunk emitted `message_stop` before usage was known and set `Done`; the usage chunk was then discarded | terminal pair deferred until usage is known; `CloseClaudeStream` guarantees one `message_delta` + `message_stop` at end of stream |
| Gemini on OpenAI-wire upstream, stream | one STOP frame with the pre-request estimate, real counts never | usage-only chunk (no content, no finish) dropped as a "leading empty frame" | usage-only chunk is a frame; content-less finish chunk held so the terminal frame is the single STOP with billed `usageMetadata` (vendor remaps included) |

Both are "figures wrong, money right". A third, not fixed here: the **prompt figure
itself changes semantics across wires**. An OpenAI-wire caller on an Anthropic upstream
sees `prompt_tokens` = Anthropic `input_tokens` (cache excluded), and a Claude-wire caller
on an OpenAI upstream sees `input_tokens` = OpenAI `prompt_tokens` (cache included).
Each wire's own arithmetic then over- or under-counts by the cached slice. See §4.1.

## 3. Not changed, with reasons

- `baidu`, `dify`, `zhipu_4v`, `openai/audio` parse an OpenAI-shaped `Usage` without the
  flag. Those wires expose no cached-token field today, so `CachedTokens` is always 0 and
  the flag is inert. Stamping it would encode a semantic nobody has verified. Revisit
  the day one of them starts emitting `prompt_tokens_details.cached_tokens`.
- Claude 5m/1h cache-creation split on the OpenAI → Claude direction: no reachable
  upstream fills it; listed as unsourced in the census.
- `IMAGE` modality in Gemini `promptTokensDetails`: upstream New API maps it to
  `ImageTokens`, but our settlement subtracts `ImageTokens` from the base and prices them
  at an image ratio, so mapping it would move money for Gemini image inputs without a
  verified ratio. Left unmapped.

## 4. Remaining plan (in value order)

1. **Prompt-figure semantics at the wire boundary** (found by the matrix, §2a) —
   **implemented 2026-09-02.** When the usage crosses wires, present the prompt in the
   caller's semantics: Claude-wire `input_tokens` = prompt − cached − cache_creation when
   the source flag says the prompt includes them (`dto.Usage.AnthropicInputTokens`);
   OpenAI-wire `prompt_tokens` = input + cache_read + cache_creation, `total_tokens`
   recomputed, when it does not (`dto.Usage.AsOpenAIWire`). Display copy only: the
   settlement struct keeps its source semantics. Touches `claudeTerminalUsage`, the Claude
   handler's two OpenAI-format usage writes, and the #122 lock that pinned
   `input_tokens` = 3127 (it pinned "fields carried", not the semantics; it now sets the
   flag and expects 14, with an unflagged sibling that stays 3127). Third matrix table
   `TestBillingInvariance_CallerSeesThePromptInItsOwnWireSemantics`: same event, every
   row × client format × transport, the prompt figure in that wire's semantics plus the
   SDK arithmetic of that wire reproducing the 105 charge, plus the negative assertion
   that the source-semantics figure is absent.
   Notes from the implementation:
   - **Streaming needed two more sites.** The Claude-wire terminal `message_delta` is
     built from the chunk's re-parsed usage when a vendor inlines usage in its finish
     chunk (last frame, or followed by another frame); re-parsing loses the wire flag
     and the vendor cache remaps, so `provider/openai/helper.go` now runs
     `applyUsagePostProcessing` at both re-parse sites (`handleClaudeFormat`,
     `HandleFinalResponse` Claude branch). The standard usage-only last chunk was
     already fine (closed from the billed usage). The Moonshot row deliberately does not
     run the Claude format in the matrix (its adaptor routes Claude-wire clients to the
     Anthropic handlers, a different upstream body); the inline-usage shapes are locked
     directly in `stream_terminal_crosswire_test.go`.
   - **Direction asymmetry is deliberate.** The flag defaults to false (Anthropic
     semantics). The subtraction degrades to the old behaviour when the flag is missing;
     the addition would double-count, so `AsOpenAIWire` is called only where the source
     wire is fixed by construction (`provider/claude`). `geminiUsageMetadata` is not
     flag-keyed: every producer reaching it is includes-cached and no Anthropic-wire
     upstream routes to a Gemini-wire client.
   - **Consume log stays on source semantics** (`wire_log_parity_test.go`): on a
     cross-wire call the log's `prompt_tokens` is the upstream figure and the body's is
     the client-wire figure; the difference is exactly the cached slice. This is by
     design, not a reconciliation defect, and needs a coordination changelog entry (root
     repo, owner) stating that OpenAI-wire callers on Anthropic/aws/vertex channels see
     `prompt_tokens`/`total_tokens` rise by cache_read + cache_creation from deploy day
     (the v2 chat / playground display fields parse that body too).
   - **Known limitations.** `message_start` on the Claude wire still carries the
     pre-request estimate and no cache fields (the real counts are not known yet; the
     official SDKs overwrite from `message_delta`, so the final message is right).
     Inbound `message_delta` cache counters are int, so 0 and absent are
     indistinguishable; a present (>0) value overwrites the `message_start` figure, an
     absent one keeps it (SDK accumulator rule). OpenAI's wire has no standard field for
     cache creation, so it is folded into `prompt_tokens` at the undiscounted rate — an
     OpenAI SDK's estimate of the creation term is 0.25× below the relay's 1.25× charge;
     the non-standard `claude_cache_creation_5_m/1_h_tokens` keys remain for callers
     that want to reconstruct it. OpenAI-wire `cache_write_tokens` (chat and Responses)
     is parsed, billed and shown since 2026-09-02 — see §4.4.
2. **Live proof on a real Gemini or xAI channel** — the fixes are unit- and
   mutation-verified only. First real channel: send the same prompt twice, expect
   `cached_tokens > 0` on the second reply and a log `cache_tokens` that reconciles with
   the charge. Owner-gated on a key.
3. **Operator-facing cache ratio audit** — the ratio map now seeds Gemini 2.5/3 and the
   OpenAI/DeepSeek/Claude families; xAI Grok names in the model table are stale
   (`grok-3-beta` era) and carry no cache ratio. Refresh model names and ratios from the
   provider price lists in one pass, with the source URL in the comment as done for
   Gemini.

### 4.3 addendum — Grok price audit (2026-09-02, read-only research; owner decision needed)

Source: https://docs.x.ai/docs/models (fetched twice, consistent). The caching guide and
legacy-models pages returned 404, so the usage field name for cached tokens and the
retirement status of old ids could **not** be confirmed from a primary source.

- Repo: `ratio_setting/model_ratio.go` carries eight `grok-*` entries (`grok-3-beta`,
  `grok-3-mini-beta`, `grok-3-fast-beta`, `grok-3-mini-fast-beta`, `grok-2`,
  `grok-2-vision`, `grok-beta`, `grok-vision-beta`); **none** is on the current models
  page. `defaultCacheRatio` has **no** grok entry, so every xAI cache read is priced at
  ratio 1 (full input price) today.
- Current text models and the cached/input ratio the page implies (USD per 1M tokens):
  `grok-4.6` 2.00 / 0.50 → 0.25 · `grok-4.5` 2.00 / 0.30 → 0.15 · `grok-4.3` 1.25 / 0.20 →
  0.16 · `grok-4.20-0309-reasoning`, `-non-reasoning`, `grok-4.20-multi-agent-0309`
  1.25 / 0.20 → 0.16 · `grok-build-0.1` 1.00 / 0.20 → 0.20.
- Blocker for seeding: the page bills the **whole request** at a higher tier once the
  prompt reaches a listed threshold (≥200k context, input and cached ×2). The ratio table
  is one value per model, so the tier cannot be expressed; the owner must choose the
  lower tier (under-bills long prompts) or the higher (over-bills short ones) before any
  grok entry is seeded. No repo change was made.

### 4.4 Cache writes on the OpenAI wire + Gemini tool-use completion (2026-09-02, implemented)

Two follow-ups from §4.1, both re-verified against HEAD and both money-relevant.

**Gemini completion figure.** `buildUsageFromGeminiMetadata` folds
`toolUsePromptTokenCount` into `PromptTokens` (it is billed as input) and computes
`CompletionTokens = candidates + thoughts`. Both handlers then overwrote that with
`TotalTokens − PromptTokens`. The official definition is `totalTokenCount = prompt +
thoughts + response candidates` (ai.google.dev/api/generate-content, read 2026-09-02);
the tool-use prompt is *beside* it, not inside. So a grounded call was under-counted by
the tool-use prompt and went **negative** once that exceeded the answer; the
non-streaming handler handed the negative figure to settlement as a credit, the streaming
handler hid it behind its text-length estimate. Fix: `geminiCompletionTokens` keeps the
builder figure; the subtraction survives only as a fallback for an upstream that reports
a total but no candidate count, floored at zero. Locks: `tool_use_completion_test.go`
(non-stream, stream, fallback/floor table).

**`cache_write_tokens`.** Present on both OpenAI usage shapes in the official OpenAPI
spec (chat `prompt_tokens_details.cache_write_tokens`: "the unadjusted number of prompt
tokens written to cache"; Responses `input_tokens_details.cache_write_tokens`, required
alongside `cached_tokens`). Disjoint from `cached_tokens`, inside `prompt_tokens` (the
org-usage example on the same spec: 500 uncached + 400 cached + 100 written = 1000). The
vendor prices writes at **1.25× the uncached input rate for GPT-5.6 and later** and adds
no write charge on earlier models (developers.openai.com prompt-caching guide, read
2026-09-02). Until now the Go field was `json:"-"`: not parsed, billed as plain input,
invisible to callers on any wire.

Changes:
- `dto.InputTokenDetails.CachedCreationTokens` is `json:"cache_write_tokens,omitempty"`:
  chat-wire parse is automatic, OpenAI-wire callers on Anthropic upstreams now also see
  the write count under the spec's key (zero stays off the wire). Responses parse copies
  it explicitly on both transports.
- **Ratio by wire.** The map default behind `CacheCreationRatio` (1.25) is Anthropic's
  universal surcharge and must keep applying to any unlisted Claude name. Mapping OpenAI
  writes onto the same field would have billed every older GPT model's writes at 1.25×.
  `types.PriceData.CacheCreationRatioDefaulted` (set only by `ModelPriceHelper` when the
  model has no `defaultCreateCacheRatio` entry) and `CacheCreationRatioForWire(flag)`:
  on the OpenAI/Gemini wire (`PromptTokensIncludeCached`) an unlisted model bills the
  write at 1; a listed one (gpt-5.6, -sol, -terra, -luna seeded at 1.25) or the Anthropic
  wire keeps the configured ratio. An explicitly set ratio is always honoured. Applied in
  all three places that price creation tokens (`postConsumeQuota`,
  `PostClaudeConsumeQuota`, `EstimateQuotaFromUsage`); the OpenRouter cost inference
  keeps the ratio it solved against.
- Claude-wire callers on OpenAI upstreams now get `cache_creation_input_tokens` and an
  `input_tokens` net of both cache terms (`AnthropicInputTokens` already subtracted
  creation; it was always zero before).

Locks: `dto/usage_cache_write_wire_test.go` (round trip, zero omitted),
`provider/openai/cache_write_tokens_test.go` (chat non-stream/stream × OpenAI/Claude
callers, Responses non-stream/stream), `relay/billing_cache_write_test.go` (105 / 110 /
110 across both settlement paths and the estimate), `helper/price_cache_write_defaulted_test.go`,
`types/price_data_cache_write_test.go`, `app/quota_openrouter_inference_ratio_test.go`.

Not done / owner: model ratios for the gpt-5.6 family are not seeded (pricing page not
reachable from here; only the write ratio, which the guide states, is). Live proof on a
GPT-5.6 key remains owner-gated. Coordination changelog (root repo): OpenAI-wire callers
on Anthropic channels now see `prompt_tokens_details.cache_write_tokens` when a write
occurred.
