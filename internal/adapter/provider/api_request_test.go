package provider

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func TestProcessHeaderOverride(t *testing.T) {
	tests := []struct {
		name            string
		headersOverride map[string]interface{}
		apiKey          string
		wantHeaders     map[string]string
		wantErr         bool
	}{
		{
			name:            "nil map",
			headersOverride: nil,
			apiKey:          "sk-123",
			wantHeaders:     map[string]string{},
			wantErr:         false,
		},
		{
			name:            "empty map",
			headersOverride: map[string]interface{}{},
			apiKey:          "sk-123",
			wantHeaders:     map[string]string{},
			wantErr:         false,
		},
		{
			name:            "single header",
			headersOverride: map[string]interface{}{"X-Custom": "val"},
			apiKey:          "sk-123",
			wantHeaders:     map[string]string{"X-Custom": "val"},
			wantErr:         false,
		},
		{
			name: "multiple headers",
			headersOverride: map[string]interface{}{
				"X-One":   "1",
				"X-Two":   "2",
				"X-Three": "3",
			},
			apiKey: "sk-123",
			wantHeaders: map[string]string{
				"X-One":   "1",
				"X-Two":   "2",
				"X-Three": "3",
			},
			wantErr: false,
		},
		{
			name:            "Accept-Encoding lowercase skipped",
			headersOverride: map[string]interface{}{"accept-encoding": "gzip"},
			apiKey:          "sk-123",
			wantHeaders:     map[string]string{},
			wantErr:         false,
		},
		{
			name:            "Accept-Encoding canonical skipped",
			headersOverride: map[string]interface{}{"Accept-Encoding": "br"},
			apiKey:          "sk-123",
			wantHeaders:     map[string]string{},
			wantErr:         false,
		},
		{
			name:            "ACCEPT-ENCODING uppercase skipped",
			headersOverride: map[string]interface{}{"ACCEPT-ENCODING": "deflate"},
			apiKey:          "sk-123",
			wantHeaders:     map[string]string{},
			wantErr:         false,
		},
		{
			name:            "api_key replacement",
			headersOverride: map[string]interface{}{"Auth": "Bearer {api_key}"},
			apiKey:          "sk-123",
			wantHeaders:     map[string]string{"Auth": "Bearer sk-123"},
			wantErr:         false,
		},
		{
			name:            "double api_key replacement",
			headersOverride: map[string]interface{}{"X-Keys": "{api_key}:{api_key}"},
			apiKey:          "mykey",
			wantHeaders:     map[string]string{"X-Keys": "mykey:mykey"},
			wantErr:         false,
		},
		{
			name:            "empty apiKey",
			headersOverride: map[string]interface{}{"Auth": "Bearer {api_key}"},
			apiKey:          "",
			wantHeaders:     map[string]string{"Auth": "Bearer "},
			wantErr:         false,
		},
		{
			name:            "no variable",
			headersOverride: map[string]interface{}{"X-Static": "val"},
			apiKey:          "sk-456",
			wantHeaders:     map[string]string{"X-Static": "val"},
			wantErr:         false,
		},
		{
			name:            "non-string value int",
			headersOverride: map[string]interface{}{"X-Num": 42},
			apiKey:          "sk-123",
			wantErr:         true,
		},
		{
			name:            "non-string value bool",
			headersOverride: map[string]interface{}{"X-Bool": true},
			apiKey:          "sk-123",
			wantErr:         true,
		},
		{
			name:            "nil value",
			headersOverride: map[string]interface{}{"X-Nil": nil},
			apiKey:          "sk-123",
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &common.RelayInfo{
				ChannelMeta: &common.ChannelMeta{
					HeadersOverride: tt.headersOverride,
					ApiKey:          tt.apiKey,
				},
			}

			got, err := processHeaderOverride(info)
			if (err != nil) != tt.wantErr {
				t.Fatalf("processHeaderOverride() err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if len(got) != len(tt.wantHeaders) {
				t.Fatalf("processHeaderOverride() returned %d headers, want %d", len(got), len(tt.wantHeaders))
			}
			for k, wantV := range tt.wantHeaders {
				gotV, ok := got[k]
				if !ok {
					t.Errorf("missing header %q", k)
					continue
				}
				if gotV != wantV {
					t.Errorf("header %q = %q, want %q", k, gotV, wantV)
				}
			}
		})
	}
}

