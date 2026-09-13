package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"golang.org/x/net/proxy"
)

var (
	httpClient      *http.Client
	proxyClientLock sync.Mutex
	proxyClients    = make(map[string]*http.Client)

	// forceH1ClientLock/forceH1Clients cache HTTP/1.1-only clients, keyed by
	// proxyURL ("" for no proxy). Deliberately a SEPARATE map from
	// proxyClients (not just a different key namespace within it) so every
	// existing NewProxyHttpClient caller — and its cache-bound behaviour — is
	// untouched by this feature (L5, routing-resilience-limits-13).
	forceH1ClientLock sync.Mutex
	forceH1Clients    = make(map[string]*http.Client)
)

// maxProxyClients caps the proxy-client cache. Keys are configured proxy URLs
// (from channel config, not attacker-controlled), so this is a safety ceiling
// against unbounded growth — not a hot eviction path. When the cache is full it
// is dropped wholesale (idle connections closed first) and rebuilt on demand;
// entries are cheap to reconstruct, so a coarse reset beats per-key LRU here.
const maxProxyClients = 256

// maxForceH1Clients bounds the forced-HTTP/1.1 client cache for the same
// reason maxProxyClients bounds proxyClients — the same coarse-reset
// treatment applies since keys are operator-configured proxy URLs, not
// attacker-controlled.
const maxForceH1Clients = 256

