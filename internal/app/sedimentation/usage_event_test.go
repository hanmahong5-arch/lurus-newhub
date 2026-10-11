package sedimentation

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	hubnats "github.com/LurusTech/lurus-hub/internal/pkg/nats"
)

func consumeRow() *repo.Log {
	return &repo.Log{
		UserId: 9, TenantId: "t1", Type: repo.LogTypeConsume, ProjectId: 4, TokenId: 5,
		ModelName: "model-a", PromptTokens: 10, CompletionTokens: 20, Quota: 30,
		ChargedCNY4: 777, CreatedAt: 1700000000,
		// Content is deliberately populated: it must never reach the event.
		Content: "SECRET-PROMPT-TEXT",
		Other:   `{"request_id":"req-1","end_user":"hash-xyz","note":"SECRET-OTHER"}`,
	}
}

func TestBuildItem_MetadataOnly(t *testing.T) {
	it, ok := buildItem(consumeRow())
	if !ok {
		t.Fatal("consume row with request id must be eligible")
	}
	raw, _ := json.Marshal(it.payload)
	if strings.Contains(string(raw), "SECRET") {
		t.Fatalf("payload leaks row text: %s", raw)
	}
	p := it.payload
	if p.TenantID != "t1" || p.ProjectID != 4 || p.TokenID != 5 || p.EndUserHash != "hash-xyz" ||
		p.Model != "model-a" || p.PromptTokens != 10 || p.CompletionTokens != 20 || p.Quota != 30 ||
		p.ChargedCNY4 != 777 || p.RequestID != "req-1" || p.CreatedAt != 1700000000 {
		t.Fatalf("payload = %+v", p)
	}
	// The wire shape is a closed set of keys; a new one must be a conscious
	// contract change (doc/contracts/llm-usage-recorded.md).
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	allowed := map[string]bool{"v": true, "tenant_id": true, "project_id": true, "token_id": true,
		"end_user_hash": true, "model": true, "prompt_tokens": true, "completion_tokens": true,
		"quota": true, "charged_cny4": true, "request_id": true, "created_at": true, "has_body": true,
		// optional metering fields (migration 053), omitempty
		"relay_mode": true, "usage_unit": true, "usage_quantity": true,
		"usage_source": true, "retrieval_documents": true}
	for k := range m {
		if !allowed[k] {
			t.Errorf("unexpected payload key %q", k)
		}
	}
}

// A row without metering columns (written before migration 053) must produce
// an event that is byte-identical to the old shape: no new keys at all.
func TestBuildItem_LegacyRowOmitsMeteringKeys(t *testing.T) {
	it, _ := buildItem(consumeRow())
	raw, _ := json.Marshal(it.payload)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	for _, k := range []string{"relay_mode", "usage_unit", "usage_quantity", "usage_source", "retrieval_documents"} {
		if _, has := m[k]; has {
			t.Errorf("legacy row must not emit %q: %s", k, raw)
		}
	}
}

func TestBuildItem_CarriesMetering(t *testing.T) {
	l := consumeRow()
	l.RelayMode = relayconstant.RelayModeRerank
	l.UsageUnit, l.UsageQuantity, l.UsageSource, l.RetrievalDocuments = "search_unit", 2, "estimated", 101
	it, ok := buildItem(l)
	if !ok {
		t.Fatal("row must be eligible")
	}
	p := it.payload
	if p.RelayMode != "rerank" || p.UsageUnit != "search_unit" || p.UsageQuantity != 2 ||
		p.UsageSource != "estimated" || p.RetrievalDocuments != 101 {
		t.Fatalf("metering fields not carried: %+v", p)
	}
	raw, _ := json.Marshal(p)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	for _, k := range []string{"relay_mode", "usage_unit", "usage_quantity", "usage_source", "retrieval_documents"} {
		if _, has := m[k]; !has {
			t.Errorf("missing %q in %s", k, raw)
		}
	}
}

func TestBuildItem_Ineligible(t *testing.T) {
	mut := map[string]func(*repo.Log){
		"error row":     func(l *repo.Log) { l.Type = repo.LogTypeError },
		"no tenant":     func(l *repo.Log) { l.TenantId = "" },
		"no user":       func(l *repo.Log) { l.UserId = 0 },
		"no request id": func(l *repo.Log) { l.Other = `{"end_user":"x"}` },
		"bad other":     func(l *repo.Log) { l.Other = "not json" },
	}
	for name, f := range mut {
		l := consumeRow()
		f(l)
		if _, ok := buildItem(l); ok {
			t.Errorf("%s must not produce an event", name)
		}
	}
	if _, ok := buildItem(nil); ok {
		t.Error("nil row must not produce an event")
	}
}

type recorder struct {
	mu  sync.Mutex
	got []hubnats.LLMUsageRecordedPayload
}

func (r *recorder) publish(_ context.Context, _ int, p hubnats.LLMUsageRecordedPayload) {
	r.mu.Lock()
	r.got = append(r.got, p)
	r.mu.Unlock()
}

func (r *recorder) n() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

func runOne(t *testing.T, d deps, tenants ...string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := make(chan item, 8)
	done := make(chan struct{})
	go func() { runWorker(ctx, q, d); close(done) }()
	for _, tn := range tenants {
		l := consumeRow()
		l.TenantId = tn
		it, _ := buildItem(l)
		q <- it
	}
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done
}

func TestWorker_PublishesOnlyWithConsent(t *testing.T) {
	rec := &recorder{}
	consent := map[string]bool{"yes": true, "no": false}
	runOne(t, deps{
		consent: func(id string) bool { return consent[id] },
		hasBody: func(string, string) bool { return true },
		publish: rec.publish,
	}, "no", "yes")
	if rec.n() != 1 || rec.got[0].TenantID != "yes" {
		t.Fatalf("published = %+v, want exactly the consenting tenant", rec.got)
	}
	if !rec.got[0].HasBody {
		t.Error("has_body must reflect the probe")
	}
}

func TestWorker_NoConsentNeverProbesBodyOrPublishes(t *testing.T) {
	rec := &recorder{}
	probed := false
	runOne(t, deps{
		consent: func(string) bool { return false },
		hasBody: func(string, string) bool { probed = true; return true },
		publish: rec.publish,
	}, "t1")
	if rec.n() != 0 || probed {
		t.Fatalf("published=%d probed=%v, want neither", rec.n(), probed)
	}
}

func TestWorker_HasBodyFalseWhenNothingArchived(t *testing.T) {
	rec := &recorder{}
	runOne(t, deps{
		consent: func(string) bool { return true },
		hasBody: func(string, string) bool { return false },
		publish: rec.publish,
	}, "t1")
	if rec.n() != 1 || rec.got[0].HasBody {
		t.Fatalf("got %+v", rec.got)
	}
}

func TestWorker_ConsentLookupCachedPerTenant(t *testing.T) {
	rec := &recorder{}
	calls := 0
	runOne(t, deps{
		consent: func(string) bool { calls++; return true },
		hasBody: func(string, string) bool { return false },
		publish: rec.publish,
	}, "t1", "t1", "t1")
	if calls != 1 || rec.n() != 3 {
		t.Fatalf("consent calls=%d published=%d, want 1 and 3", calls, rec.n())
	}
}

func TestRegister_NoopWhenNATSDisabled(t *testing.T) {
	t.Setenv("LLM_QUOTA_NATS_ENABLED", "")
	if RegisterUsageEvents(context.Background()) {
		t.Fatal("must register nothing when NATS is disabled")
	}
	if started {
		t.Fatal("worker started with NATS disabled")
	}
}
