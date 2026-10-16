package middleware

// fallback_routing_test.go - the "fallback" routing strategy end to end:
// policy index (sqlite), the real channel cache, the real eligibility guard,
// and a fake breaker view standing in for handler's channelBreakers.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/app/relay/helper"
	"github.com/LurusTech/lurus-hub/internal/app/routingfallback"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/tidwall/gjson"
)

const fbPrimary = "primary-a"

// fbWorld seeds one shared channel per model in group "default" and installs a
// breaker view that treats the listed channel ids as open.
type fbWorld struct {
	f    *drFixture
	open map[int]bool
}

var fbChannelIDs = map[string]int{fbPrimary: 9811, "model-a": 9812, "model-b": 9813}

// newFallbackWorld builds the fixture, seeds channels for models and rebuilds
// the channel cache. Models absent from the list have no channel at all.
func newFallbackWorld(t *testing.T, models ...string) *fbWorld {
	t.Helper()
	f := setupDecision(t, 0, false)
	for _, m := range models {
		id := fbChannelIDs[m]
		weight, priority := uint(100), int64(0)
		ch := &repo.Channel{
			Id: id, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "ch-" + m,
			Key: "k", Models: m, Group: "default", TenantId: "default", Weight: &weight, Priority: &priority,
		}
		if err := repo.DB.Create(ch).Error; err != nil {
			t.Fatal(err)
		}
		if err := repo.DB.Create(&repo.Ability{Group: "default", Model: m, ChannelId: id, Enabled: true, Weight: weight, Priority: &priority}).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo.InitChannelCache()

	w := &fbWorld{f: f, open: map[int]bool{}}
	prev := routingfallback.BreakerAllOpen
	routingfallback.BreakerAllOpen = func(ids []int) (bool, time.Duration) {
		if len(ids) == 0 {
			return false, 0
		}
		for _, id := range ids {
			if !w.open[id] {
				return false, 0
			}
		}
		return true, 30 * time.Second
	}
	t.Cleanup(func() { routingfallback.BreakerAllOpen = prev })
	return w
}

func (w *fbWorld) trip(models ...string) {
	for _, m := range models {
		w.open[fbChannelIDs[m]] = true
	}
}

func fallbackPolicy(enabled bool, models ...string) entity.RoutingPolicy {
	p := entity.RoutingPolicy{PublicModel: fbPrimary, Strategy: entity.RoutingStrategyFallback, Enabled: enabled}
	for i, m := range models {
		p.Candidates = append(p.Candidates, entity.RoutingCandidate{ID: "c" + string(rune('a'+i)), Model: m})
	}
	return p
}

func fbChat(model string) string {
	return `{"model":"` + model + `","messages":[{"role":"user","content":"my secret passport 12345 please summarise"}]}`
}

// reasonCount reads lurus_routing_fallback_total{reason} from the default
// registry by its exposed name, so the test also pins the metric's name.
func reasonCount(reason string) float64 {
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		panic(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "lurus_routing_fallback_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "reason" && l.GetValue() == reason {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	panic("lurus_routing_fallback_total{reason=" + reason + "} is not registered")
}

func TestApplyFallbackRouting_EveryReason(t *testing.T) {
	cases := []struct {
		name       string
		channels   []string // models that have a channel
		trip       []string // models whose channels have an open breaker
		wantReason string
		wantModel  string
	}{
		{"primary healthy", []string{fbPrimary, "model-a"}, nil, "fallback:primary_ok", fbPrimary},
		{"primary has no channel", []string{"model-a"}, nil, "fallback:no_channel", "model-a"},
		{"primary breaker open", []string{fbPrimary, "model-a"}, []string{fbPrimary}, "fallback:primary_unavailable", "model-a"},
		{"first candidate also open -> second", []string{fbPrimary, "model-a", "model-b"}, []string{fbPrimary, "model-a"}, "fallback:primary_unavailable", "model-b"},
		{"first candidate has no channel -> second", []string{fbPrimary, "model-b"}, []string{fbPrimary}, "fallback:primary_unavailable", "model-b"},
		{"everything open", []string{fbPrimary, "model-a"}, []string{fbPrimary, "model-a"}, "fallback:no_candidate", fbPrimary},
		{"nothing exists", nil, nil, "fallback:no_candidate", fbPrimary},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newFallbackWorld(t, tc.channels...)
			w.trip(tc.trip...)
			w.f.seedPolicy(fallbackPolicy(true, "model-a", "model-b"))
			before := reasonCount(strings.TrimPrefix(tc.wantReason, "fallback:"))

			rec := w.f.do("/v1/chat/completions", fbChat(fbPrimary))
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			if got := rec.Header().Get(HeaderRoutingReason); got != tc.wantReason {
				t.Fatalf("%s = %q, want %q", HeaderRoutingReason, got, tc.wantReason)
			}
			if got := rec.Header().Get(HeaderRoutedModel); got != tc.wantModel {
				t.Fatalf("%s = %q, want %q", HeaderRoutedModel, got, tc.wantModel)
			}
			b := rec.Body.String()
			keys := []string{"model", "body_model", "original_model"}
			if tc.wantModel == fbPrimary {
				keys = keys[:2] // original_model is only written by a rewrite
			}
			for _, k := range keys {
				if got := gjson.Get(b, k).String(); got != tc.wantModel {
					t.Errorf("%s = %q, want %q (distributor, relay body and gin key must agree): %s", k, got, tc.wantModel, b)
				}
			}
			if gjson.Get(b, "content_length").Int() != gjson.Get(b, "body_len").Int() {
				t.Errorf("ContentLength %d != body length %d", gjson.Get(b, "content_length").Int(), gjson.Get(b, "body_len").Int())
			}
			if got := reasonCount(strings.TrimPrefix(tc.wantReason, "fallback:")); got != before+1 {
				t.Errorf("lurus_routing_fallback_total{reason=%q} moved %v -> %v, want +1", tc.wantReason, before, got)
			}
		})
	}
}

