package middleware

// decision_routing_test.go - ApplyDecisionRouting end to end against the real
// evaluator path: policy index (sqlite), tenant-scoped System One channel
// selection, the System One adaptor and the shared fake vendor
// (internal/testkit/fakeupstream) whose choice answers are steered by keywords
// in the criteria text ("cheap" -> 0.8, "vague" -> 0.4).

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/app/routingdecision"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
	"github.com/LurusTech/lurus-hub/internal/testkit/fakeupstream"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const drTenant = "t-route"

type drAudit struct {
	mu     sync.Mutex
	events []*entity.AuditEvent
}

func (a *drAudit) CreateAuditEvent(e *entity.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
	return nil
}

func (a *drAudit) byAction(action string) []*entity.AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []*entity.AuditEvent
	for _, e := range a.events {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

type drSettle struct {
	model  string
	tokens int
	n      int
}

type drFixture struct {
	t      *testing.T
	engine *gin.Engine
	fake   *fakeupstream.Server
	audit  *drAudit
	settle *drSettle
	// limit, when set, is installed as the token model limit.
	limit map[string]bool
}

// setupDecision wires sqlite, a System One channel for evaluator-a pointing at
// the fake vendor, and a route that mimics the Distribute() insertion point.
// delay slows the vendor to exercise the evaluation deadline.
func setupDecision(t *testing.T, delay time.Duration, withChannel bool) *drFixture {
	t.Helper()
	db, cleanup := setupCoverDB(t)
	t.Cleanup(cleanup)
	app.InitHttpClient()
	if err := db.AutoMigrate(&entity.RoutingPolicy{}); err != nil {
		t.Fatal(err)
	}

	prevCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prevCache })

	fs := system_setting.GetFetchSetting()
	prevAllow := fs.AllowPrivateIp
	fs.AllowPrivateIp = true // the fake vendor listens on loopback
	t.Cleanup(func() { fs.AllowPrivateIp = prevAllow })

	f := &drFixture{t: t, audit: &drAudit{}, settle: &drSettle{}}
	f.fake = fakeupstream.New(fakeupstream.Config{})
	h := f.fake.Handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			time.Sleep(delay)
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	if withChannel {
		base := srv.URL
		weight, priority := uint(100), int64(0)
		ch := &repo.Channel{
			Id: 9801, Type: constant.ChannelTypeTypeSafe, Status: common.ChannelStatusEnabled, Name: "evaluator",
			Key: "k", Models: "evaluator-a", Group: "default", TenantId: "default", BaseURL: &base,
			Weight: &weight, Priority: &priority,
		}
		if err := db.Create(ch).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&repo.Ability{Group: "default", Model: "evaluator-a", ChannelId: 9801, Enabled: true, Weight: weight, Priority: &priority}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Rebuild the process-wide channel cache for THIS database even when no
	// channel was seeded, so a previous test's evaluator channel cannot leak in.
	repo.InitChannelCache()

	governance.SetAuditWriter(f.audit)
	t.Cleanup(func() { governance.SetAuditWriter(nil) })

	prevSettle := routingdecision.Settle
	routingdecision.Settle = func(_ *gin.Context, model string, out *routingdecision.EvalOutput, _ time.Duration) {
		f.settle.model, f.settle.tokens = model, out.InputTokens
		f.settle.n++
	}
	t.Cleanup(func() { routingdecision.Settle = prevSettle })
	routingdecision.Invalidate()
	t.Cleanup(routingdecision.Invalidate)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	handler := func(c *gin.Context) {
		c.Set("tenant_context", &TenantContext{TenantID: drTenant})
		c.Set("tenant_id", drTenant)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		if f.limit != nil {
			common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
			common.SetContextKey(c, constant.ContextKeyTokenModelLimit, f.limit)
		}
		mr, _, err := getModelRequest(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"err": err.Error()})
			return
		}
		ApplyDecisionRouting(c, mr, drTenant)
		body, _ := common.GetRequestBody(c)
		c.JSON(http.StatusOK, gin.H{
			"model":          mr.Model,
			"body_model":     gjson.GetBytes(body, "model").String(),
			"original_model": c.GetString("original_model"),
			"content_length": c.Request.ContentLength,
			"body_len":       len(body),
		})
	}
	r.POST("/v1/chat/completions", handler)
	r.POST("/v1/responses", handler)
	r.POST("/v1/messages", handler)
	f.engine = r
	return f
}