func checkRedirect(req *http.Request, via []*http.Request) error {
	fetchSetting := system_setting.GetFetchSetting()
	urlStr := req.URL.String()
	if err := common.ValidateURLWithFetchSetting(urlStr, fetchSetting.EnableSSRFProtection, fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, fetchSetting.ApplyIPFilterForDomain); err != nil {
		return fmt.Errorf("redirect to %s blocked: %v", urlStr, err)
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	return nil
}

// applyRelayTransportTimeouts bounds the two upstream hang vectors on a relay
// transport (B1/R3) so a wedged provider cannot pin a client connection forever
// — WITHOUT capping legitimate long streams (the SSE body is governed per-chunk
// by streamingTimeout, not here). Factored so the transport construction sites
// (default, http/https proxy, socks5 proxy, and the forced-HTTP/1.1 transport
// in newForceHTTP1Transport) cannot drift.
//
//   - ResponseHeaderTimeout caps time-to-first-response-header; it is satisfied
//     for the request lifetime once 200+headers arrive, so an in-flight stream is
//     never cut by it. This is the real "hung upstream" guard.
//   - A net.Dialer connect timeout is installed ONLY when the transport has no
//     DialContext yet: the socks5 site sets its own dialer and must not be
//     clobbered (doing so would break socks5 proxying). socks5 still gets the
//     ResponseHeaderTimeout guard.
//
// http.Client.Timeout (RelayTimeout) is deliberately left untouched — a TOTAL
// timeout would cut long legitimate streams (the RELAY_TIMEOUT=0 footgun).
func applyRelayTransportTimeouts(t *http.Transport) {
	t.ResponseHeaderTimeout = common.RelayResponseHeaderTimeout
	if t.DialContext == nil {
		t.DialContext = (&net.Dialer{Timeout: common.RelayDialTimeout}).DialContext
	}
}

// defaultNonStreamReadTimeout bounds a NON-streaming response body read when
// RELAY_TIMEOUT=0 (the default): with no http.Client.Timeout set, only
// ResponseHeaderTimeout + dial are bounded, so a slow-trickling non-stream body
// could otherwise hang the reading goroutine forever. Does not apply to SSE
// streams, which are governed per-chunk by the existing streamingTimeout.
const defaultNonStreamReadTimeout = 300 * time.Second

// nonStreamReadTimeout resolves RELAY_NONSTREAM_READ_TIMEOUT (seconds) once per
// call; cheap enough (a single os.Getenv) that per-request use is fine and it
// avoids adding new package-level init-order coupling.
func nonStreamReadTimeout() time.Duration {
	return time.Duration(common.GetEnvOrDefault("RELAY_NONSTREAM_READ_TIMEOUT", int(defaultNonStreamReadTimeout/time.Second))) * time.Second
}

// deadlineReadCloser wraps a response body so that if no byte is read within
// the configured timeout, the underlying body is closed to unblock the stuck
// Read() call (net/http surfaces this to the caller as a read error, not a hang).
// The timer is reset after every successful Read and stopped on Close, so a
// body that IS being consumed steadily never trips it.
type deadlineReadCloser struct {
	io.ReadCloser
	timeout time.Duration
	timer   *time.Timer
}

// WrapNonStreamReadDeadline bounds reads from a non-streaming upstream response
// body so a wedged/slow-trickling provider cannot hang the reading goroutine
// forever under RELAY_TIMEOUT=0. Callers must only use this for non-stream
// responses (info.IsStream == false); wrapping an SSE body would cut a
// legitimate long-lived stream mid-flight.
func WrapNonStreamReadDeadline(body io.ReadCloser) io.ReadCloser {
	if body == nil {
		return body
	}
	d := &deadlineReadCloser{ReadCloser: body, timeout: nonStreamReadTimeout()}
	d.timer = time.AfterFunc(d.timeout, func() { _ = body.Close() })
	return d
}

func (d *deadlineReadCloser) Read(p []byte) (int, error) {
	n, err := d.ReadCloser.Read(p)
	if err == nil {
		d.timer.Reset(d.timeout)
	}
	return n, err
}

func (d *deadlineReadCloser) Close() error {
	d.timer.Stop()
	return d.ReadCloser.Close()
}

func InitHttpClient() {
	transport := &http.Transport{
		MaxIdleConns:        common.RelayMaxIdleConns,
		MaxIdleConnsPerHost: common.RelayMaxIdleConnsPerHost,
		ForceAttemptHTTP2:   true,
		Proxy:               http.ProxyFromEnvironment, // Support HTTP_PROXY, HTTPS_PROXY, NO_PROXY env vars
	}
	// Transport-layer SSRF guard (hardening plan R1): validate the resolved
	// destination at dial time so a rebinding or NO_PROXY-direct internal target
	// cannot be reached even though the relay hot path does not re-validate per
	// request. Set before applyRelayTransportTimeouts so the latter keeps this
	// DialContext (it only installs its own when DialContext is nil); the dialer
	// wrapped here already carries RelayDialTimeout.
	transport.DialContext = newRelayGuardedDialContext(&net.Dialer{Timeout: common.RelayDialTimeout})
	applyRelayTransportTimeouts(transport)

	if common.RelayTimeout == 0 {
		httpClient = &http.Client{
			Transport:     transport,
			CheckRedirect: checkRedirect,
		}
	} else {
		httpClient = &http.Client{
			Transport:     transport,
			Timeout:       time.Duration(common.RelayTimeout) * time.Second,
			CheckRedirect: checkRedirect,
		}
	}
}

func GetHttpClient() *http.Client {
	return httpClient
}

// GetHttpClientWithProxy returns the default client or a proxy-enabled one when proxyURL is provided.
func GetHttpClientWithProxy(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		return GetHttpClient(), nil
	}
	return NewProxyHttpClient(proxyURL)
}

// ResetProxyClientCache 清空代理客户端缓存，确保下次使用时重新初始化
func ResetProxyClientCache() {
	proxyClientLock.Lock()
	defer proxyClientLock.Unlock()
	resetProxyClientsLocked()
}

// resetProxyClientsLocked closes idle connections on every cached client and
// replaces the cache with a fresh empty map. Callers must hold proxyClientLock.
func resetProxyClientsLocked() {
	for _, client := range proxyClients {
		if transport, ok := client.Transport.(*http.Transport); ok && transport != nil {
			transport.CloseIdleConnections()
		}
	}
	proxyClients = make(map[string]*http.Client)
}

