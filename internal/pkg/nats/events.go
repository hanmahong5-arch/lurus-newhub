package nats

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Typed LLM event helpers for downstream consumers (notification module).
//
// Subjects + envelope shapes mirror 2l-svc-platform/modules/notification
// internal/pkg/event/types.go AND 2b-svc-newapi/service/event_publish.go.
// Cross-fork wire compat is required so the unified inbox handles events
// from either gateway transparently.
//
// Ported from newapi commit 9ef4e6db (2026-04-30) + fc49e72d (2026-05-01).

const (
	// SubjectLLMImageGenerated is fired once per successful image-generation.
	SubjectLLMImageGenerated = "llm.image.generated"
	// SubjectLLMUsageMilestone is fired when a user crosses a usage milestone
	// (e.g. 10k tokens this day, 1M tokens lifetime).
	SubjectLLMUsageMilestone = "llm.usage.milestone"
	// SubjectPoolThreshold fires when a tenant credit pool dips below its
	// configured alert_threshold_pct. No _crossed suffix — matches
	// llm.quota.threshold + llm.image.generated convention.
	// Canonical: ADR 2026-05-18 (tenant-credit-pool) §9 Q5.
	SubjectPoolThreshold = "llm.pool.threshold"
)

// llmEventEnvelope is the canonical wire format for LLM_EVENTS stream.
//
//	{
//	  "event_id":   "<uuid>",
//	  "event_type": "<subject>",
//	  "account_id": <int64>,
//	  "payload":    {...},
//	  "occurred_at": "2026-04-30T10:00:00Z"
//	}
type llmEventEnvelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	AccountID  int64           `json:"account_id"`
	Payload    json.RawMessage `json:"payload"`
	OccurredAt time.Time       `json:"occurred_at"`
}

// LLMImageGeneratedPayload — payload shape for image.generated events.
type LLMImageGeneratedPayload struct {
	JobID    string `json:"job_id"`
	ImageURL string `json:"image_url"`
	Prompt   string `json:"prompt"`
}

// LLMUsageMilestonePayload — payload shape for usage.milestone events.
type LLMUsageMilestonePayload struct {
	Period     string `json:"period"`      // "day" | "month" | "lifetime"
	TokensUsed int64  `json:"tokens_used"`
	Milestone  string `json:"milestone"`   // human-readable bucket label
}

// PublishImageGenerated emits one llm.image.generated event for the user.
// userID is the platform account_id (newhub user.id == platform account_id
// by SSO bridge convention). Prompt is truncated to 80 chars to match the
// notification UX expectation. Fire-and-forget; logs but never errors.
//
// No-op when:
//   - publisher is not initialised (NATS disabled)
//   - userID <= 0 (anonymous / pre-auth)
func PublishImageGenerated(ctx context.Context, userID int, model, prompt, imageURL string) {
	if userID <= 0 {
		return
	}
	p := Get()
	if p == nil {
		return
	}
	payload := LLMImageGeneratedPayload{
		JobID:    uuid.NewString(),
		ImageURL: imageURL,
		Prompt:   truncateRune(prompt, 80),
	}
	publishLLMEvent(ctx, p, SubjectLLMImageGenerated, int64(userID), payload, model)
}

// PublishUsageMilestone emits one llm.usage.milestone event.
// period: "day" | "month" | "lifetime". milestone: free-form bucket label.
func PublishUsageMilestone(ctx context.Context, userID int, tokensUsed int64, milestone, period string) {
	if userID <= 0 {
		return
	}
	p := Get()
	if p == nil {
		return
	}
	payload := LLMUsageMilestonePayload{
		Period:     period,
		TokensUsed: tokensUsed,
		Milestone:  milestone,
	}
	publishLLMEvent(ctx, p, SubjectLLMUsageMilestone, int64(userID), payload, "")
}

// eventPublishBudget bounds one fire-and-forget event publish. Nothing waits on
// the result, so the only thing this number protects is the calling goroutine.
const eventPublishBudget = 5 * time.Second

// publishLLMEvent builds the canonical envelope and hands off to the publisher.
// Marshaling errors are logged but never propagated to the caller.
//
// The caller's context is used for its values only: cancellation is stripped
// with context.WithoutCancel and replaced by eventPublishBudget. Both typed
// helpers are called with the HTTP request's context
// (internal/app/relay/image_handler.go:160 for image.generated,
// internal/app/relay/compatible_handler.go:552 → app.CheckAndPublishUsageMilestone
// for usage.milestone), which is already cancelled whenever the client hung up.
// Publisher.Publish honours cancellation as of cycle 12, so without this detach
// "the browser closed" would silently become "the notification was dropped".
func publishLLMEvent(ctx context.Context, p *Publisher, subject string, accountID int64, typedPayload any, model string) {
	rawPayload, err := json.Marshal(typedPayload)
	if err != nil {
		slog.Warn("nats publishLLMEvent marshal failed", "subject", subject, "err", err)
		return
	}
	env := llmEventEnvelope{
		EventID:    uuid.NewString(),
		EventType:  subject,
		AccountID:  accountID,
		Payload:    rawPayload,
		OccurredAt: time.Now().UTC(),
	}
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), eventPublishBudget)
	defer cancel()
	if err := p.Publish(pubCtx, subject, env); err != nil {
		slog.Warn("nats publishLLMEvent enqueue failed",
			"subject", subject, "account_id", accountID, "model", model, "err", err)
	}
}

// truncateRune returns s if it has at most max runes, else the first max
// runes. Avoids slicing inside a multi-byte UTF-8 sequence.
func truncateRune(s string, max int) string {
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
