package routingdecision

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"github.com/gin-gonic/gin"
)

type fakeEvaluator struct {
	out   *EvalOutput
	err   error
	delay time.Duration
	calls atomic.Int32
	last  EvalInput
}

func (f *fakeEvaluator) Evaluate(ctx context.Context, _ *gin.Context, in EvalInput) (*EvalOutput, error) {
	f.calls.Add(1)
	f.last = in
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.out, f.err
}

func answer(choice string, p float64) *EvalOutput {
	return &EvalOutput{Choice: choice, Probabilities: map[string]float64{choice: p, "other": 1 - p}, InputTokens: 120, OutputTokens: 7}
}

func twoCandidates() (*entity.RoutingPolicy, []entity.RoutingCandidate) {
	p := validPolicy()
	return p, p.Candidates
}

func TestDecide_Outcomes(t *testing.T) {
	t.Setenv("ROUTING_DECISION_TIMEOUT_MS", "")
	p, cands := twoCandidates()

	t.Run("confident choice is applied", func(t *testing.T) {
		ev := &fakeEvaluator{out: answer("strong", 0.9)}
		out := Decide(nil, ev, p, "smart", "hard question", cands)
		if out.Reason != ReasonApplied || out.Target != "model-b" || out.TargetID != "strong" {
			t.Fatalf("outcome = %+v", out)
		}
		if out.Confidence != 0.9 || out.Eval == nil || out.Eval.InputTokens != 120 {
			t.Fatalf("confidence/usage lost: %+v", out)
		}
		if ev.last.Text != "hard question" || ev.last.Model != "evaluator-a" || len(ev.last.Candidates) != 2 {
			t.Fatalf("evaluator input = %+v", ev.last)
		}
	})

	t.Run("confidence below min falls back to the default candidate", func(t *testing.T) {
		ev := &fakeEvaluator{out: answer("strong", 0.64)}
		out := Decide(nil, ev, p, "smart", "x", cands)
		if out.Reason != ReasonLowConfidence || out.Target != "model-a" {
			t.Fatalf("outcome = %+v", out)
		}
		if out.Eval == nil {
			t.Fatal("a low-confidence evaluation was still paid for and must be billed")
		}
	})

	t.Run("confidence exactly at min is applied", func(t *testing.T) {
		ev := &fakeEvaluator{out: answer("strong", 0.65)}
		out := Decide(nil, ev, p, "smart", "x", cands)
		if out.Reason != ReasonApplied {
			t.Fatalf("outcome = %+v", out)
		}
	})

	t.Run("no_preference goes to the default", func(t *testing.T) {
		ev := &fakeEvaluator{out: answer(NoPreferenceID, 0.99)}
		out := Decide(nil, ev, p, "smart", "x", cands)
		if out.Reason != ReasonNoPreference || out.Target != "model-a" {
			t.Fatalf("outcome = %+v", out)
		}
	})

	t.Run("evaluator error is unavailable and falls back", func(t *testing.T) {
		ev := &fakeEvaluator{err: ErrNoEvaluatorChannel}
		out := Decide(nil, ev, p, "smart", "x", cands)
		if out.Reason != ReasonEvaluatorUnavailable || out.Target != "model-a" || !errors.Is(out.Err, ErrNoEvaluatorChannel) || out.Eval != nil {
			t.Fatalf("outcome = %+v", out)
		}
	})

	t.Run("unknown choice is unusable but its usage is kept", func(t *testing.T) {
		ev := &fakeEvaluator{out: answer("ghost", 0.99)}
		out := Decide(nil, ev, p, "smart", "x", cands)
		if out.Reason != ReasonEvaluatorUnavailable || out.Target != "model-a" || out.Eval == nil {
			t.Fatalf("outcome = %+v", out)
		}
	})

	t.Run("timeout is unavailable", func(t *testing.T) {
		t.Setenv("ROUTING_DECISION_TIMEOUT_MS", "20")
		ev := &fakeEvaluator{out: answer("strong", 0.9), delay: 2 * time.Second}
		start := time.Now()
		out := Decide(nil, ev, p, "smart", "x", cands)
		if out.Reason != ReasonEvaluatorUnavailable || !errors.Is(out.Err, context.DeadlineExceeded) {
			t.Fatalf("outcome = %+v", out)
		}
		if time.Since(start) > time.Second {
			t.Fatalf("timeout not honoured: %v", time.Since(start))
		}
	})

	t.Run("single candidate skips the evaluator", func(t *testing.T) {
		ev := &fakeEvaluator{out: answer("strong", 0.9)}
		out := Decide(nil, ev, p, "smart", "x", cands[1:])
		if out.Reason != ReasonSingleCandidate || out.Target != "model-b" || ev.calls.Load() != 0 {
			t.Fatalf("outcome = %+v calls=%d", out, ev.calls.Load())
		}
	})

	t.Run("no candidates left is ineligible", func(t *testing.T) {
		out := Decide(nil, &fakeEvaluator{}, p, "smart", "x", nil)
		if out.Reason != ReasonIneligible || out.Target != "" {
			t.Fatalf("outcome = %+v", out)
		}
	})

	t.Run("default narrowed away leaves the request alone", func(t *testing.T) {
		ev := &fakeEvaluator{err: errors.New("boom")}
		narrowed := []entity.RoutingCandidate{cands[1], {ID: "third", Model: "model-c", Criteria: "x"}}
		out := Decide(nil, ev, p, "smart", "x", narrowed)
		if out.Reason != ReasonEvaluatorUnavailable || out.Target != "" {
			t.Fatalf("outcome = %+v", out)
		}
	})
}

