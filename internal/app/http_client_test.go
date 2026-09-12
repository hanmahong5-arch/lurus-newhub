package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestHttpClient_Init(t *testing.T) {
	common.RelayTimeout = 0
	common.RelayMaxIdleConns = 100
	common.RelayMaxIdleConnsPerHost = 50

	InitHttpClient()

	client := GetHttpClient()
	if client == nil {
		t.Fatal("expected non-nil http client after InitHttpClient()")
	}
}

func TestHttpClient_ProxyFromEnv(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:8888")

	common.RelayTimeout = 0
	common.RelayMaxIdleConns = 100
	common.RelayMaxIdleConnsPerHost = 50

	// Should not panic even with an unreachable proxy
	InitHttpClient()

	client := GetHttpClient()
	if client == nil {
		t.Fatal("expected non-nil http client with proxy env set")
	}
}

func TestHttpClient_NoProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")

	common.RelayTimeout = 0
	common.RelayMaxIdleConns = 100
	common.RelayMaxIdleConnsPerHost = 50

	InitHttpClient()

	client := GetHttpClient()
	if client == nil {
		t.Fatal("expected non-nil http client without proxy env")
	}
}

func TestHttpClient_Timeout(t *testing.T) {
	common.RelayTimeout = 30
	common.RelayMaxIdleConns = 100
	common.RelayMaxIdleConnsPerHost = 50

	InitHttpClient()

	client := GetHttpClient()
	if client == nil {
		t.Fatal("expected non-nil http client")
	}
	expected := 30 * time.Second
	if client.Timeout != expected {
		t.Fatalf("expected timeout %v, got %v", expected, client.Timeout)
	}
}

// TestHttpClient_RelayTransportTimeouts is the B1/R3 footgun guard: the default
// relay client must bound time-to-first-header + connect, but MUST NOT set a
// total Client.Timeout (which would cut legitimate long SSE streams).
func TestHttpClient_RelayTransportTimeouts(t *testing.T) {
	common.RelayTimeout = 0 // the footgun default: no total timeout
	common.RelayResponseHeaderTimeout = 90 * time.Second
	common.RelayDialTimeout = 10 * time.Second
	common.RelayMaxIdleConns = 100
	common.RelayMaxIdleConnsPerHost = 50

	InitHttpClient()
	client := GetHttpClient()
	if client == nil {
		t.Fatal("expected non-nil http client")
	}
	// Footgun guard: a TOTAL timeout would cut long legitimate streams.
	if client.Timeout != 0 {
		t.Fatalf("Client.Timeout must stay 0 (no total timeout), got %v", client.Timeout)
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	if tr.ResponseHeaderTimeout != 90*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %v, want 90s", tr.ResponseHeaderTimeout)
	}
	if tr.DialContext == nil {
		t.Fatal("DialContext (connect timeout) must be set")
	}
}

// TestRelayTransportTimeouts_Behavior proves the runtime semantics of
// applyRelayTransportTimeouts: a never-responding upstream IS cut at
// ResponseHeaderTimeout, while a stream whose body is slower than that timeout is
// NOT cut (headers already arrived). Both assertions stay well under 1s (-short).
func TestRelayTransportTimeouts_Behavior(t *testing.T) {
	common.RelayDialTimeout = 10 * time.Second

	newClient := func() *http.Client {
		tr := &http.Transport{}
		applyRelayTransportTimeouts(tr)
		return &http.Client{Transport: tr} // no total Timeout — mirrors production
	}

	t.Run("no_headers_is_timed_out", func(t *testing.T) {
		common.RelayResponseHeaderTimeout = 250 * time.Millisecond
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(700 * time.Millisecond) // never sends headers within the timeout
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		start := time.Now()
		resp, err := newClient().Get(srv.URL)
		elapsed := time.Since(start)
		if err == nil {
			_ = resp.Body.Close()
			t.Fatal("expected ResponseHeaderTimeout error for a no-headers upstream")
		}
		if elapsed >= time.Second {
			t.Fatalf("must time out at ~ResponseHeaderTimeout, took %v", elapsed)
		}
	})

	t.Run("slow_body_stream_not_cut", func(t *testing.T) {
		common.RelayResponseHeaderTimeout = 400 * time.Millisecond
		const chunks = 3
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Headers arrive immediately → ResponseHeaderTimeout satisfied. The body
			// then streams for 3×180ms = 540ms (> 400ms) and must NOT be cut.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			for i := 0; i < chunks; i++ {
				time.Sleep(180 * time.Millisecond)
				_, _ = io.WriteString(w, "data: keepalive\n\n")
				w.(http.Flusher).Flush()
			}
		}))
		defer srv.Close()

		start := time.Now()
		resp, err := newClient().Get(srv.URL)
		if err != nil {
			t.Fatalf("slow-body stream must NOT be cut by ResponseHeaderTimeout, got %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, readErr := io.ReadAll(resp.Body)
		elapsed := time.Since(start)
		if readErr != nil {
			t.Fatalf("reading slow body: %v", readErr)
		}
		if strings.Count(string(body), "keepalive") != chunks {
			t.Fatalf("expected %d streamed chunks intact, got %q", chunks, body)
		}
		if elapsed >= time.Second {
			t.Fatalf("behavioral test must stay -short-safe (<1s), took %v", elapsed)
		}
	})
}

