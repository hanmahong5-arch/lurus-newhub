package handler

// relay_systemone_test.go - hermetic relay-level tests for POST /v1/systemone.
// They drive the REAL middleware chain (TokenAuth -> Distribute -> Relay)
// against httptest upstreams standing in for the hosted API and a
// self-hosted server, and assert what the caller sees, what the upstream saw,
// what the user was charged (exact quota), and what the breaker learned.
//
// Price under test: $0.042 per 1M input tokens, output free. Ratio 1 is
// $2 / 1M tokens, so ratio 0.021 and 1000 input tokens settle at exactly
// 21 quota (1 quota == 1 token at ratio 1, group ratio 1).

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/model_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

const soValidBody = `{"model":"jev-latest","state":{"body":"I was billed twice"},` +
	`"questions":{"dept":{"type":"choice","instructions":"which department?",` +
	`"criteria":{"billing":"refunds and charges","tech":"bugs and outages"}}}}`

// soUpstream is one fake System One server plus what it observed.
type soUpstream struct {
	srv     *httptest.Server
	hits    atomic.Int32
	mu      sync.Mutex
	headers http.Header
	path    string
	body    []byte
	// inFlight runs inside the upstream while the call is pending, i.e. while
	// only the pre-consumed hold has left the wallet.
	inFlight func()
}

func (u *soUpstream) setInFlight(fn func()) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.inFlight = fn
}

func (u *soUpstream) lastRequest() (http.Header, string, []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.headers, u.path, u.body
}

// soChannelSpec describes one seeded channel.
type soChannelSpec struct {
	channelType int
	priority    int64
	models      string
	key         string
	handler     http.HandlerFunc
}

type soHarness struct {
	*relaySuccessCtx
	upstreams []*soUpstream
	channels  []*repo.Channel
}