func (f *drFixture) seedPolicy(p entity.RoutingPolicy) {
	f.t.Helper()
	if p.TenantID == "" {
		p.TenantID = drTenant
	}
	if p.Strategy == "" {
		p.Strategy = entity.RoutingStrategyDecision
	}
	if err := repo.UpsertRoutingPolicy(&p); err != nil {
		f.t.Fatal(err)
	}
	routingdecision.Invalidate()
}

func twoWayPolicy(enabled bool, cheapCriteria, strongCriteria string) entity.RoutingPolicy {
	return entity.RoutingPolicy{
		PublicModel: "smart", Enabled: enabled, EvaluatorModel: "evaluator-a", MinConfidence: 0.65, DefaultCandidate: "strong",
		Candidates: entity.RoutingCandidates{
			{ID: "cheap", Model: "model-a", Criteria: cheapCriteria},
			{ID: "strong", Model: "model-b", Criteria: strongCriteria},
		},
	}
}

func (f *drFixture) do(path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	return w
}

const chatBody = `{"model":"smart","messages":[{"role":"user","content":"my secret passport 12345 please summarise"}]}`

func evalHits(f *drFixture) int {
	n := 0
	for _, r := range f.fake.Requests() {
		if r.Path == "/v1/systemone" {
			n++
		}
	}
	return n
}

func TestApplyDecisionRouting_AppliedRewritesEverythingAndAnnounces(t *testing.T) {
	f := setupDecision(t, 0, true)
	f.seedPolicy(twoWayPolicy(true, "cheap simple questions", "hard problems"))

	w := f.do("/v1/chat/completions", chatBody)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if got := w.Header().Get(HeaderRoutedModel); got != "model-a" {
		t.Errorf("%s = %q, want model-a", HeaderRoutedModel, got)
	}
	if got := w.Header().Get(HeaderRoutingReason); got != "decision:applied" {
		t.Errorf("%s = %q, want decision:applied", HeaderRoutingReason, got)
	}
	b := w.Body.String()
	for _, k := range []string{"model", "body_model", "original_model"} {
		if gjson.Get(b, k).String() != "model-a" {
			t.Errorf("%s = %q, want model-a (distributor, relay body and gin key must all agree): %s", k, gjson.Get(b, k).String(), b)
		}
	}
	if gjson.Get(b, "content_length").Int() != gjson.Get(b, "body_len").Int() {
		t.Errorf("ContentLength %d != rewritten body length %d", gjson.Get(b, "content_length").Int(), gjson.Get(b, "body_len").Int())
	}
	if evalHits(f) != 1 {
		t.Errorf("evaluator calls = %d, want exactly 1", evalHits(f))
	}
	if f.settle.n != 1 || f.settle.model != "evaluator-a" || f.settle.tokens != 1000 {
		t.Errorf("evaluation usage was not settled once on the evaluator model: %+v", f.settle)
	}
}

func TestApplyDecisionRouting_ResponsesFormat(t *testing.T) {
	f := setupDecision(t, 0, true)
	f.seedPolicy(twoWayPolicy(true, "simple", "cheap deep work"))
	w := f.do("/v1/responses", `{"model":"smart","input":"hello"}`)
	if got := w.Header().Get(HeaderRoutedModel); got != "model-b" {
		t.Fatalf("%s = %q, want model-b; body=%s", HeaderRoutedModel, got, w.Body)
	}
	if gjson.Get(w.Body.String(), "body_model").String() != "model-b" {
		t.Fatalf("responses body not rewritten: %s", w.Body)
	}
}

func TestApplyDecisionRouting_LowConfidenceFallsBackToDefault(t *testing.T) {
	f := setupDecision(t, 0, true)
	f.seedPolicy(twoWayPolicy(true, "vague simple questions", "hard problems"))
	w := f.do("/v1/chat/completions", chatBody)
	if got := w.Header().Get(HeaderRoutingReason); got != "decision:low_confidence" {
		t.Fatalf("reason = %q; body=%s", got, w.Body)
	}
	if got := w.Header().Get(HeaderRoutedModel); got != "model-b" {
		t.Fatalf("low confidence must go to the default candidate (strong -> model-b), got %q", got)
	}
	if f.settle.n != 1 {
		t.Errorf("a low-confidence evaluation is still billed, settle calls = %d", f.settle.n)
	}
}

func TestApplyDecisionRouting_MinConfidenceZeroAcceptsAnything(t *testing.T) {
	f := setupDecision(t, 0, true)
	p := twoWayPolicy(true, "vague simple questions", "hard problems")
	p.MinConfidence = 0
	f.seedPolicy(p)
	w := f.do("/v1/chat/completions", chatBody)
	if got := w.Header().Get(HeaderRoutingReason); got != "decision:applied" {
		t.Fatalf("min_confidence 0 should apply the 0.4 pick, reason = %q", got)
	}
}

