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
			name:         "none of the four headers present -> empty, not a defect",
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

// TestDoRequest_ForceHTTP1NegotiatesHTTP1 drives doRequest through the real
// DoApiRequest entry point against a live httptest upstream that offers
// HTTP/2, with ChannelSetting.ForceHTTP1 set — the response must come back
// over HTTP/1.1 despite the server's h2 support. This is the seam L5 exists
// for: an operator pinning one flaky-HTTP/2 channel to H1.
func TestDoRequest_ForceHTTP1NegotiatesHTTP1(t *testing.T) {
	app.InitHttpClient()
	provReqCovAllowPrivateIP(t)

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	client, err := app.GetHttpClientFor("", true)
	if err != nil {
		t.Fatalf("GetHttpClientFor: %v", err)
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	// Trust the test server's self-signed cert while leaving TLSNextProto as
	// GetHttpClientFor set it (non-nil empty), so no ALPN "h2" offer goes out
	// regardless of what the server supports.
	certPool := x509.NewCertPool()
	certPool.AddCert(server.Certificate())
	tr.TLSClientConfig = &tls.Config{RootCAs: certPool}

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
}