// soOKHandler answers with the hosted wire shape and the given usage.
func soOKHandler(inputTokens, outputTokens int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"model":"jev-1.13.0","answers":{"dept":{"type":"choice","choice":"billing",`+
			`"probabilities":{"billing":0.99,"tech":0.01},"confidence":0.9}},`+
			`"usage":{"input_tokens":%d,"output_tokens":%d}}`, inputTokens, outputTokens)
	}
}

func soStatusHandler(status int, body string, headers map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// soDropConnection closes the TCP connection without answering.
func soDropConnection(w http.ResponseWriter, r *http.Request) {
	if hj, ok := w.(http.Hijacker); ok {
		if conn, _, err := hj.Hijack(); err == nil {
			_ = conn.Close()
		}
	}
}

// setupSystemOneRouter reuses the single-channel harness for its DB, user,
// token and cleanup, replaces its channel with the given ones, and mounts
// POST /v1/systemone on the same middleware chain the real router uses.
func setupSystemOneRouter(t *testing.T, specs ...soChannelSpec) *soHarness {
	t.Helper()
	ctx := setupRelaySuccessRouter(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the fixture's own upstream must not be reached")
	})
	// The fixture never builds the ratio map; boot does (idempotent, as in
	// governance_savings_test.go).
	ratio_setting.InitRatioSettings()
	// The fixture seeded one channel of its own; this suite wants only its own.
	if err := ctx.db.Where("1 = 1").Delete(&repo.Ability{}).Error; err != nil {
		t.Fatalf("clear abilities: %v", err)
	}
	if err := ctx.db.Delete(&repo.Channel{}, ctx.channel.Id).Error; err != nil {
		t.Fatalf("clear fixture channel: %v", err)
	}

	h := &soHarness{relaySuccessCtx: ctx}
	weight := uint(10)
	for i, spec := range specs {
		up := &soUpstream{}
		handler := spec.handler
		up.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			up.hits.Add(1)
			body, _ := io.ReadAll(r.Body)
			up.mu.Lock()
			up.headers, up.path, up.body = r.Header.Clone(), r.URL.Path, body
			fn := up.inFlight
			up.mu.Unlock()
			if fn != nil {
				fn()
			}
			handler(w, r)
		}))
		t.Cleanup(up.srv.Close)

		priority := spec.priority
		baseURL := up.srv.URL
		ch := &repo.Channel{
			// Unique high ids: channelBreakers is a process-global registry
			// keyed by channel id (see setupRelaySuccessRouter).
			Id:       ctx.channel.Id + 1 + i,
			TenantId: "default", Type: spec.channelType, Key: spec.key,
			Status: common.ChannelStatusEnabled, Name: fmt.Sprintf("systemone-%d", i),
			BaseURL: &baseURL, Models: spec.models, Group: "default",
			Weight: &weight, Priority: &priority,
		}
		if err := ctx.db.Create(ch).Error; err != nil {
			t.Fatalf("seed channel %d: %v", i, err)
		}
		if err := ch.AddAbilities(nil); err != nil {
			t.Fatalf("abilities for channel %d: %v", i, err)
		}
		h.upstreams = append(h.upstreams, up)
		h.channels = append(h.channels, ch)
	}

	grp := ctx.router.Group("/v1")
	grp.Use(middleware.StampRelayFormat(), middleware.TokenAuth(), middleware.Distribute())
	grp.POST("/systemone", func(c *gin.Context) {
		Relay(c, types.RelayFormatSystemOne)
	})
	return h
}

func hostedChannel(priority int64, key string, handler http.HandlerFunc) soChannelSpec {
	return soChannelSpec{channelType: constant.ChannelTypeTypeSafe, priority: priority, models: "jev-latest", key: key, handler: handler}
}

func (h *soHarness) post(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.token.Key)
	return h.serve(req)
}

func (h *soHarness) userQuota(t *testing.T) int {
	t.Helper()
	var u repo.User
	if err := h.db.First(&u, h.user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	return u.Quota
}

func (h *soHarness) consumeLogs(t *testing.T) []repo.Log {
	t.Helper()
	var logs []repo.Log
	if err := h.db.Where("type = ?", repo.LogTypeConsume).Find(&logs).Error; err != nil {
		t.Fatalf("query consume logs: %v", err)
	}
	return logs
}

func breakerFails(channelID int) int {
	for _, s := range channelBreakers.Snapshot() {
		if s.ChannelID == channelID {
			return s.ConsecutiveFails
		}
	}
	return 0
}

// holdQuota puts the user under the trust threshold (10 x QuotaPerUnit), above
// which pre-consume is skipped on purpose; the fixture's wallet is far above
// it, so tests that observe the in-flight hold must start from a smaller one.
func (h *soHarness) holdQuota(t *testing.T) {
	t.Helper()
	if err := h.db.Model(&repo.User{}).Where("id = ?", h.user.Id).Update("quota", 1000000).Error; err != nil {
		t.Fatalf("shrink user quota: %v", err)
	}
	if h.userQuota(t) > common.GetTrustQuota() {
		t.Fatalf("user quota still above the trust threshold %d", common.GetTrustQuota())
	}
}

// (a) A successful call is billed on input tokens only: 2000 reported
// input tokens at ratio 0.021 is exactly 42 quota, however many output tokens
// the upstream reports, and the quota leaves the user exactly once.
func TestSystemOne_Success_BilledPerInputTokenOnly(t *testing.T) {
	h := setupSystemOneRouter(t, hostedChannel(0, "sk-upstream-aaaaaaaa", soOKHandler(2000, 777)))
	before := h.userQuota(t)

	w := h.post(t, soValidBody)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Model   string                     `json:"model"`
		Answers map[string]json.RawMessage `json:"answers"`
		Usage   struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not the TypeSafe shape: %v; body=%s", err, w.Body.String())
	}
	if resp.Model != "jev-1.13.0" || len(resp.Answers) != 1 || resp.Usage.InputTokens != 2000 {
		t.Errorf("response = %+v, want model jev-1.13.0, 1 answer, 2000 input tokens", resp)
	}

	const wantQuota = 42 // 2000 * 0.021
	if got := before - h.userQuota(t); got != wantQuota {
		t.Errorf("user charged %d quota, want exactly %d (2000 input tokens x 0.021; output tokens are free)", got, wantQuota)
	}
	logs := h.consumeLogs(t)
	if len(logs) != 1 {
		t.Fatalf("consume log rows = %d, want 1", len(logs))
	}
	if logs[0].Quota != wantQuota || logs[0].PromptTokens != 2000 || logs[0].CompletionTokens != 0 {
		t.Errorf("log quota/prompt/completion = %d/%d/%d, want %d/2000/0", logs[0].Quota, logs[0].PromptTokens, logs[0].CompletionTokens, wantQuota)
	}
	if logs[0].ChannelType != constant.ChannelTypeTypeSafe {
		t.Errorf("log ChannelType = %d, want %d", logs[0].ChannelType, constant.ChannelTypeTypeSafe)
	}
	if got := breakerFails(h.channels[0].Id); got != 0 {
		t.Errorf("breaker consecutive fails = %d after a success, want 0", got)
	}
}

// (b) Pre-consume holds a non-zero estimate while the upstream works, and
// settlement replaces it with the real charge: the 500-token pre-consume floor
// at ratio 0.021 holds 10 quota, then 2000 reported input tokens settle at 42.
func TestSystemOne_PreConsumeHoldsEstimateThenSettlementCorrectsIt(t *testing.T) {
	h := setupSystemOneRouter(t, hostedChannel(0, "sk-upstream-rrrrrrrr", soOKHandler(2000, 777)))
	h.holdQuota(t)
	before := h.userQuota(t)
	var held atomic.Int64
	h.upstreams[0].setInFlight(func() { held.Store(int64(before - h.userQuota(t))) })

	w := h.post(t, soValidBody)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	const wantHold, wantFinal = 10, 42
	if got := held.Load(); got != wantHold {
		t.Errorf("in-flight hold = %d quota, want exactly %d (pre-consume floor 500 x 0.021, never zero)", got, wantHold)
	}
	if got := before - h.userQuota(t); got != wantFinal {
		t.Errorf("settled charge = %d quota, want exactly %d (the hold corrected to 2000 x 0.021)", got, wantFinal)
	}
}

// What reaches the upstream: the channel's own key as Bearer (never the
// caller's hub key), the real path, and the mapped upstream model.
func TestSystemOne_UpstreamSeesChannelKeyAndMappedModel(t *testing.T) {
	spec := hostedChannel(0, "sk-upstream-bbbbbbbb", soOKHandler(1000, 0))
	h := setupSystemOneRouter(t, spec)
	mapping := `{"jev-latest":"jev-1.13.0"}`
	if err := h.db.Model(&repo.Channel{}).Where("id = ?", h.channels[0].Id).Update("model_mapping", mapping).Error; err != nil {
		t.Fatalf("set model mapping: %v", err)
	}

	w := h.post(t, soValidBody)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	hdr, path, body := h.upstreams[0].lastRequest()
	if path != "/v1/systemone" {
		t.Errorf("upstream path = %q, want /v1/systemone", path)
	}
	if got := hdr.Get("Authorization"); got != "Bearer sk-upstream-bbbbbbbb" {
		t.Errorf("upstream Authorization = %q, want the channel key as Bearer", got)
	}
	if strings.Contains(hdr.Get("Authorization"), h.token.Key) {
		t.Error("the caller's hub key leaked to the upstream")
	}
	var sent struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &sent); err != nil || sent.Model != "jev-1.13.0" {
		t.Errorf("upstream model = %q (err %v), want the mapped jev-1.13.0; body=%s", sent.Model, err, body)
	}
}

// Global pass-through must not reach a System One upstream: the raw body would
// carry the public model name unmapped (a self-hosted server silently
// auto-routes names it does not know) and fields the hosted API never takes.
func TestSystemOne_PassThroughSettingStillMapsAndFilters(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	orig := settings.PassThroughRequestEnabled
	settings.PassThroughRequestEnabled = true
	t.Cleanup(func() { settings.PassThroughRequestEnabled = orig })

	h := setupSystemOneRouter(t, hostedChannel(0, "sk-upstream-cccccccc", soOKHandler(1000, 0)))
	if err := h.db.Model(&repo.Channel{}).Where("id = ?", h.channels[0].Id).Update("model_mapping", `{"jev-latest":"jev-1.13.0"}`).Error; err != nil {
		t.Fatalf("set model mapping: %v", err)
	}
	w := h.post(t, strings.Replace(soValidBody, "{", `{"temperature":0.2,`, 1))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	_, _, body := h.upstreams[0].lastRequest()
	var sent map[string]json.RawMessage
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("upstream body: %v; %s", err, body)
	}
	if string(sent["model"]) != `"jev-1.13.0"` {
		t.Errorf("upstream model = %s, want the mapped jev-1.13.0", sent["model"])
	}
	if _, ok := sent["temperature"]; ok {
		t.Errorf("unknown field reached the hosted upstream: %s", body)
	}
}

// A self-hosted channel (type 58) serves a laya name at the same price and
// the caller sees the public model name, not an internal checkpoint.
func TestSystemOne_SelfHostedChannelServesAndBills(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"laya-rl-agent","answers":{"dept":{"type":"choice","choice":"billing",`+
			`"probabilities":{"billing":1},"confidence":1}},"usage":{"input_tokens":1000,"output_tokens":0},`+
			`"routing":{"model":"english"}}`)
	}
	h := setupSystemOneRouter(t, soChannelSpec{
		channelType: constant.ChannelTypeSystemOneCompatible, priority: 0,
		models: "laya-auto", key: "", handler: handler,
	})
	before := h.userQuota(t)

	w := h.post(t, strings.Replace(soValidBody, "jev-latest", "laya-auto", 1))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"model":"laya-auto"`) {
		t.Errorf("body = %s, want the public model name laya-auto", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "routing") {
		t.Errorf("upstream-only fields leaked to the caller: %s", w.Body.String())
	}
	if got := before - h.userQuota(t); got != 21 {
		t.Errorf("user charged %d, want exactly 21 (1000 x 0.021)", got)
	}
}

// (c) A rejected channel credential is OUR configuration fault: the relay
// fails over to the next channel and the caller never learns of a 401.
func TestSystemOne_UpstreamAuthRejected_FailsOver(t *testing.T) {
	for _, status := range []int{401, 403, 404, 405} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			bad := hostedChannel(10, "sk-upstream-cccccccc", soStatusHandler(status, `{"detail":"Invalid API key"}`, nil))
			good := hostedChannel(0, "sk-upstream-dddddddd", soOKHandler(1000, 0))
			h := setupSystemOneRouter(t, bad, good)
			before := h.userQuota(t)

			w := h.post(t, soValidBody)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 from the healthy channel; body=%s", w.Code, w.Body.String())
			}
			if h.upstreams[0].hits.Load() != 1 || h.upstreams[1].hits.Load() != 1 {
				t.Errorf("hits bad/good = %d/%d, want 1/1 (fail over exactly once)", h.upstreams[0].hits.Load(), h.upstreams[1].hits.Load())
			}
			if got := before - h.userQuota(t); got != 21 {
				t.Errorf("user charged %d, want exactly 21: only the successful attempt bills", got)
			}
			if got := breakerFails(h.channels[0].Id); got != 1 {
				t.Errorf("rejected channel breaker consecutive fails = %d, want 1: a dead credential is a channel failure", got)
			}
			if got := breakerFails(h.channels[1].Id); got != 0 {
				t.Errorf("healthy channel breaker consecutive fails = %d, want 0", got)
			}
		})
	}
}

// (c) With no channel to fail over to, the caller still must not see the
// upstream's 401/403: that reads as "your hub key is wrong".
func TestSystemOne_UpstreamAuthRejected_NeverSurfacedAsCallerAuthError(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			key := "sk-upstream-eeeeeeee"
			h := setupSystemOneRouter(t, hostedChannel(0, key,
				soStatusHandler(status, `{"detail":"bad key `+key+`"}`, nil)))
			before := h.userQuota(t)

			w := h.post(t, soValidBody)
			if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden {
				t.Fatalf("status = %d: the upstream credential failure was surfaced to the caller as an auth error", w.Code)
			}
			if w.Code < 500 {
				t.Errorf("status = %d, want a 5xx gateway error", w.Code)
			}
			if strings.Contains(w.Body.String(), key) {
				t.Errorf("the channel key leaked into the response: %s", w.Body.String())
			}
			if h.userQuota(t) != before {
				t.Errorf("user quota changed by %d on a failed call", before-h.userQuota(t))
			}
			if n := len(h.consumeLogs(t)); n != 0 {
				t.Errorf("consume log rows = %d, want 0", n)
			}
		})
	}
}

// A dropped connection is a channel problem too: fail over, bill once.
func TestSystemOne_ConnectionError_FailsOver(t *testing.T) {
	h := setupSystemOneRouter(t,
		hostedChannel(10, "sk-upstream-ffffffff", soDropConnection),
		hostedChannel(0, "sk-upstream-gggggggg", soOKHandler(1000, 0)))
	before := h.userQuota(t)

	w := h.post(t, soValidBody)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after failing over; body=%s", w.Code, w.Body.String())
	}
	if h.upstreams[1].hits.Load() != 1 {
		t.Errorf("healthy channel hits = %d, want 1", h.upstreams[1].hits.Load())
	}
	if got := before - h.userQuota(t); got != 21 {
		t.Errorf("user charged %d, want exactly 21", got)
	}
}

// (d) The caller's own mistake (upstream 400/413/422): same status back, the
// explanation kept but the channel key redacted, NO retry on another channel,
// NO breaker failure, NO charge.
func TestSystemOne_CallerFault_PassedThroughWithoutRetryOrBreakerFailure(t *testing.T) {
	for _, status := range []int{400, 413, 422} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			key := "sk-upstream-hhhhhhhh"
			detail := fmt.Sprintf(`{"detail":"question 'dept': criteria is malformed (key %s)"}`, key)
			h := setupSystemOneRouter(t,
				hostedChannel(10, key, soStatusHandler(status, detail, nil)),
				hostedChannel(0, "sk-upstream-iiiiiiii", soOKHandler(1000, 0)))
			h.holdQuota(t)
			before := h.userQuota(t)
			var held atomic.Int64
			h.upstreams[0].setInFlight(func() { held.Store(int64(before - h.userQuota(t))) })

			w := h.post(t, soValidBody)
			if w.Code != status {
				t.Fatalf("status = %d, want the upstream's %d passed through; body=%s", w.Code, status, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "question 'dept'") {
				t.Errorf("body = %s, want the upstream's explanation naming the question", w.Body.String())
			}
			if strings.Contains(w.Body.String(), key) {
				t.Errorf("the channel key was echoed to the caller: %s", w.Body.String())
			}
			if h.upstreams[0].hits.Load() != 1 || h.upstreams[1].hits.Load() != 0 {
				t.Errorf("hits first/second = %d/%d, want 1/0: a caller's bad request must not be retried elsewhere", h.upstreams[0].hits.Load(), h.upstreams[1].hits.Load())
			}
			if got := breakerFails(h.channels[0].Id); got != 0 {
				t.Errorf("breaker consecutive fails = %d, want 0: the caller's fault must not trip a healthy channel", got)
			}
			if held.Load() != 10 {
				t.Errorf("in-flight hold = %d, want 10: the test must observe a pre-consume that is then released", held.Load())
			}
			if h.userQuota(t) != before {
				t.Errorf("user charged %d for a rejected request, want 0", before-h.userQuota(t))
			}
			if n := len(h.consumeLogs(t)); n != 0 {
				t.Errorf("consume log rows = %d, want 0", n)
			}
		})
	}
}

// (e) Overload (429/503/529): retried on another channel, and when there is
// none the caller gets a retryable status with the upstream's Retry-After.
func TestSystemOne_Overload_FailsOverAndPropagatesRetryAfter(t *testing.T) {
	for _, status := range []int{429, 503, 529} {
		t.Run(fmt.Sprintf("failover_%d", status), func(t *testing.T) {
			h := setupSystemOneRouter(t,
				hostedChannel(10, "sk-upstream-jjjjjjjj", soStatusHandler(status, `{"detail":"busy"}`, map[string]string{"Retry-After": "7"})),
				hostedChannel(0, "sk-upstream-kkkkkkkk", soOKHandler(1000, 0)))
			w := h.post(t, soValidBody)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 from the second channel; body=%s", w.Code, w.Body.String())
			}
			if h.upstreams[1].hits.Load() != 1 {
				t.Errorf("second channel hits = %d, want 1", h.upstreams[1].hits.Load())
			}
		})
		t.Run(fmt.Sprintf("sole_%d", status), func(t *testing.T) {
			h := setupSystemOneRouter(t,
				hostedChannel(0, "sk-upstream-llllllll", soStatusHandler(status, `{"detail":"busy"}`, map[string]string{"Retry-After": "7"})))
			before := h.userQuota(t)

			w := h.post(t, soValidBody)
			wantStatus := status
			if status == 529 {
				wantStatus = http.StatusServiceUnavailable
			}
			if w.Code != wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, wantStatus, w.Body.String())
			}
			if got := w.Header().Get("Retry-After"); got != "7" {
				t.Errorf("Retry-After = %q, want the upstream's 7", got)
			}
			if h.userQuota(t) != before {
				t.Errorf("user charged %d on an overloaded upstream, want 0", before-h.userQuota(t))
			}
		})
	}
}

// (f) Any other upstream 5xx is the upstream's failure: it fails over and
// counts toward the breaker.
func TestSystemOne_Upstream5xx_FailsOverAndCountsAgainstBreaker(t *testing.T) {
	h := setupSystemOneRouter(t,
		hostedChannel(10, "sk-upstream-mmmmmmmm", soStatusHandler(500, `{"detail":"boom"}`, nil)),
		hostedChannel(0, "sk-upstream-nnnnnnnn", soOKHandler(1000, 0)))

	w := h.post(t, soValidBody)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after failing over; body=%s", w.Code, w.Body.String())
	}
	if got := breakerFails(h.channels[0].Id); got != 1 {
		t.Errorf("failing channel breaker consecutive fails = %d, want 1", got)
	}
	if got := breakerFails(h.channels[1].Id); got != 0 {
		t.Errorf("healthy channel breaker consecutive fails = %d, want 0", got)
	}
}

// (g) A body that fails validation is the caller's 400 and never reaches an
// upstream or the wallet.
func TestSystemOne_InvalidRequest_Is400WithoutUpstreamOrCharge(t *testing.T) {
	cases := map[string]string{
		"invalid json":        `{"model":`,
		"missing state":       `{"model":"jev-latest","questions":{"q":{"type":"noul","instructions":"x"}}}`,
		"empty questions":     `{"model":"jev-latest","state":"x","questions":{}}`,
		"batch states":        `{"model":"jev-latest","states":["a"],"questions":{"q":{"type":"noul","instructions":"x"}}}`,
		"hooks":               `{"model":"jev-latest","state":"x","hooks":[],"questions":{"q":{"type":"noul","instructions":"x"}}}`,
		"unknown type":        `{"model":"jev-latest","state":"x","questions":{"q":{"type":"bogus","instructions":"x"}}}`,
		"missing instruction": `{"model":"jev-latest","state":"x","questions":{"q":{"type":"noul"}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			h := setupSystemOneRouter(t, hostedChannel(0, "sk-upstream-oooooooo", soOKHandler(1000, 0)))
			before := h.userQuota(t)

			w := h.post(t, body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
			}
			if h.upstreams[0].hits.Load() != 0 {
				t.Errorf("upstream hits = %d, want 0 for an invalid request", h.upstreams[0].hits.Load())
			}
			if h.userQuota(t) != before {
				t.Errorf("user charged %d for an invalid request, want 0", before-h.userQuota(t))
			}
		})
	}
}

