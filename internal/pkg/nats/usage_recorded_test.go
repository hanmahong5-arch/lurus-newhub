package nats

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

func TestPublishUsageRecorded_EnvelopeAndVersion(t *testing.T) {
	fj := &fakeJS{}
	withGlobal(t, fj)

	PublishUsageRecorded(context.Background(), 42, LLMUsageRecordedPayload{
		TenantID: "t1", RequestID: "req-1", Model: "model-a", HasBody: true, V: 99,
	})
	if fj.mu.calls != 1 || fj.mu.subject != SubjectLLMUsageRecorded {
		t.Fatalf("calls=%d subject=%q", fj.mu.calls, fj.mu.subject)
	}
	env := decodeEnvelope(t, fj.mu.data)
	if env.EventType != SubjectLLMUsageRecorded || env.AccountID != 42 {
		t.Fatalf("envelope = %+v", env)
	}
	var p LLMUsageRecordedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.V != UsageRecordedVersion || p.RequestID != "req-1" || !p.HasBody {
		t.Fatalf("payload = %+v (v must be forced to the contract version)", p)
	}
}

func TestPublishUsageRecorded_NoopWhenDisabledOrAnonymous(t *testing.T) {
	prev := global
	global = nil
	t.Cleanup(func() { global = prev })
	PublishUsageRecorded(context.Background(), 42, LLMUsageRecordedPayload{}) // must not panic

	fj := &fakeJS{}
	withGlobal(t, fj)
	PublishUsageRecorded(context.Background(), 0, LLMUsageRecordedPayload{})
	if fj.mu.calls != 0 {
		t.Fatalf("anonymous publish reached the broker")
	}
}

// A failed publish is counted under the usage group and never panics or
// surfaces to the caller.
func TestPublishUsageRecorded_FailureCountedAsUsage(t *testing.T) {
	withGlobal(t, failingJS{})
	before := testutil.ToFloat64(metrics.NATSPublishFailedTotal.WithLabelValues("usage"))
	PublishUsageRecorded(context.Background(), 42, LLMUsageRecordedPayload{RequestID: "r"})
	after := testutil.ToFloat64(metrics.NATSPublishFailedTotal.WithLabelValues("usage"))
	if after != before+1 {
		t.Fatalf("usage failures %v -> %v, want +1", before, after)
	}
}