// ResetForceH1ClientCache closes idle connections on every cached forced-
// HTTP/1.1 client and replaces the cache with a fresh empty map, mirroring
// ResetProxyClientCache. Tests that must temporarily mutate a cached client
// in place (e.g. to install a test-only TLSClientConfig, since there is no
// seam to inject a stand-in client into doRequest) call this in t.Cleanup so
// no later GetHttpClientFor(_, true) caller in the process inherits the
// test-only state.
func ResetForceH1ClientCache() {
	forceH1ClientLock.Lock()
	defer forceH1ClientLock.Unlock()
	for _, client := range forceH1Clients {
		if transport, ok := client.Transport.(*http.Transport); ok && transport != nil {
			transport.CloseIdleConnections()
		}
	}
	forceH1Clients = make(map[string]*http.Client)
}

// storeProxyClient caches client under proxyURL, enforcing maxProxyClients.
// When the cache is already at the bound it is dropped wholesale (idle
// connections closed) before the new entry is inserted, so len(proxyClients)
// never exceeds maxProxyClients.
func storeProxyClient(proxyURL string, client *http.Client) {
	proxyClientLock.Lock()
	defer proxyClientLock.Unlock()
	if len(proxyClients) >= maxProxyClients {
		resetProxyClientsLocked()
	}
	proxyClients[proxyURL] = client
}

// NewProxyHttpClient 创建支持代理的 HTTP 客户端
func NewProxyHttpClient(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		return http.DefaultClient, nil
	}

	proxyClientLock.Lock()
	if client, ok := proxyClients[proxyURL]; ok {
		proxyClientLock.Unlock()
		return client, nil
	}
	proxyClientLock.Unlock()

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}

	switch parsedURL.Scheme {
	case "http", "https":
		tr := &http.Transport{
			MaxIdleConns:        common.RelayMaxIdleConns,
			MaxIdleConnsPerHost: common.RelayMaxIdleConnsPerHost,
			ForceAttemptHTTP2:   true,
			Proxy:               http.ProxyURL(parsedURL),
		}
		applyRelayTransportTimeouts(tr)
		client := &http.Client{
			Transport:     tr,
			CheckRedirect: checkRedirect,
		}
		client.Timeout = time.Duration(common.RelayTimeout) * time.Second
		storeProxyClient(proxyURL, client)
		return client, nil

	case "socks5", "socks5h":
		// 获取认证信息
		var auth *proxy.Auth
		if parsedURL.User != nil {
			auth = &proxy.Auth{
				User:     parsedURL.User.Username(),
				Password: "",
			}
			if password, ok := parsedURL.User.Password(); ok {
				auth.Password = password
			}
		}

		// 创建 SOCKS5 代理拨号器
		// proxy.SOCKS5 使用 tcp 参数，所有 TCP 连接包括 DNS 查询都将通过代理进行。行为与 socks5h 相同
		dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, proxy.Direct)
		if err != nil {
			return nil, err
		}

		tr := &http.Transport{
			MaxIdleConns:        common.RelayMaxIdleConns,
			MaxIdleConnsPerHost: common.RelayMaxIdleConnsPerHost,
			ForceAttemptHTTP2:   true,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
		}
		// Preserves the socks5 DialContext above (helper only sets it when nil);
		// adds the ResponseHeaderTimeout hung-upstream guard.
		applyRelayTransportTimeouts(tr)
		client := &http.Client{
			Transport:     tr,
			CheckRedirect: checkRedirect,
		}
		client.Timeout = time.Duration(common.RelayTimeout) * time.Second
		storeProxyClient(proxyURL, client)
		return client, nil

	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s, must be http, https, socks5 or socks5h", parsedURL.Scheme)
	}
}

