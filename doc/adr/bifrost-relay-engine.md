# ADR: Bifrost as in-process relay engine

**Status**: Accepted  
**Date**: 2026-10-23  
**Scope**: `2b-svc-newhub` only (not `2l-svc-platform`)

## Decision

Embed [`github.com/maximhq/bifrost/core`](https://github.com/maximhq/bifrost) **in-process** behind existing Gin relay routes (`hub.lurus.cn/v1/*`). Flag-gated:

| Env | Default | Meaning |
|-----|---------|---------|
| `RELAY_ENGINE` | `legacy` | `legacy` = existing `GetAdaptor` path; `bifrost` = Bifrost Go SDK |
| `RELAY_BIFROST_MODES` | `chat` | Comma list of modalities Bifrost may own |

Unmapped `ApiType` values **always** fall through to legacy even when `RELAY_ENGINE=bifrost`.

## Why not a Bifrost HTTP sidecar

D1 already rejected rewriting newhub as an HTTP client to another gateway (extra hop, dual-repo boundary erosion). Process-in SDK keeps auth / credit-pool / Platform settlement on the outer shell.

## Billing

newhub PreConsume / PostConsume / credit-pool / platform wallet remain the **sole ledger**. Bifrost budget / virtual-key features are not used as an account of record. The registered `billingPlugin` is intentionally a no-op extension point.

## Retry / failover

Bifrost `NetworkConfig.MaxRetries = 0` and request `Fallbacks = nil`. Channel-level retry stays in newhub's outer relay loop so the two layers do not stack.

## Coverage

| Label | ApiTypes (examples) |
|-------|---------------------|
| `bifrost-owned` | OpenAI, OpenRouter, DeepSeek, SiliconFlow, Moonshot, xAI, Mistral, Anthropic, Gemini, Ollama, Cohere, Ali, VolcEngine, ZhipuV4, … |
| `deferred` (legacy) | Baidu, Xunfei, Zhipu (v3), Tencent, PaLM, Dify, Jina, Cloudflare, Coze, Jimeng, MokaAI, Replicate, **Bedrock, Vertex** (ApiKey+BaseURL insufficient; IAM/project extras unmodeled in `account.go`) |

See `internal/app/relay/bifrosteng/mapping.go`.

### Modalities (`RELAY_BIFROST_MODES`)

| Mode | Status |
|------|--------|
| `chat` | Wired (`TextHelper` → `DispatchChat`, stream + non-stream). Convert covers text, `image_url` / `input_audio` / `file`, tool_calls, and `tools`/`tool_choice`/`stop`/`seed`/`response_format`. `video_url` and pass-through channels **fall through to legacy** (`ChatConvertible` / `ShouldHandleChat`). |
| `embeddings` | Wired (`EmbeddingHelper` → `DispatchEmbedding`); enable via `RELAY_BIFROST_MODES=chat,embeddings` |
| `messages` (Claude wire) | **deferred** — `ClaudeHelper` stays on legacy adaptor |
| images / audio | **deferred** — legacy adaptor |

Failover: Bifrost `Fallbacks` left empty; newhub outer channel retry is the only failover layer (`MaxRetries=0`).

## Observability (admin console)

No separate Bifrost SPA. Operators read relay-engine state on the existing Admin **Gateway** page (`web/src/pages/v2/Admin/Gateway`), which already owns the honesty contract (per-replica, lazy/absent ≠ healthy, 401/403/error ≠ empty-green). Visual north-star = CostIntelligence caveat strip + ModelPerformance 3-up KPIs (`CaveatStrip` / `KpiTile`), not a Bifrost product UI fork.

| Surface | Role |
|---------|------|
| `GET /api/v2/admin/gateway/relay-engine` | Flag + modes + ready + process-start totals (`bifrosteng.Snapshot`) |
| Prometheus `lurus_gateway_relay_engine_total` / `lurus_gateway_bifrost_duration_seconds` | Long-window series |
| Gateway UI panel | Same poll/caveat pattern as breakers; `engine=legacy` + `ready=false` is the default healthy posture |

## Platform

No Bifrost import in `2l-svc-platform`. Money-path activation (credit-pool, `LLM_TOKEN_PROVIDER=newhub`) is independent of this ADR.
