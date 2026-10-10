package nats

import "context"

// SubjectLLMUsageRecorded fires once per stored consume log of a tenant that
// consented to data sedimentation. Metadata only - the payload never carries
// prompt or response text (a consumer pulls an archived body through the
// authenticated GET body API, keyed by request_id). Contract:
// doc/contracts/llm-usage-recorded.md. Its "llm.usage." prefix lands the
// publish-failure counter in the "usage" subject_group.
const SubjectLLMUsageRecorded = "llm.usage.recorded"

// UsageRecordedVersion is the payload's v field; bump only on a breaking change.
const UsageRecordedVersion = 1

// LLMUsageRecordedPayload is the wire shape of llm.usage.recorded (v=1).
// Deliberately a closed struct of scalars: adding a free-text field here is a
// contract change, not a convenience.
type LLMUsageRecordedPayload struct {
	V                int    `json:"v"`
	TenantID         string `json:"tenant_id"`
	ProjectID        int    `json:"project_id"`
	TokenID          int    `json:"token_id"`
	EndUserHash      string `json:"end_user_hash,omitempty"` // tenant-scoped HMAC; never the raw id
	Model            string `json:"model"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	Quota            int    `json:"quota"`
	ChargedCNY4      int64  `json:"charged_cny4"` // wallet debit in 1/10000 CNY; 0 = nothing recorded
	RequestID        string `json:"request_id"`
	CreatedAt        int64  `json:"created_at"` // unix seconds, the log row's timestamp
	HasBody          bool   `json:"has_body"`
}

// PublishUsageRecorded emits one llm.usage.recorded event. userID is the
// platform account_id (envelope account_id). No-op when NATS is not
// initialised or userID <= 0. Fire-and-forget: a failure is logged and counted
// by Publisher.Publish (lurus_nats_publish_failed_total{subject_group="usage"})
// and never reaches the caller.
func PublishUsageRecorded(ctx context.Context, userID int, p LLMUsageRecordedPayload) {
	if userID <= 0 {
		return
	}
	pub := Get()
	if pub == nil {
		return
	}
	p.V = UsageRecordedVersion
	publishLLMEvent(ctx, pub, SubjectLLMUsageRecorded, int64(userID), p, p.Model)
}