// GetHttpClientFor is the single seam api_request.go's doRequest uses to pick
// a relay transport (L5, routing-resilience-limits-13). With forceHTTP1
// false it is a pure pass-through to the pre-existing clients — pointer-
// identical to GetHttpClient()/NewProxyHttpClient's cached entry — so a
// channel with no override set behaves byte-for-byte as before. With
// forceHTTP1 true it returns an HTTP/1.1-only transport cached in its own
// map (forceH1Clients), so a channel with a flaky HTTP/2 upstream can be
// pinned to H1 without affecting any sibling channel — including one that
// shares the same proxyURL.
//
// Honest scope: covers every call that goes through provider.doRequest,
// which is provider.DoApiRequest AND provider.DoTaskApiRequest — so it
// includes AWS Bedrock in API-key mode (aws/adaptor.go DoApiRequest branch),
// Coze, Vertex (chat), and every provider/task/*/adaptor.go relay (they all
// call DoTaskApiRequest). Bypassed only by side calls that build their own
// client directly: AWS's AKSK-credential mode (aws/relay-aws.go), Coze's
// result-poll call (coze/relay-coze.go), Vertex's service-account token
// exchange (vertex/service_account.go), MJ-proxy's own image fetch
// (relay/mjproxy_handler.go) and hailuo's task-status fetch
// (task/hailuo/adaptor.go). Exact sites listed in
// doc/product-integration-guide.md §G, not silently assumed.
func GetHttpClientFor(proxyURL string, forceHTTP1 bool) (*http.Client, error) {
	if !forceHTTP1 {
		return GetHttpClientWithProxy(proxyURL)
	}
	return getOrBuildForceHTTP1Client(proxyURL)
}

// getOrBuildForceHTTP1Client returns the cached HTTP/1.1-only client for
// proxyURL ("" for no proxy), building and caching one on first use.
func getOrBuildForceHTTP1Client(proxyURL string) (*http.Client, error) {
	forceH1ClientLock.Lock()
	if client, ok := forceH1Clients[proxyURL]; ok {
		forceH1ClientLock.Unlock()
		return client, nil
	}
	forceH1ClientLock.Unlock()

	transport, err := newForceHTTP1Transport(proxyURL)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: checkRedirect,
	}
	if common.RelayTimeout != 0 {
		client.Timeout = time.Duration(common.RelayTimeout) * time.Second
	}

	forceH1ClientLock.Lock()
	defer forceH1ClientLock.Unlock()
	if existing, ok := forceH1Clients[proxyURL]; ok {
		// Lost a race with another goroutine building the same entry; keep
		// theirs so callers observe one stable client per key.
		return existing, nil
	}
	if len(forceH1Clients) >= maxForceH1Clients {
		for _, c := range forceH1Clients {
			if t, ok := c.Transport.(*http.Transport); ok && t != nil {
				t.CloseIdleConnections()
			}
		}
		forceH1Clients = make(map[string]*http.Client)
	}
	forceH1Clients[proxyURL] = client
	return client, nil
}

// newForceHTTP1Transport builds a transport that cannot negotiate HTTP/2.
// ForceAttemptHTTP2:false alone is not sufficient — net/http.Transport
// otherwise auto-wires HTTP/2 support (adding "h2" to the TLS ALPN offer)
// whenever TLSNextProto is nil; setting it to a non-nil EMPTY map is what
// actually suppresses that wiring, which is why the mutation
// "drop TLSNextProto" is the one that must turn this red.
//
// proxyURL == "" mirrors InitHttpClient's default transport (SSRF-guarded
// dial context, ProxyFromEnvironment); a configured proxyURL mirrors
// NewProxyHttpClient's http/https/socks5 branches, minus HTTP/2.
func newForceHTTP1Transport(proxyURL string) (*http.Transport, error) {
	tr := &http.Transport{
		MaxIdleConns:        common.RelayMaxIdleConns,
		MaxIdleConnsPerHost: common.RelayMaxIdleConnsPerHost,
		ForceAttemptHTTP2:   false,
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
	}

	if proxyURL == "" {
		tr.Proxy = http.ProxyFromEnvironment
		tr.DialContext = newRelayGuardedDialContext(&net.Dialer{Timeout: common.RelayDialTimeout})
		applyRelayTransportTimeouts(tr)
		return tr, nil
	}

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	switch parsedURL.Scheme {
	case "http", "https":
		tr.Proxy = http.ProxyURL(parsedURL)
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if parsedURL.User != nil {
			auth = &proxy.Auth{User: parsedURL.User.Username()}
			if password, ok := parsedURL.User.Password(); ok {
				auth.Password = password
			}
		}
		dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, proxy.Direct)
		if err != nil {
			return nil, err
		}
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		}
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s, must be http, https, socks5 or socks5h", parsedURL.Scheme)
	}
	applyRelayTransportTimeouts(tr)
	return tr, nil
}
