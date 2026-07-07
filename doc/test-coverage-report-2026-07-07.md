# Test Coverage — per-package targets & documented hermetic exceptions (2026-07-07)

Target agreed with owner: **each package ≥90% statement coverage where hermetically
feasible; packages whose remaining code is live-I/O-only are documented exceptions
at their realistic ceiling.** No network is mocked to inflate a number, no hollow
tautological tests, no new dependencies — the uncovered remainder in every exception
below is code that only executes against a real upstream (HTTP to a vendor API, a
live DB/PG, Meilisearch, an OTLP collector, or wall-clock task polling).

Measured with `go test -short` (the hermetic tier; the CI `pg-integration` job covers
the PG-gated paths separately, so packages like `repo`, `openrouter_pool`, `migration`
read higher there than under `-short`).

## Whole-module total

- Before this campaign: **51.8%** (`-short`, `coverpkg=./...`).
- After: **69.1%** own-package merged (`go test -short -coverprofile ./...`), 0 failures.
  Note the two numbers use different metrics: the 51.8% baseline is `coverpkg=./...`
  (credits cross-package hits — e.g. an `app` test covering `common`); the 69.1% is
  own-package merged (each package's own tests vs its own statements), which is a
  **conservative lower bound** — the coverpkg-equivalent after would be higher. The
  full `coverpkg=./...` re-measure was OOM-killed twice on this box, so the
  conservative merged figure is reported instead. The headline result is per-package
  (below), which is what the target was defined on.

## At or above 90% (hermetic)

Provider adapters (batch 1) and task/settings/utils (batch 2) that reached ≥90%:
`constant(provider) 100`, `openrouter 100`, `tencent 97.7`, `minimax 97.4`, `xai 96.8`,
`common(provider) 95.3`, `submodel 95.2`, `jina 95`, `palm 94.4`, `mokaai 94`,
`zhipu 93.2`, `volcengine 91.9`, `gemini 91.8`, `jimeng 91.7`, `cloudflare 91.7`,
`cohere 91.4`, `siliconflow 91.1`, `claude 90.7`, `baidu_v2 90.2`;
`task/music 96.4`, `task/ali 94.7`, `task/gemini 93.3`, `task/jimeng 93.1`, `task/sora 93`,
`task/vidu 92.1`, `task/kling 92.1`, `task/suno 92`, `task/doubao 91`, `task/vertex 90`;
`model_setting 100`, `operation_setting 100`, `reasoning 100`, `system_setting 100`,
`pkg/constant 100`, `console_setting 99.4`, `logger 97.5`, `dto 97.1`,
`relay/common_handler 97`, `config 94.5`, `entity 94`, `setting 93.3`.

## Documented exceptions (hermetic ceiling < 90% — remainder is live-I/O only)

| Package | Hermetic % | Why the rest can't be unit-tested |
|---|---|---|
| `adapter/handler` | ~48% | Provider billing APIs, live relay round-trips, OIDC callback needs a real IdP, Meilisearch, platform gRPC — integration infra, not units (pre-existing exception). |
| `adapter/provider/openai` | 26.4% | The base OpenAI adapter: streaming/response handlers decode a live upstream `http.Response`; only request-building is hermetic. |
| `adapter/provider` (root) | 13.4% | `DoApiRequest` and friends are the real outbound HTTP layer every adapter delegates to. |
| `adapter/provider/xunfei` | 52.3% | WebSocket/live handlers. |
| `adapter/provider/coze` | 60.6% | Live chat/bot handlers. |
| `adapter/provider/ollama` | 68.7% | Live generate/embedding handlers. |
| `adapter/provider/aws` | 71.6% | Bedrock SDK `InvokeModel*` need a live AWS endpoint. |
| `adapter/provider/dify` | 71.8% | Live app-message handlers. |
| `adapter/provider/replicate` | 74.3% | Prediction poll loop hits a live upstream. |
| `adapter/provider/ali` | 75.5% | `asyncTaskWait`/`updateTask` poll a live channel URL. |
| `adapter/provider/deepseek` | 77.4% | `DoResponse` delegates to a live-shaped upstream body. |
| `adapter/provider/mistral` `moonshot` `zhipu_4v` `baidu` `perplexity` `vertex` | 81–87% | Response handlers decode a live upstream. |
| `adapter/provider/task/hailuo` | 83.1% | Task submit/poll dials a live upstream. |
| `pkg/tracing` | 88% | OTLP span export to a live collector. |
| `lifecycle` | 69.4% | Leader-election step loop is wall-clock + PG-lease timing. |
| `adapter/provider/{ai360,lingyiwanwu,xinference}` | `[no statements]` | Constants-only packages (model list + channel name); Go coverage instruments only function bodies, so % is undefined — value assertions were still added. |

## Method

Two background workflows (Opus orchestrating Sonnet writers), one agent per package,
each: read the whole package (incl. existing tests, to avoid duplication), write ONE
`<pkg>_coverage_test.go`, self-verify `go test -cover`, report the measured %. Every
result was independently re-verified here (git shows only `*_test.go` added — zero
source / go.mod changes; the full trees compile and pass; files spot-checked for
meaningful exact-value assertions, not tautologies).
