package governance

// Data classification tiers aligned with OTel Telemetry Governance Framework.
// Each log/audit field is assigned a tier that dictates collection, storage,
// export, and retention policies.
//
// Reference: OpenTelemetry Telemetry Data Governance (2026)
//   - Public    → freely queryable, aggregatable, exportable
//   - Internal  → admin-visible only, not exposed to end-user APIs
//   - Confidential → requires opt-in, masked on export, retention-limited
//   - Restricted → NEVER stored; use fingerprints or hashes instead
//
// W3C PROV mapping:
//   - Agent (Who)    → user_id, token_id, channel_id      [Confidential/Internal]
//   - Activity (What)→ relay_mode, upstream_model, latency [Public/Internal]
//   - Entity (Result)→ tokens, quota, content summary      [Public]

// DataTier represents the sensitivity classification of a data field.
type DataTier int

const (
	// TierPublic: freely queryable, aggregatable, safe to export.
	// Fields: model_name, channel_type, relay_mode, total_latency_ms,
	//         quota, prompt_tokens, completion_tokens, is_stream, created_at
	TierPublic DataTier = iota

	// TierInternal: visible to admins only, not returned in user-facing APIs.
	// Fields: channel_id, channel_name, upstream_model, group,
	//         request_fingerprint, use_channel, model_ratio, group_ratio,
	//         data_flow_source, data_flow_dest
	TierInternal

	// TierConfidential: requires user opt-in (RecordIpLog), masked on export,
	// subject to retention limits.
	// Fields: user_id, username, token_id, token_name, ip, tenant_id
	TierConfidential

	// TierRestricted: MUST NEVER be stored in plaintext.
	// Fingerprints or hashes are used instead.
	// Fields: request body (prompt), response body, API keys, channel keys
	TierRestricted
)

// FieldClassification maps known log/audit fields to their data tier.
// Used by export filters (e.g., Meilisearch sync) to decide what to include.
var FieldClassification = map[string]DataTier{
	// Public — safe for dashboards, aggregation, export
	"model_name":        TierPublic,
	"channel_type":      TierPublic,
	"relay_mode":        TierPublic,
	"total_latency_ms":  TierPublic,
	"quota":             TierPublic,
	"prompt_tokens":     TierPublic,
	"completion_tokens": TierPublic,
	"is_stream":         TierPublic,
	"created_at":        TierPublic,
	"type":              TierPublic,
	"use_time":          TierPublic,
	// Time to first token. Public for the same reason total_latency_ms is: the
	// framework's own PROV mapping puts latency under Activity, and this is the
	// caller's own request timing — it carries no price, no margin and no
	// upstream identity. It sat under Internal until 2026-09-01, which meant the
	// headline performance number of an AI gateway was the one thing the paying
	// customer could not see, while the coarser total_latency_ms right next to
	// it was public. (It was also a sentinel -1000 on every non-streaming
	// request until the same day, so nobody had reason to question the tier.)
	"frt": TierPublic,

	// Internal — admin only
	"channel_id":          TierInternal,
	"channel_name":        TierInternal,
	"upstream_model":      TierInternal,
	"group":               TierInternal,
	"request_fingerprint": TierInternal,
	"model_ratio":         TierInternal,
	"group_ratio":         TierInternal,
	"completion_ratio":    TierInternal,
	"model_price":         TierInternal,
	"data_flow_source":    TierInternal,
	"data_flow_dest":      TierInternal,
	"admin_info":          TierInternal,

	// Confidential — user opt-in, masked on export
	"user_id":    TierConfidential,
	"username":   TierConfidential,
	"token_id":   TierConfidential,
	"token_name": TierConfidential,
	"ip":         TierConfidential,
	"client_ip":  TierConfidential,
	"tenant_id":  TierConfidential,
}

// IsExportSafe returns true if the field can be safely exported to
// external systems (e.g., Meilisearch, analytics pipelines).
// Only Public and Internal fields are export-safe; Confidential fields
// require explicit opt-in checked by the caller.
func IsExportSafe(field string) bool {
	tier, known := FieldClassification[field]
	if !known {
		// Unknown fields default to Internal (admin-only, not exported).
		return false
	}
	return tier == TierPublic || tier == TierInternal
}