func TestApplyDecisionRouting_EvaluatorUnavailable(t *testing.T) {
	t.Run("no channel for the evaluator model", func(t *testing.T) {
		f := setupDecision(t, 0, false)
		f.seedPolicy(twoWayPolicy(true, "cheap", "hard"))
		w := f.do("/v1/chat/completions", chatBody)
		if got := w.Header().Get(HeaderRoutingReason); got != "decision:evaluator_unavailable" {
			t.Fatalf("reason = %q", got)
		}
		if got := w.Header().Get(HeaderRoutedModel); got != "model-b" {
			t.Fatalf("must fall back to the default candidate, got %q", got)
		}
		if f.settle.n != 0 {
			t.Error("nothing was evaluated, nothing may be billed")
		}
	})
	t.Run("evaluator slower than the deadline", func(t *testing.T) {
		t.Setenv("ROUTING_DECISION_TIMEOUT_MS", "40")
		f := setupDecision(t, 400*time.Millisecond, true)
		f.seedPolicy(twoWayPolicy(true, "cheap", "hard"))
		start := time.Now()
		w := f.do("/v1/chat/completions", chatBody)
		if got := w.Header().Get(HeaderRoutingReason); got != "decision:evaluator_unavailable" {
			t.Fatalf("reason = %q", got)
		}
		if time.Since(start) > 350*time.Millisecond {
			t.Fatalf("the request waited %v for a slow evaluator", time.Since(start))
		}
		if ev := f.audit.byAction(governance.ActionRoutingDecision); len(ev) != 1 || !strings.Contains(ev[0].Details, `"error_kind":"timeout"`) {
			t.Fatalf("timeout not audited as such: %+v", ev)
		}
	})
}

func TestApplyDecisionRouting_IneligibleRequestsAreNeverEvaluated(t *testing.T) {
	f := setupDecision(t, 0, true)
	f.seedPolicy(twoWayPolicy(true, "cheap", "hard"))
	for name, body := range map[string]string{
		"tools":       `{"model":"smart","tools":[{"type":"function","function":{"name":"f"}}],"messages":[{"role":"user","content":"x"}]}`,
		"multi turn":  `{"model":"smart","messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"},{"role":"user","content":"c"}]}`,
		"reasoning":   `{"model":"smart","reasoning_effort":"high","messages":[{"role":"user","content":"x"}]}`,
		"pinned":      `{"model":"smart","metadata":{"pin:route":"1"},"messages":[{"role":"user","content":"x"}]}`,
		"audited run": `{"model":"smart","metadata":{"audit":"true"},"messages":[{"role":"user","content":"x"}]}`,
	} {
		w := f.do("/v1/chat/completions", body)
		if got := w.Header().Get(HeaderRoutingReason); got != "decision:ineligible" {
			t.Errorf("%s: reason = %q, want decision:ineligible", name, got)
		}
		if got := w.Header().Get(HeaderRoutedModel); got != "smart" {
			t.Errorf("%s: routed model = %q, the request must stay on the requested model", name, got)
		}
		if gjson.Get(w.Body.String(), "body_model").String() != "smart" {
			t.Errorf("%s: body was rewritten", name)
		}
	}
	if evalHits(f) != 0 {
		t.Fatalf("an ineligible request reached the evaluator %d times", evalHits(f))
	}
	if len(f.audit.byAction(governance.ActionRoutingDecision)) != 0 {
		t.Error("ineligible decisions are header+metric only, not audited")
	}
}

