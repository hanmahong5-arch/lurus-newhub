package app

// http_client_socks_dial_test.go — a per-channel SOCKS5 proxy that accepts
// the TCP connection but never answers the SOCKS greeting must not pin a
// relay goroutine forever. Both transport construction sites (the cached
// NewProxyHttpClient client and the forced-HTTP/1.1 client) are exercised,
// with and without a caller deadline, because RELAY_TIMEOUT=0 in production
// means the request ctx carries none and RelayDialTimeout is the only bound.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// silentSocksProxy listens on loopback and accepts connections without ever
// writing a byte, so the SOCKS handshake blocks on the client side until a
// deadline fires. Conns are closed on cleanup so the accept goroutine exits.
func silentSocksProxy(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var conns []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns = append(conns, c)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return ln.Addr().String()
}

func socksTransportFor(t *testing.T, proxyURL string, forceHTTP1 bool) *http.Transport {
	t.Helper()
	client, err := GetHttpClientFor(proxyURL, forceHTTP1)
	if err != nil {
		t.Fatalf("GetHttpClientFor(%q, %v): %v", proxyURL, forceHTTP1, err)
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok || tr == nil || tr.DialContext == nil {
		t.Fatalf("expected an *http.Transport with a DialContext, got %T", client.Transport)
	}
	return tr
}

// assertSocksDialBounded runs the dial under a 2s watchdog: a dial that
// outlives it is the unbounded hang this test exists to catch.
func assertSocksDialBounded(t *testing.T, tr *http.Transport, ctx context.Context) {
	t.Helper()
	type result struct {
		conn net.Conn
		err  error
	}
	res := make(chan result, 1)
	start := time.Now()
	go func() {
		conn, err := tr.DialContext(ctx, "tcp", "example.com:443")
		res <- result{conn, err}
	}()
	select {
	case r := <-res:
		if r.conn != nil {
			_ = r.conn.Close()
			t.Fatal("dial through a silent socks5 proxy must not yield a connection")
		}
		if r.err == nil {
			t.Fatal("expected a dial error, got nil")
		}
		if elapsed := time.Since(start); elapsed >= time.Second {
			t.Fatalf("dial took %v; expected to fail within RelayDialTimeout (300ms)", elapsed)
		}
		if !errors.Is(r.err, context.DeadlineExceeded) && !errors.Is(r.err, os.ErrDeadlineExceeded) {
			var ne net.Error
			if !errors.As(r.err, &ne) || !ne.Timeout() {
				t.Fatalf("expected a timeout error, got %v", r.err)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dial through a silent socks5 proxy hung past the 2s watchdog: ctx/RelayDialTimeout are not applied to the SOCKS handshake")
	}
}

func TestSocksDial_RespectsRelayDialTimeoutAndCtx(t *testing.T) {
	ResetProxyClientCache()
	ResetForceH1ClientCache()
	t.Cleanup(ResetProxyClientCache)
	t.Cleanup(ResetForceH1ClientCache)
	prevDial := common.RelayDialTimeout
	common.RelayDialTimeout = 300 * time.Millisecond
	t.Cleanup(func() { common.RelayDialTimeout = prevDial })

	proxyURL := "socks5://" + silentSocksProxy(t)

	for _, forceHTTP1 := range []bool{false, true} {
		tr := socksTransportFor(t, proxyURL, forceHTTP1)
		name := "cached"
		if forceHTTP1 {
			name = "forceHTTP1"
		}
		t.Run(name+"/no_ctx_deadline", func(t *testing.T) {
			assertSocksDialBounded(t, tr, context.Background())
		})
		t.Run(name+"/ctx_deadline", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			assertSocksDialBounded(t, tr, ctx)
		})
	}
}