func TestHttpClient_NewProxyClient_HTTP(t *testing.T) {
	common.RelayTimeout = 10
	common.RelayMaxIdleConns = 100
	common.RelayMaxIdleConnsPerHost = 50

	ResetProxyClientCache()

	client, err := NewProxyHttpClient("http://proxy:8080")
	if err != nil {
		t.Fatalf("unexpected error for http proxy: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client for http proxy")
	}
}

func TestHttpClient_NewProxyClient_SOCKS5(t *testing.T) {
	common.RelayTimeout = 10
	common.RelayMaxIdleConns = 100
	common.RelayMaxIdleConnsPerHost = 50

	ResetProxyClientCache()

	client, err := NewProxyHttpClient("socks5://proxy:1080")
	if err != nil {
		t.Fatalf("unexpected error for socks5 proxy: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client for socks5 proxy")
	}
}

func TestHttpClient_NewProxyClient_Invalid(t *testing.T) {
	common.RelayTimeout = 10
	common.RelayMaxIdleConns = 100
	common.RelayMaxIdleConnsPerHost = 50

	ResetProxyClientCache()

	_, err := NewProxyHttpClient("ftp://proxy:21")
	if err == nil {
		t.Fatal("expected error for unsupported proxy scheme ftp")
	}
}

func TestHttpClient_NewProxyClient_Empty(t *testing.T) {
	ResetProxyClientCache()

	client, err := NewProxyHttpClient("")
	if err != nil {
		t.Fatalf("unexpected error for empty proxy URL: %v", err)
	}
	if client != http.DefaultClient {
		t.Fatal("expected default http client for empty proxy URL")
	}
}

func TestHttpClient_GetHttpClientWithProxy_Empty(t *testing.T) {
	common.RelayTimeout = 0
	common.RelayMaxIdleConns = 100
	common.RelayMaxIdleConnsPerHost = 50

	InitHttpClient()

	client, err := GetHttpClientWithProxy("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client != GetHttpClient() {
		t.Fatal("expected GetHttpClient() result when proxy URL is empty")
	}
}

func TestHttpClient_GetHttpClientWithProxy_WithURL(t *testing.T) {
	common.RelayTimeout = 10
	common.RelayMaxIdleConns = 100
	common.RelayMaxIdleConnsPerHost = 50

	ResetProxyClientCache()

	client, err := GetHttpClientWithProxy("http://proxy:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client for proxy URL")
	}
}

// TestProxyClientCacheBounded asserts the proxy-client cache never grows past
// maxProxyClients (§7 "bound everything"). Inserting more distinct proxy URLs
// than the bound must keep len(proxyClients) <= maxProxyClients at every step,
// and — since we insert strictly more than the bound — eviction must fire so
// the cache stays strictly smaller than the number of inserts.
func TestProxyClientCacheBounded(t *testing.T) {
	common.RelayTimeout = 0
	common.RelayMaxIdleConns = 100
	common.RelayMaxIdleConnsPerHost = 50

	ResetProxyClientCache()
	t.Cleanup(ResetProxyClientCache)

	// http scheme builds a client with no network call, so this is hermetic.
	const inserts = maxProxyClients + 50
	for i := 0; i < inserts; i++ {
		url := fmt.Sprintf("http://proxy-%d.invalid:8080", i)
		client, err := NewProxyHttpClient(url)
		if err != nil {
			t.Fatalf("NewProxyHttpClient(%q): %v", url, err)
		}
		if client == nil {
			t.Fatalf("nil client for %q", url)
		}
		proxyClientLock.Lock()
		size := len(proxyClients)
		proxyClientLock.Unlock()
		if size > maxProxyClients {
			t.Fatalf("after %d inserts the cache holds %d entries, exceeds bound %d", i+1, size, maxProxyClients)
		}
	}

	proxyClientLock.Lock()
	final := len(proxyClients)
	proxyClientLock.Unlock()
	if final == 0 || final > maxProxyClients {
		t.Fatalf("final cache size %d not in (0, %d]", final, maxProxyClients)
	}
	if final >= inserts {
		t.Fatalf("cache never evicted: holds %d of %d inserts (unbounded)", final, inserts)
	}
}