func TestApplyDecisionRouting_SessionAffinityIsIneligible(t *testing.T) {
	f := setupDecision(t, 0, true)
	f.seedPolicy(twoWayPolicy(true, "cheap", "hard"))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(chatBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Session-Id", "conv-1")
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	if got := w.Header().Get(HeaderRoutingReason); got != "decision:ineligible" {
		t.Fatalf("reason = %q", got)
	}
}

func TestApplyDecisionRouting_SingleCandidateSkipsEvaluator(t *testing.T) {
	f := setupDecision(t, 0, true)
	p := twoWayPolicy(true, "cheap", "hard")
	p.Candidates = p.Candidates[:1]
	p.DefaultCandidate = "cheap"
	f.seedPolicy(p)
	w := f.do("/v1/chat/completions", chatBody)
	if got := w.Header().Get(HeaderRoutingReason); got != "decision:single_candidate" {
		t.Fatalf("reason = %q", got)
	}
	if got := w.Header().Get(HeaderRoutedModel); got != "model-a" {
		t.Fatalf("routed = %q", got)
	}
	if evalHits(f) != 0 {
		t.Fatal("a single candidate must not be evaluated")
	}
}

func TestApplyDecisionRouting_TokenModelLimitNarrowsCandidates(t *testing.T) {
	f := setupDecision(t, 0, true)
	f.limit = map[string]bool{"smart": true, "model-a": true} // model-b is not allowed
	f.seedPolicy(twoWayPolicy(true, "hard", "cheap deep work"))
	w := f.do("/v1/chat/completions", chatBody)
	if got := w.Header().Get(HeaderRoutedModel); got != "model-a" {
		t.Fatalf("a rewrite reached a model the token may not use: %q (reason %s)", got, w.Header().Get(HeaderRoutingReason))
	}
	if got := w.Header().Get(HeaderRoutingReason); got != "decision:single_candidate" {
		t.Fatalf("reason = %q", got)
	}
}

func TestApplyDecisionRouting_NoPolicyIsInvisibleAndTouchesNoDB(t *testing.T) {
	f := setupDecision(t, 0, true)
	w := f.do("/v1/chat/completions", chatBody)
	assertUntouched(t, w)
	// A disabled policy behaves exactly like no policy.
	f.seedPolicy(twoWayPolicy(false, "cheap", "hard"))
	assertUntouched(t, f.do("/v1/chat/completions", chatBody))

	// Closed state is a memory lookup: with the database gone, requests inside
	// the index TTL still pass untouched.
	sqlDB, err := repo.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close()
	for i := 0; i < 5; i++ {
		assertUntouched(t, f.do("/v1/chat/completions", chatBody))
	}
	if evalHits(f) != 0 || f.settle.n != 0 {
		t.Fatalf("closed state evaluated or billed: hits=%d settle=%d", evalHits(f), f.settle.n)
	}
}

func assertUntouched(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if w.Header().Get(HeaderRoutedModel) != "" || w.Header().Get(HeaderRoutingReason) != "" {
		t.Fatalf("routing headers present without an enabled policy: %v", w.Header())
	}
	if gjson.Get(w.Body.String(), "body_model").String() != "smart" || gjson.Get(w.Body.String(), "model").String() != "smart" {
		t.Fatalf("request modified without an enabled policy: %s", w.Body)
	}
}

func TestApplyDecisionRouting_OtherWireFormatsAreSkipped(t *testing.T) {
	f := setupDecision(t, 0, true)
	f.seedPolicy(twoWayPolicy(true, "cheap", "hard"))
	w := f.do("/v1/messages", `{"model":"smart","messages":[{"role":"user","content":"hi"}]}`)
	assertUntouched(t, w)
	if evalHits(f) != 0 {
		t.Fatal("an unsupported wire format reached the evaluator")
	}
}

func TestApplyDecisionRouting_AuditCarriesNoUserText(t *testing.T) {
	f := setupDecision(t, 0, true)
	f.seedPolicy(twoWayPolicy(true, "cheap simple questions", "hard problems"))
	f.do("/v1/chat/completions", chatBody)
	evs := f.audit.byAction(governance.ActionRoutingDecision)
	if len(evs) != 1 {
		t.Fatalf("routing.decision events = %d, want 1", len(evs))
	}
	d := evs[0].Details
	for _, banned := range []string{"passport", "12345", "summarise", "my secret"} {
		if strings.Contains(d, banned) {
			t.Fatalf("user text %q leaked into the audit chain: %s", banned, d)
		}
	}
	for path, want := range map[string]string{
		"reason": "applied", "requested_model": "smart", "routed_model": "model-a",
		"candidate_id": "cheap", "choice": "cheap", "evaluator_model": "evaluator-a",
	} {
		if got := gjson.Get(d, path).String(); got != want {
			t.Errorf("audit %s = %q, want %q (%s)", path, got, want, d)
		}
	}
	if gjson.Get(d, "confidence").Float() != 0.8 || gjson.Get(d, "probabilities.cheap").Float() != 0.8 {
		t.Errorf("confidence / distribution missing: %s", d)
	}
	if gjson.Get(d, "eval_input_tokens").Int() != 1000 || gjson.Get(d, "latency_ms").Int() < 0 {
		t.Errorf("evaluation usage / latency missing: %s", d)
	}
}