func TestDecide_ConcurrencyLimit(t *testing.T) {
	t.Setenv("ROUTING_DECISION_TIMEOUT_MS", "")
	p, cands := twoCandidates()
	for i := 0; i < MaxConcurrent; i++ {
		if !tryAcquire() {
			t.Fatalf("slot %d unavailable", i)
		}
	}
	t.Cleanup(func() {
		for i := 0; i < MaxConcurrent; i++ {
			release()
		}
	})
	ev := &fakeEvaluator{out: answer("strong", 0.9)}
	start := time.Now()
	out := Decide(nil, ev, p, "smart", "x", cands)
	if out.Reason != ReasonEvaluatorUnavailable || !errors.Is(out.Err, ErrEvaluatorBusy) || out.Target != "model-a" {
		t.Fatalf("outcome = %+v", out)
	}
	if ev.calls.Load() != 0 {
		t.Fatal("evaluator was called while the limiter was full")
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("a full limiter must not make the request wait")
	}
}

func TestTimeout(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"":      DefaultTimeout,
		"250":   250 * time.Millisecond,
		"0":     DefaultTimeout,
		"-5":    DefaultTimeout,
		"abc":   DefaultTimeout,
		"99999": DefaultTimeout,
	} {
		t.Setenv("ROUTING_DECISION_TIMEOUT_MS", raw)
		if got := Timeout(); got != want {
			t.Errorf("Timeout() with %q = %v, want %v", raw, got, want)
		}
	}
}

func TestRewriteModel(t *testing.T) {
	in := `{"model":"smart","max_tokens":1e3,"messages":[{"role":"user","content":"a<b>&c"}],"stream":true}`
	out, err := RewriteModel([]byte(in), "model-b")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`"model":"model-b"`, `"max_tokens":1e3`, `"content":"a<b>&c"`, `"stream":true`} {
		if !contains(s, want) {
			t.Errorf("rewritten body lost %s: %s", want, s)
		}
	}
	if _, err := RewriteModel([]byte(`[1]`), "m"); err == nil {
		t.Error("a non-object body must be rejected")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestIndex(t *testing.T) {
	now := time.Unix(1000, 0)
	loads := 0
	rows := []entity.RoutingPolicy{
		{TenantID: "t1", PublicModel: "smart", Strategy: entity.RoutingStrategyDecision, Enabled: true},
		{TenantID: "t1", PublicModel: "off", Strategy: entity.RoutingStrategyDecision, Enabled: false},
		{TenantID: "t2", PublicModel: "smart", Strategy: "other", Enabled: true},
	}
	var loadErr error
	idx := NewIndex(func() ([]entity.RoutingPolicy, error) { loads++; return rows, loadErr }, func() time.Time { return now })

	if idx.Lookup("t1", "smart") == nil {
		t.Fatal("enabled policy not found")
	}
	if idx.Lookup("t1", "off") != nil || idx.Lookup("t2", "smart") != nil || idx.Lookup("t3", "smart") != nil {
		t.Fatal("disabled / foreign-strategy / other-tenant policy matched")
	}
	if idx.Lookup("", "smart") != nil || idx.Lookup("t1", "") != nil {
		t.Fatal("empty key matched")
	}
	if loads != 1 {
		t.Fatalf("loads = %d, want 1 (all lookups inside the TTL share one load)", loads)
	}

	now = now.Add(IndexTTL + time.Second)
	rows = nil
	if idx.Lookup("t1", "smart") != nil {
		t.Fatal("expired snapshot served after the policy was removed")
	}
	if loads != 2 {
		t.Fatalf("loads = %d, want 2", loads)
	}

	// A failing reload keeps serving the previous view.
	rows = []entity.RoutingPolicy{{TenantID: "t1", PublicModel: "smart", Strategy: entity.RoutingStrategyDecision, Enabled: true}}
	idx.Invalidate()
	if idx.Lookup("t1", "smart") == nil {
		t.Fatal("not reloaded after Invalidate")
	}
	now = now.Add(IndexTTL + time.Second)
	loadErr = errors.New("db down")
	if idx.Lookup("t1", "smart") == nil {
		t.Fatal("a failed reload dropped the previous view")
	}
}

func TestIndex_NoPolicyIsZeroDBAfterFirstLoad(t *testing.T) {
	now := time.Unix(1000, 0)
	loads := 0
	idx := NewIndex(func() ([]entity.RoutingPolicy, error) { loads++; return nil, nil }, func() time.Time { return now })
	for i := 0; i < 1000; i++ {
		_ = idx.Lookup("t1", "smart")
	}
	if loads != 1 {
		t.Fatalf("loads = %d for 1000 lookups with no policies, want 1", loads)
	}
}