// TestDoRequest_CapturesUpstreamRequestId drives doRequest through the real
// DoApiRequest entry point against a live httptest upstream: no hand-built
// *http.Response stands in for the seam under test.
func TestDoRequest_CapturesUpstreamRequestId(t *testing.T) {
	app.InitHttpClient()
	provReqCovAllowPrivateIP(t)

	tests := []struct {
		name         string
		respHeaders  map[string]string
		wantCaptured string
	}{
		{
			name: "x-request-id takes priority over the other three",
			respHeaders: map[string]string{
				"X-Request-Id":      "xrid-1",
				"Request-Id":        "rid-1",
				"Openai-Request-Id": "oid-1",
				"Cf-Ray":            "ray-1",
			},
			wantCaptured: "xrid-1",
		},
		{
			name: "falls back to request-id when x-request-id absent",
			respHeaders: map[string]string{
				"Request-Id":        "rid-2",
				"Openai-Request-Id": "oid-2",
				"Cf-Ray":            "ray-2",
			},
			wantCaptured: "rid-2",
		},
		{
			name: "falls back to openai-request-id when the first two absent",
			respHeaders: map[string]string{
				"Openai-Request-Id": "oid-3",
				"Cf-Ray":            "ray-3",
			},
			wantCaptured: "oid-3",
		},
		{
			name:         "falls back to cf-ray when the other three absent",
			respHeaders:  map[string]string{"Cf-Ray": "ray-4"},
			wantCaptured: "ray-4",
		},
		{
			name:         "none of the upstreamRequestIdHeaders present -> empty, not a defect",
			respHeaders:  map[string]string{},
			wantCaptured: "",
		},
		{
			name:         "exactly 128 printable-ASCII bytes kept",
			respHeaders:  map[string]string{"X-Request-Id": strings.Repeat("b", 128)},
			wantCaptured: strings.Repeat("b", 128),
		},
		{
			name:         "129 bytes dropped, not truncated to 128",
			respHeaders:  map[string]string{"X-Request-Id": strings.Repeat("a", 129)},
			wantCaptured: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tt.respHeaders {
					w.Header().Set(k, v)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			adaptor := &provReqCovAdaptor{url: server.URL + "/v1/x"}
			info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}}
			c, _ := provReqCovNewGinContext(t, http.MethodGet, "/x", nil)

			resp, err := DoApiRequest(adaptor, c, info, nil)
			if err != nil {
				t.Fatalf("DoApiRequest() error = %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if info.UpstreamRequestId != tt.wantCaptured {
				t.Errorf("info.UpstreamRequestId = %q, want %q", info.UpstreamRequestId, tt.wantCaptured)
			}
			if got := c.GetString("upstream_request_id"); got != tt.wantCaptured {
				t.Errorf(`c.GetString("upstream_request_id") = %q, want %q`, got, tt.wantCaptured)
			}
		})
	}
}

// TestDoRequest_ClearsStaleUpstreamRequestIdBeforeEachAttempt guards against
// cross-attempt contamination: a retry can land on a different channel than
// the previous attempt, and the previous attempt's captured id must not
// survive onto this attempt's gin.Context / RelayInfo — otherwise a failed
// attempt (or a header-less success) on channel B would still carry channel
// A's vendor id into whatever error/log row this attempt produces.
func TestDoRequest_ClearsStaleUpstreamRequestIdBeforeEachAttempt(t *testing.T) {
	app.InitHttpClient()
	provReqCovAllowPrivateIP(t)

	t.Run("client.Do failure clears the previous attempt's id", func(t *testing.T) {
		adaptor := &provReqCovAdaptor{url: provReqCovClosedPortURL(t) + "/v1/x"}
		info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}, UpstreamRequestId: "stale-channel-a"}
		c, _ := provReqCovNewGinContext(t, http.MethodGet, "/x", nil)
		c.Set("upstream_request_id", "stale-channel-a")

		resp, err := DoApiRequest(adaptor, c, info, nil)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			t.Fatalf("DoApiRequest() error = nil, want a do-request-failed error against a closed port")
		}
		if info.UpstreamRequestId != "" {
			t.Errorf("info.UpstreamRequestId = %q after a failed attempt, want \"\" (must not leak the previous channel's id onto this attempt's error row)", info.UpstreamRequestId)
		}
		if got := c.GetString("upstream_request_id"); got != "" {
			t.Errorf(`c.GetString("upstream_request_id") = %q after a failed attempt, want ""`, got)
		}
	})

	t.Run("header-less 200 clears the previous attempt's id", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		adaptor := &provReqCovAdaptor{url: server.URL + "/v1/x"}
		info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{}, UpstreamRequestId: "stale-channel-a"}
		c, _ := provReqCovNewGinContext(t, http.MethodGet, "/x", nil)
		c.Set("upstream_request_id", "stale-channel-a")

		resp, err := DoApiRequest(adaptor, c, info, nil)
		if err != nil {
			t.Fatalf("DoApiRequest() error = %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if info.UpstreamRequestId != "" {
			t.Errorf("info.UpstreamRequestId = %q, want \"\" (upstream sent no id header)", info.UpstreamRequestId)
		}
		if got := c.GetString("upstream_request_id"); got != "" {
			t.Errorf(`c.GetString("upstream_request_id") = %q, want ""`, got)
		}
	})
}

