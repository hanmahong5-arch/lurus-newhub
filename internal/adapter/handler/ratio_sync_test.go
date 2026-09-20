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
	"net/url"
	"os"
	"path/filepath"
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

// ratioSyncModuleRoot walks up from this package to the directory holding
// go.mod, so the marker gate below can read the guards' own sources.
func ratioSyncModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find the module root above this package")
	return ""
}

// TestFetchUpstreamRatios_DialTimeRefusalIsAlsoFixed closes the other half
// of the refusal (cycle-13 L8 repair, D-L8-2). The pre-dial check is not the
// only thing that can refuse this fetch: the shared client's transport
// re-checks the RESOLVED address at connect time, and that error names the
// internal address it resolved to. Echoing it back to the caller hands over
// exactly what the fixed message exists to withhold.
//
// The configuration here is a real one, not a contrivance: in IP-whitelist
// mode (ip_filter_mode) app.ValidateOutboundURL deliberately does NOT
// resolve domain names (see its doc comment — forcing it there would reject
// CDN-hosted upstreams whose IPs rotate), while the dial guard applies the
// private-IP rule to the resolved address regardless of list mode. A host name that
// resolves to loopback therefore passes the pre-dial check and is refused at
// the dial. DNS rebinding reaches the same branch with the default settings.
func TestFetchUpstreamRatios_DialTimeRefusalIsAlsoFixed(t *testing.T) {
	fs := system_setting.GetFetchSetting()
	prev := *fs
	fs.EnableSSRFProtection = true
	fs.AllowPrivateIp = false
	fs.IpFilterMode = true
	fs.ApplyIPFilterForDomain = false
	fs.DomainFilterMode = false
	fs.DomainList = nil
	t.Cleanup(func() { *fs = prev })

	listener, conns := countingLoopbackServer(t)
	parsed, err := url.Parse(listener)
	if err != nil {
		t.Fatalf("parse listener url %q: %v", listener, err)
	}
	// Same listener, reached by NAME instead of by literal address.
	target := "http://localhost:" + parsed.Port()

	// The fixture is only meaningful if the pre-dial check admits this
	// target — otherwise this would be a second copy of the base_url case.
	if err := app.ValidateOutboundURL(target); err != nil {
		t.Fatalf("fixture is wrong: the pre-dial check already refuses %s (%v); this case must reach the DIAL-time guard", target, err)
	}

	resp := postRatioSync(t, `{"upstreams":[{"name":"probe","base_url":"`+target+`"}],"timeout":2}`)

	if len(resp.Data.TestResults) != 1 {
		t.Fatalf("test_results = %d entries, want 1: %+v", len(resp.Data.TestResults), resp.Data.TestResults)
	}
	got := resp.Data.TestResults[0]
	if got.Error != ratioSyncEgressRefusedMessage {
		t.Errorf("error = %q, want the fixed refusal %q — a dial-time refusal must not echo the address it resolved",
			got.Error, ratioSyncEgressRefusedMessage)
	}
	if n := conns.Load(); n != 0 {
		t.Errorf("the refused target accepted %d connection(s)", n)
	}
}

// TestRatioSyncEgressMarkers_StillMatchTheGuards is the drift gate for the
// classification above. internal/app builds both refusals with fmt.Errorf
// and no wrapped sentinel, so there is nothing to errors.Is against and the
// match is on a substring; if either guard is reworded, this fails here
// instead of silently resuming the leak.
func TestRatioSyncEgressMarkers_StillMatchTheGuards(t *testing.T) {
	root := ratioSyncModuleRoot(t)
	cases := []struct {
		file    string
		markers []string
	}{
		{filepath.Join("internal", "app", "relay_dial_guard.go"), []string{ssrfDialGuardMarker}},
		{filepath.Join("internal", "app", "http_client.go"), []string{ssrfRedirectGuardPrefix, ssrfRedirectGuardMarker}},
	}
	for _, tc := range cases {
		body, err := os.ReadFile(filepath.Join(root, tc.file))
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		for _, marker := range tc.markers {
			if !strings.Contains(string(body), marker) {
				t.Errorf("%s no longer contains %q — ratio_sync classifies its transport errors on that substring, so the refusal message would start leaking the resolved address again",
					filepath.ToSlash(tc.file), marker)
			}
		}
	}
}

