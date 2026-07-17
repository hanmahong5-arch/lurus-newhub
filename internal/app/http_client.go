package app

import (
	"context"
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
)

// maxProxyClients caps the proxy-client cache. Keys are configured proxy URLs
// (from channel config, not attacker-controlled), so this is a safety ceiling
// against unbounded growth — not a hot eviction path. When the cache is full it
// is dropped wholesale (idle connections closed first) and rebuilt on demand;
// entries are cheap to reconstruct, so a coarse reset beats per-key LRU here.
const maxProxyClients = 256

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
// by streamingTimeout, not here). Factored so the three transport construction
// sites (default + http/https proxy + socks5 proxy) cannot drift.
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