// A request that is not eligible for rewriting is announced as such and left
// alone even though the primary is down: a model swap under a tool loop or an
// audited run is never acceptable.
func TestApplyFallbackRouting_IneligibleRequestsAreNeverRewritten(t *testing.T) {
	w := newFallbackWorld(t, "model-a") // primary has no channel: would reroute if eligible
	w.f.seedPolicy(fallbackPolicy(true, "model-a"))
	for name, body := range map[string]string{
		"tools":       `{"model":"primary-a","tools":[{"type":"function","function":{"name":"f"}}],"messages":[{"role":"user","content":"x"}]}`,
		"tool_choice": `{"model":"primary-a","tool_choice":"auto","messages":[{"role":"user","content":"x"}]}`,
		"multi turn":  `{"model":"primary-a","messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"},{"role":"user","content":"c"}]}`,
		"reasoning":   `{"model":"primary-a","reasoning_effort":"high","messages":[{"role":"user","content":"x"}]}`,
		"pinned":      `{"model":"primary-a","metadata":{"pin:route":"1"},"messages":[{"role":"user","content":"x"}]}`,
		"audited":     `{"model":"primary-a","metadata":{"audit:true":"1"},"messages":[{"role":"user","content":"x"}]}`,
		"previous":    `{"model":"primary-a","previous_response_id":"resp_1","input":"x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			before := reasonCount("ineligible")
			path := "/v1/chat/completions"
			if name == "previous" {
				path = "/v1/responses"
			}
			rec := w.f.do(path, body)
			if got := rec.Header().Get(HeaderRoutingReason); got != "fallback:ineligible" {
				t.Fatalf("%s = %q, want fallback:ineligible; body=%s", HeaderRoutingReason, got, rec.Body)
			}
			if got := rec.Header().Get(HeaderRoutedModel); got != fbPrimary {
				t.Fatalf("%s = %q, want the requested model untouched", HeaderRoutedModel, got)
			}
			if gjson.Get(rec.Body.String(), "body_model").String() != fbPrimary {
				t.Fatalf("ineligible request body was rewritten: %s", rec.Body)
			}
			if reasonCount("ineligible") != before+1 {
				t.Error("ineligible outcome not counted")
			}
		})
	}

	t.Run("session affinity", func(t *testing.T) {
		rec := w.f.doWithHeader("/v1/chat/completions", fbChat(fbPrimary), "X-Session-Id", "conv-1")
		if got := rec.Header().Get(HeaderRoutingReason); got != "fallback:ineligible" {
			t.Fatalf("reason = %q", got)
		}
		if gjson.Get(rec.Body.String(), "body_model").String() != fbPrimary {
			t.Fatal("a pinned conversation was rewritten")
		}
	})
}

// Off-state: no policy, a disabled policy and an unsupported wire format all
// leave the request and the response byte-identical to today, and a fallback
// policy for another model is invisible too.
func TestApplyFallbackRouting_OffStateIsInvisible(t *testing.T) {
	w := newFallbackWorld(t, "model-a") // primary has no channel
	before := reasonCount("no_channel") + reasonCount("no_candidate") + reasonCount("primary_ok")

	assertUntouchedAs := func(rec interface {
		Header() http.Header
	}, body string) {
		t.Helper()
		if rec.Header().Get(HeaderRoutedModel) != "" || rec.Header().Get(HeaderRoutingReason) != "" {
			t.Fatalf("routing headers present in the off state: %v", rec.Header())
		}
		if gjson.Get(body, "body_model").String() != fbPrimary {
			t.Fatalf("request modified in the off state: %s", body)
		}
	}

	rec := w.f.do("/v1/chat/completions", fbChat(fbPrimary))
	assertUntouchedAs(rec, rec.Body.String())

	w.f.seedPolicy(fallbackPolicy(false, "model-a"))
	rec = w.f.do("/v1/chat/completions", fbChat(fbPrimary))
	assertUntouchedAs(rec, rec.Body.String())

	other := fallbackPolicy(true, "model-a")
	other.PublicModel = "some-other-model"
	w.f.seedPolicy(other)
	rec = w.f.do("/v1/chat/completions", fbChat(fbPrimary))
	assertUntouchedAs(rec, rec.Body.String())

	w.f.seedPolicy(fallbackPolicy(true, "model-a"))
	rec = w.f.do("/v1/messages", `{"model":"primary-a","messages":[{"role":"user","content":"hi"}]}`)
	assertUntouchedAs(rec, rec.Body.String())

	if after := reasonCount("no_channel") + reasonCount("no_candidate") + reasonCount("primary_ok"); after != before {
		t.Fatalf("off-state traffic was counted: %v -> %v", before, after)
	}

	// Closed state is a memory lookup: with the database gone, requests inside
	// the index TTL for a model with no policy still pass untouched.
	w.f.seedPolicy(fallbackPolicy(false, "model-a"))
	rec = w.f.do("/v1/chat/completions", fbChat(fbPrimary)) // loads the index
	assertUntouchedAs(rec, rec.Body.String())
	sqlDB, err := repo.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close()
	for i := 0; i < 3; i++ {
		rec = w.f.do("/v1/chat/completions", fbChat(fbPrimary))
		assertUntouchedAs(rec, rec.Body.String())
	}
}

// The rewrite happens before pricing, so billing follows the served model:
// the RelayInfo built from the context after routing carries the candidate,
// and the price helper reads the candidate's ratio, not the primary's.
func TestApplyFallbackRouting_BillingFollowsTheServedModel(t *testing.T) {
	w := newFallbackWorld(t, fbPrimary, "model-a")
	// After the fixture: it installs its own ratio table and restores the
	// original one on cleanup.
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"primary-a":1,"model-a":7}`); err != nil {
		t.Fatal(err)
	}
	w.trip(fbPrimary)
	w.f.seedPolicy(fallbackPolicy(true, "model-a"))

	var origin string
	var ratio float64
	w.f.after = func(c *gin.Context, mr *ModelRequest) {
		req := &dto.GeneralOpenAIRequest{Model: mr.Model}
		info := relaycommon.GenRelayInfoOpenAI(c, req)
		origin = info.OriginModelName
		pd, err := helper.ModelPriceHelper(c, info, 100, &types.TokenCountMeta{})
		if err != nil {
			t.Errorf("price: %v", err)
			return
		}
		ratio = pd.ModelRatio
	}
	rec := w.f.do("/v1/chat/completions", fbChat(fbPrimary))
	if got := rec.Header().Get(HeaderRoutedModel); got != "model-a" {
		t.Fatalf("not rerouted: %s / %s", got, rec.Body)
	}
	if origin != "model-a" {
		t.Fatalf("pricing model = %q, want the served model model-a", origin)
	}
	if ratio != 7 {
		t.Fatalf("priced at ratio %v, want model-a's 7 (primary-a is 1)", ratio)
	}
}

