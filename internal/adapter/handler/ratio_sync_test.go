package handler

// ratio_sync_test.go — cycle-13 L8. POST /api/ratio_sync/fetch takes a URL
// out of the request body and fetches it server-side. Every other
// operator-supplied outbound target on this gateway (channel base_url, the
// channel test/fetch endpoints, the two task-media content routes) is vetted
// by app.ValidateOutboundURL first; this one was not, so it was the one
// body-URL egress with no SSRF snapshot in front of it — an admin session
// (or anything that reaches an admin session) could read the response of any
// address the pod can open, including the in-cluster Redis, Postgres and the
// platform's internal APIs.
//
// The oracle is deliberately two-sided: the fixed refusal in the response
// body proves the request was classified, and the listener's own connection
// counter proves nothing was dialled. A test that only checked the body
// would stay green against a "fetch it, then decide" implementation.

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
)

// countingLoopbackServer starts an httptest server on loopback whose every
// accepted connection bumps a counter, so "was it dialled at all" is an
// observation rather than an inference from the response body.
func countingLoopbackServer(t *testing.T) (url string, conns *atomic.Int64) {
	t.Helper()
	conns = &atomic.Int64{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"model_ratio":{"pwned":1}}}`))
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL, conns
}

// buildRatioSyncRouter mounts the real handler. app.InitHttpClient() is the
// boot step cmd/server/main.go performs before any route is served; the
// handler now uses that shared client, so the fixture performs it too —
// otherwise the "allowed target IS fetched" case below would be measuring a
// nil client instead of the egress path.
func buildRatioSyncRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	app.InitHttpClient()
	r := gin.New()
	r.POST("/api/ratio_sync/fetch", FetchUpstreamRatios)
	return r
}

type ratioSyncResponse struct {
	Success bool `json:"success"`
	Data    struct {
		TestResults []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"test_results"`
	} `json:"data"`
}

func postRatioSync(t *testing.T, body string) ratioSyncResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/ratio_sync/fetch", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	buildRatioSyncRouter().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp ratioSyncResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}
	return resp
}

// TestFetchUpstreamRatios_RefusesLoopbackBaseURL: the base_url arm.
func TestFetchUpstreamRatios_RefusesLoopbackBaseURL(t *testing.T) {
	target, conns := countingLoopbackServer(t)

	resp := postRatioSync(t, `{"upstreams":[{"name":"probe","base_url":"`+target+`"}],"timeout":2}`)

	if len(resp.Data.TestResults) != 1 {
		t.Fatalf("test_results = %d entries, want 1: %+v", len(resp.Data.TestResults), resp.Data.TestResults)
	}
	got := resp.Data.TestResults[0]
	if got.Status != "error" {
		t.Errorf("status = %q, want \"error\" — a loopback target must be refused", got.Status)
	}
	if got.Error != ratioSyncEgressRefusedMessage {
		t.Errorf("error = %q, want the fixed refusal %q (a variable message leaks whether the address answered)",
			got.Error, ratioSyncEgressRefusedMessage)
	}
	if n := conns.Load(); n != 0 {
		t.Errorf("the refused target accepted %d connection(s) — the check must run BEFORE the dial", n)
	}
}

// TestFetchUpstreamRatios_RefusesLoopbackViaAbsoluteEndpoint: the endpoint
// arm. An absolute `endpoint` REPLACES base_url entirely
// (ratio_sync.go builds fullURL = endpoint when it starts with http), so a
// check that only looked at base_url would be walked straight around by
// sending a public base_url and a loopback endpoint.
func TestFetchUpstreamRatios_RefusesLoopbackViaAbsoluteEndpoint(t *testing.T) {
	target, conns := countingLoopbackServer(t)

	resp := postRatioSync(t,
		`{"upstreams":[{"name":"probe","base_url":"https://upstream.example","endpoint":"`+target+`/api/ratio_config"}],"timeout":2}`)

	if len(resp.Data.TestResults) != 1 {
		t.Fatalf("test_results = %d entries, want 1: %+v", len(resp.Data.TestResults), resp.Data.TestResults)
	}
	if got := resp.Data.TestResults[0]; got.Error != ratioSyncEgressRefusedMessage {
		t.Errorf("error = %q, want the fixed refusal %q", got.Error, ratioSyncEgressRefusedMessage)
	}
	if n := conns.Load(); n != 0 {
		t.Errorf("the refused target accepted %d connection(s) via the endpoint override", n)
	}
}

// TestFetchUpstreamRatios_RefusalMessageIsEnglishAndFixed: the refusal text
// reaches an API client, so it follows the same English-only rule as every
// other wire message, and it says nothing about the address it refused.
func TestFetchUpstreamRatios_RefusalMessageIsEnglishAndFixed(t *testing.T) {
	for _, r := range ratioSyncEgressRefusedMessage {
		if r > 127 {
			t.Fatalf("refusal message %q contains a non-ASCII rune %q — API error text is English-only",
				ratioSyncEgressRefusedMessage, r)
		}
	}
	for _, leak := range []string{"127.0.0.1", "localhost", "private", "169.254"} {
		if strings.Contains(strings.ToLower(ratioSyncEgressRefusedMessage), leak) {
			t.Errorf("refusal message %q names %q — it must not describe the target it refused",
				ratioSyncEgressRefusedMessage, leak)
		}
	}
}

// TestFetchUpstreamRatios_AllowedTargetIsFetched is the other half of the
// oracle. Without it, "zero connections" would also be satisfied by an
// implementation that refuses everything (or by a nil http client), and the
// refusal rows above would pass for the wrong reason. Flipping
// allow_private_ip — the operator lever for in-cluster inference — turns the
// SAME request into a real fetch through the shared client, so the two rows
// together pin "refused BY POLICY", not "broken".
func TestFetchUpstreamRatios_AllowedTargetIsFetched(t *testing.T) {
	fs := system_setting.GetFetchSetting()
	prev := *fs
	fs.AllowPrivateIp = true
	t.Cleanup(func() { *fs = prev })

	target, conns := countingLoopbackServer(t)

	resp := postRatioSync(t, `{"upstreams":[{"name":"probe","base_url":"`+target+`"}],"timeout":5}`)

	if len(resp.Data.TestResults) != 1 {
		t.Fatalf("test_results = %d entries, want 1: %+v", len(resp.Data.TestResults), resp.Data.TestResults)
	}
	if got := resp.Data.TestResults[0]; got.Status != "success" {
		t.Errorf("status = %q error = %q, want \"success\" once the policy allows the target", got.Status, got.Error)
	}
	if n := conns.Load(); n != 1 {
		t.Errorf("listener saw %d connection(s), want 1 — the allowed target must actually be fetched", n)
	}
}
