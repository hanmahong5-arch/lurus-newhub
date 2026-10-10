package nats

import (
	"context"
	"errors"
	"testing"

	natsgo "github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

func TestSubjectGroup(t *testing.T) {
	cases := map[string]string{
		SubjectQuotaThreshold:    "quota",
		SubjectLLMUsageMilestone: "usage",
		SubjectLLMImageGenerated: "image",
		SubjectPoolThreshold:     "other",
		"something.else":         "other",
	}
	for subj, want := range cases {
		if got := subjectGroup(subj); got != want {
			t.Errorf("subjectGroup(%q) = %q, want %q", subj, got, want)
		}
	}
}

func TestRecordPublishFailure_IncrementsGroup(t *testing.T) {
	before := testutil.ToFloat64(metrics.NATSPublishFailedTotal.WithLabelValues("quota"))
	recordPublishFailure(SubjectQuotaThreshold)
	after := testutil.ToFloat64(metrics.NATSPublishFailedTotal.WithLabelValues("quota"))
	if after != before+1 {
		t.Fatalf("quota counter %v -> %v, want +1", before, after)
	}
}

// failingJS rejects every publish, as a broker outage would.
type failingJS struct {
	natsgo.JetStreamContext
}

func (failingJS) Publish(string, []byte, ...natsgo.PubOpt) (*natsgo.PubAck, error) {
	return nil, errors.New("broker down")
}

// The counter must move through the real Publish path, not only via the
// helper: that is what a caller's failed event actually exercises.
func TestPublish_BrokerErrorCounted(t *testing.T) {
	p := newPublisher(failingJS{}, "LLM_EVENTS")
	c := metrics.NATSPublishFailedTotal.WithLabelValues("image")
	before := testutil.ToFloat64(c)
	if err := p.Publish(context.Background(), SubjectLLMImageGenerated, map[string]string{"k": "v"}); err == nil {
		t.Fatal("want publish error")
	}
	if got := testutil.ToFloat64(c); got != before+1 {
		t.Fatalf("image counter %v -> %v, want +1", before, got)
	}
}