// (h) Streaming is not a thing here: a stream flag in the body is ignored and
// the answer is one JSON document.
func TestSystemOne_StreamFlagIgnored(t *testing.T) {
	h := setupSystemOneRouter(t, hostedChannel(0, "sk-upstream-pppppppp", soOKHandler(1000, 0)))
	body := strings.Replace(soValidBody, `{"model"`, `{"stream":true,"model"`, 1)

	w := h.post(t, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json (never an event stream)", ct)
	}
	if !json.Valid(w.Body.Bytes()) {
		t.Errorf("body is not a single JSON document: %s", w.Body.String())
	}
}

// A model no channel serves never reaches an upstream.
func TestSystemOne_UnknownModel_NoUpstream(t *testing.T) {
	h := setupSystemOneRouter(t, hostedChannel(0, "sk-upstream-qqqqqqqq", soOKHandler(1000, 0)))
	w := h.post(t, strings.Replace(soValidBody, "jev-latest", "no-such-model", 1))
	if w.Code < 400 {
		t.Fatalf("status = %d, want an error for an unserved model", w.Code)
	}
	if h.upstreams[0].hits.Load() != 0 {
		t.Errorf("upstream hits = %d, want 0", h.upstreams[0].hits.Load())
	}
}

// The hosted SDK prefers retry-after-ms over Retry-After. The gateway speaks
// seconds, so a millisecond hint reaches the caller as Retry-After rounded up.
func TestSystemOne_RateLimit_RetryAfterMsReachesCallerAsSeconds(t *testing.T) {
	h := setupSystemOneRouter(t,
		hostedChannel(0, "sk-upstream-ssssssss", soStatusHandler(429, `{"detail":"slow down"}`, map[string]string{"retry-after-ms": "1500"})))

	w := h.post(t, soValidBody)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "2" {
		t.Errorf("Retry-After = %q, want 2 (1500 ms rounded up)", got)
	}
}