// A rewrite target must be a model the caller could have asked for directly.
func TestApplyFallbackRouting_RespectsTokenModelLimit(t *testing.T) {
	w := newFallbackWorld(t, "model-a", "model-b")
	w.f.limit = map[string]bool{fbPrimary: true, "model-b": true} // model-a not allowed
	w.f.seedPolicy(fallbackPolicy(true, "model-a", "model-b"))
	rec := w.f.do("/v1/chat/completions", fbChat(fbPrimary))
	if got := rec.Header().Get(HeaderRoutedModel); got != "model-b" {
		t.Fatalf("routed to %q, want model-b (model-a is outside the token's limit)", got)
	}
}

func TestApplyFallbackRouting_AuditCarriesNoUserText(t *testing.T) {
	w := newFallbackWorld(t, "model-a")
	w.f.seedPolicy(fallbackPolicy(true, "model-a"))
	w.f.do("/v1/chat/completions", fbChat(fbPrimary))
	evs := w.f.audit.byAction(governance.ActionRoutingDecision)
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
		"strategy": "fallback", "reason": "no_channel", "requested_model": fbPrimary,
		"routed_model": "model-a", "candidate_id": "ca",
	} {
		if got := gjson.Get(d, path).String(); got != want {
			t.Errorf("audit %s = %q, want %q (%s)", path, got, want, d)
		}
	}

	// primary_ok is the bulk of opted-in traffic and is not audited.
	w2 := newFallbackWorld(t, fbPrimary, "model-a")
	w2.f.seedPolicy(fallbackPolicy(true, "model-a"))
	w2.f.do("/v1/chat/completions", fbChat(fbPrimary))
	if n := len(w2.f.audit.byAction(governance.ActionRoutingDecision)); n != 0 {
		t.Fatalf("primary_ok produced %d audit events, want 0", n)
	}
}