// TestGetHttpClientFor_DefaultIsSharedPointer proves GetHttpClientFor(proxy,
// false) is a purely additive seam: with forceHTTP1 off it must return the
// EXACT SAME *http.Client the pre-existing callers get (GetHttpClient() for
// no proxy, NewProxyHttpClient's cached entry for a proxy) — pointer
// identity, not just equal behaviour, is the bar because a byte-for-byte
// unchanged default is the L5 acceptance criterion.
func TestGetHttpClientFor_DefaultIsSharedPointer(t *testing.T) {
	common.RelayTimeout = 0
	common.RelayMaxIdleConns = 100
	common.RelayMaxIdleConnsPerHost = 50
	InitHttpClient()
	ResetProxyClientCache()

	c1, err := GetHttpClientFor("", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c1 != GetHttpClient() {
		t.Fatal("expected pointer-identical to GetHttpClient() when neither override is set")
	}

	const proxyURL = "http://proxy-getfor-default.invalid:8080"
	pc1, err := NewProxyHttpClient(proxyURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pc2, err := GetHttpClientFor(proxyURL, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pc1 != pc2 {
		t.Fatal("expected pointer-identical to NewProxyHttpClient's cached client when forceHTTP1 is false")
	}
}

// TestGetHttpClientFor_ForceHTTP1Transport proves the forceHTTP1=true branch
// builds a transport that cannot negotiate HTTP/2: ForceAttemptHTTP2 must be
// false AND TLSNextProto must be a non-nil empty map (net/http's Transport
// only skips its own automatic h2 wiring when TLSNextProto is already
// non-nil — leaving it nil would silently re-enable h2 despite
// ForceAttemptHTTP2:false, since that flag alone doesn't disable an ALPN
// upgrade the server offers).
func TestGetHttpClientFor_ForceHTTP1Transport(t *testing.T) {
	common.RelayTimeout = 5
	common.RelayMaxIdleConns = 10
	common.RelayMaxIdleConnsPerHost = 5

	client, err := GetHttpClientFor("", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	if tr.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 must be false for a forced-HTTP/1.1 transport")
	}
	if tr.TLSNextProto == nil {
		t.Error("TLSNextProto must be non-nil (empty map disables the automatic h2 ALPN upgrade) — nil silently falls back to the default h2-enabled table")
	}
	if len(tr.TLSNextProto) != 0 {
		t.Errorf("TLSNextProto must be empty, got %d entries", len(tr.TLSNextProto))
	}
}

// TestGetHttpClientFor_CacheKeyIncludesProtocol proves the forced-HTTP/1.1
// client cache is keyed separately from the regular proxyClients cache: the
// same proxyURL must yield a DIFFERENT client when forceHTTP1 flips (so one
// flaky-HTTP/2 channel's override cannot leak an H1-only transport onto a
// sibling channel sharing the same proxy), while two calls with the same
// (proxyURL, true) pair must return the identical cached client.
func TestGetHttpClientFor_CacheKeyIncludesProtocol(t *testing.T) {
	common.RelayTimeout = 0
	common.RelayMaxIdleConns = 10
	common.RelayMaxIdleConnsPerHost = 5
	ResetProxyClientCache()

	const proxyURL = "http://proxy-getfor-protocol.invalid:8080"

	normal, err := GetHttpClientFor(proxyURL, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h1a, err := GetHttpClientFor(proxyURL, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h1b, err := GetHttpClientFor(proxyURL, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if normal == h1a {
		t.Fatal("forceHTTP1 client must not share a cache entry with the normal proxy client for the same proxyURL")
	}
	if h1a != h1b {
		t.Fatal("two calls with forceHTTP1=true for the same proxyURL must return the cached (pointer-identical) client")
	}
}