// TestBoundUpstreamRequestId_NonPrintableASCIIDropped exercises the character-
// class half of the bound directly: a control byte embedded in a header
// value is not guaranteed to survive real HTTP framing (\r\n would corrupt
// it, and some transports normalize other control bytes), so this is the
// only reliable way to prove boundUpstreamRequestId — the actual function
// doRequest calls — rejects it rather than passing it through.
func TestBoundUpstreamRequestId_NonPrintableASCIIDropped(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"control byte rejected", "abc\x01def", ""},
		{"del byte rejected", "abc\x7Fdef", ""},
		{"empty rejected", "", ""},
		{"plain ascii kept", "req-abc-123", "req-abc-123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := boundUpstreamRequestId(tt.in); got != tt.want {
				t.Errorf("boundUpstreamRequestId(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestUpstreamRequestIdHeaders_AllSkippedFromClientResponse is the round-2
// cross-package sync lock (findings 1/12/17): app.UpstreamHeadersNotForwarded
// (internal/app/http.go, consumed by IOCopyBytesGracefully) is a second,
// independently-maintained copy of this package's own upstreamRequestIdHeaders
// list, in canonical http.Header form. provider already imports app, so the
// reverse import to assert this from app's own test package would cycle —
// this is a place that can hold both lists without an import cycle, and it
// fails the moment upstreamRequestIdHeaders gains a name
// UpstreamHeadersNotForwarded does not skip, instead of the comment-only
// "the other three/remaining names" claim the findings flagged as an
// unverified hard-coded count.
//
// One-directional, matching what http.go's own comment on
// UpstreamHeadersNotForwarded already says: this proves
// upstreamRequestIdHeaders is a subset of UpstreamHeadersNotForwarded, not
// the reverse. UpstreamHeadersNotForwarded carries one name outside that
// subset on purpose — X-Oneapi-Request-Id, the gateway's own legacy alias
// set by middleware.RequestId, not a vendor-sent id
// upstreamRequestIdHeaders is meant to capture — so a reverse assertion
// would have to carve that name out rather than treat it as a real gap; a
// round-3 repair item chose the cheaper reword instead of adding it.
func TestUpstreamRequestIdHeaders_AllSkippedFromClientResponse(t *testing.T) {
	if len(upstreamRequestIdHeaders) == 0 {
		t.Fatal("upstreamRequestIdHeaders is empty — nothing to check")
	}
	for _, h := range upstreamRequestIdHeaders {
		canon := http.CanonicalHeaderKey(h)
		if !app.UpstreamHeadersNotForwarded[canon] {
			t.Errorf("upstreamRequestIdHeaders has %q (canonical %q), which app.UpstreamHeadersNotForwarded does not skip — a vendor sending it would reach the client raw AND be captured into other.upstream_request_id under a second, undocumented channel", h, canon)
		}
	}
}

// TestDoRequest_ForceHTTP1NegotiatesHTTP1 drives doRequest through the real
// DoApiRequest entry point against a live httptest upstream that offers
// HTTP/2, with ChannelSetting.ForceHTTP1 set — the response must come back
// over HTTP/1.1 despite the server's h2 support. This is the seam L5 exists
// for: an operator pinning one flaky-HTTP/2 channel to H1.
//
// KNOWN INSENSITIVITY (L5 repair, findings routing-resilience-limits-13#3/
// #11/#27/#39, disclosed rather than hidden): the plan's prescribed mutation
// for this test ("drop TLSNextProto") does NOT turn the ProtoMajor==1
// assertion below red on this machine's net/http — the "" proxyURL path's
// guarded DialContext plus this test's own TLSClientConfig assignment
// already suppress h2 negotiation once ForceAttemptHTTP2 is false,
// regardless of TLSNextProto. The two field assertions on tr immediately
// below ARE sensitive to that mutation (and to a ForceAttemptHTTP2 flip);
// TestGetHttpClientFor_ForceHTTP1Transport (http_client_test.go) is the
// designated §7 oracle for it — this integration test proves the wiring
// (doRequest actually calls GetHttpClientFor with forceHTTP1=true and gets a
// working, negotiating client back), not the transport's internal ALPN
// suppression mechanism.
func TestDoRequest_ForceHTTP1NegotiatesHTTP1(t *testing.T) {
	app.InitHttpClient()
	provReqCovAllowPrivateIP(t)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	certPool := x509.NewCertPool()
	certPool.AddCert(server.Certificate())

	client, err := app.GetHttpClientFor("", true)
	if err != nil {
		t.Fatalf("GetHttpClientFor: %v", err)
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	if tr.ForceAttemptHTTP2 {
		t.Errorf("forced-H1 transport ForceAttemptHTTP2 = true, want false")
	}
	if tr.TLSNextProto == nil || len(tr.TLSNextProto) != 0 {
		t.Errorf("forced-H1 transport TLSNextProto = %v, want a non-nil EMPTY map", tr.TLSNextProto)
	}
	// Trust the test server's self-signed cert while leaving TLSNextProto as
	// GetHttpClientFor set it (non-nil empty), so no ALPN "h2" offer goes out
	// regardless of what the server supports. This mutates the process-
	// global cached client in place — there is no seam to inject a stand-in
	// client into doRequest — so t.Cleanup resets the cache afterward:
	// otherwise every later GetHttpClientFor("", true) caller in this
	// process would inherit a test-only RootCAs pool (finding #10).
	tr.TLSClientConfig = &tls.Config{RootCAs: certPool}
	t.Cleanup(app.ResetForceH1ClientCache)

	adaptor := &provReqCovAdaptor{url: server.URL + "/v1/x"}
	info := &common.RelayInfo{ChannelMeta: &common.ChannelMeta{
		ChannelSetting: dto.ChannelSettings{ForceHTTP1: true},
	}}
	c, _ := provReqCovNewGinContext(t, http.MethodGet, "/x", nil)

	resp, err := DoApiRequest(adaptor, c, info, nil)
	if err != nil {
		t.Fatalf("DoApiRequest() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.ProtoMajor != 1 {
		t.Errorf("resp.Proto = %q, ProtoMajor = %d, want HTTP/1.1 (server offers h2, client must refuse it)", resp.Proto, resp.ProtoMajor)
	}

	// Sibling (finding #3a): a channel with ForceHTTP1 false must still
	// negotiate real HTTP/2 against the SAME h2-capable server, proving the
	// forced client above — not some property of the server or of this
	// test's TLS setup — is what changed the outcome. Exercised via a CLONE
	// of the shared default transport (never the shared *http.Transport
	// itself, and not through DoApiRequest/doRequest), so this assertion
	// never mutates the process-global default client every other test and
	// every non-forced relay call shares.
	siblingClient, err := app.GetHttpClientFor("", false)
	if err != nil {
		t.Fatalf("GetHttpClientFor(forceHTTP1=false): %v", err)
	}
	siblingTr, ok := siblingClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", siblingClient.Transport)
	}
	siblingClone := siblingTr.Clone()
	siblingClone.TLSClientConfig = &tls.Config{RootCAs: certPool}
	siblingResp, err := (&http.Client{Transport: siblingClone}).Get(server.URL + "/v1/x")
	if err != nil {
		t.Fatalf("sibling (default transport) request failed: %v", err)
	}
	defer func() { _ = siblingResp.Body.Close() }()
	if siblingResp.ProtoMajor != 2 {
		t.Errorf("sibling resp.Proto = %q, ProtoMajor = %d, want HTTP/2.0 (default transport must still negotiate h2)", siblingResp.Proto, siblingResp.ProtoMajor)
	}
}