// TestFetchUpstreamRatios_DomainBlacklistIsEnforcedBeforeTheDial is the
// discriminating case for the PRE-DIAL check (cycle-13 L8 repair r2,
// D-L8-6). The two refusal cases above are also refused by the shared
// client's dial-time guard, so on their own they no longer prove the
// pre-dial check is doing anything: deleting it left them green.
//
// This posture takes the dial guard out of the picture — allow_private_ip
// is the operator lever for in-cluster inference and relay_dial_guard.go
// returns nil immediately when it is on — and leaves the host on the
// operator's domain blacklist. Scheme, port, domain list and IP list rules
// live ONLY in app.ValidateOutboundURL (common/ssrf_protection.go applies
// them; the dial guard deliberately enforces the private-IP rule alone), so
// this row is refused by the pre-dial check or by nothing at all.
//
// The second half is the control: the SAME listener reached by IP literal
// (which the domain blacklist cannot match) is fetched for real. Without it
// a "refused" result here would also be satisfied by an implementation that
// refuses everything in this configuration.
func TestFetchUpstreamRatios_DomainBlacklistIsEnforcedBeforeTheDial(t *testing.T) {
	fs := system_setting.GetFetchSetting()
	prev := *fs
	fs.EnableSSRFProtection = true
	fs.AllowPrivateIp = true
	fs.DomainFilterMode = false // blacklist mode
	fs.DomainList = []string{"localhost"}
	fs.IpFilterMode = false
	fs.IpList = nil
	fs.ApplyIPFilterForDomain = false
	t.Cleanup(func() { *fs = prev })

	listener, conns := countingLoopbackServer(t)
	parsed, err := url.Parse(listener)
	if err != nil {
		t.Fatalf("parse listener url %q: %v", listener, err)
	}

	blacklisted := "http://localhost:" + parsed.Port()
	resp := postRatioSync(t, `{"upstreams":[{"name":"probe","base_url":"`+blacklisted+`"}],"timeout":2}`)
	if len(resp.Data.TestResults) != 1 {
		t.Fatalf("test_results = %d entries, want 1: %+v", len(resp.Data.TestResults), resp.Data.TestResults)
	}
	if got := resp.Data.TestResults[0]; got.Error != ratioSyncEgressRefusedMessage {
		t.Errorf("status = %q error = %q, want the fixed refusal %q — a host on the operator's domain blacklist was fetched, "+
			"which means nothing on this endpoint enforces the scheme/port/domain/IP rules any more",
			got.Status, got.Error, ratioSyncEgressRefusedMessage)
	}
	if n := conns.Load(); n != 0 {
		t.Errorf("the blacklisted host accepted %d connection(s) — the list rules are enforced before the dial or not at all", n)
	}

	// Control row: same listener, address the blacklist cannot match.
	allowed := "http://127.0.0.1:" + parsed.Port()
	ctrl := postRatioSync(t, `{"upstreams":[{"name":"control","base_url":"`+allowed+`"}],"timeout":5}`)
	if len(ctrl.Data.TestResults) != 1 {
		t.Fatalf("control test_results = %d entries, want 1: %+v", len(ctrl.Data.TestResults), ctrl.Data.TestResults)
	}
	if got := ctrl.Data.TestResults[0]; got.Status != "success" {
		t.Fatalf("control status = %q error = %q, want \"success\": in this posture loopback is admitted, so the row above "+
			"must be refused by the domain rule rather than by a blanket refusal", got.Status, got.Error)
	}
	if n := conns.Load(); n != 1 {
		t.Errorf("listener saw %d connection(s) in total, want exactly 1 (the control row) ", n)
	}
}
