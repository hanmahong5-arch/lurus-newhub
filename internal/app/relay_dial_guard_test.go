package app

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

// withFetchSetting is defined in ssrf_guard_test.go (same package) and reused here.

// withLookup swaps the dial guard's resolver for a deterministic stub.
func withLookup(t *testing.T, fn func(ctx context.Context, host string) ([]net.IPAddr, error)) {
	t.Helper()
	old := relayEgressLookupIPAddr
	t.Cleanup(func() { relayEgressLookupIPAddr = old })
	relayEgressLookupIPAddr = fn
}

func TestCheckRelayEgress_BlocksInternalIPLiterals(t *testing.T) {
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})
	blocked := []string{
		"127.0.0.1:80",        // loopback
		"10.0.0.5:443",        // RFC1918
		"192.168.1.10:8080",   // RFC1918
		"172.16.0.9:80",       // RFC1918
		"169.254.169.254:80",  // link-local (cloud metadata)
		"100.64.1.1:8080",     // CGNAT
		"100.122.83.20:10250", // this cluster's Tailscale node → kubelet
		"[::1]:80",            // IPv6 loopback
		"0.0.0.0:3000",        // unspecified → routes to localhost
	}
	for _, addr := range blocked {
		if err := checkRelayEgress(context.Background(), addr, nil); err == nil {
			t.Errorf("expected %s to be blocked, got nil", addr)
		}
	}
}

func TestCheckRelayEgress_AllowsPublicIPLiterals(t *testing.T) {
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})
	for _, addr := range []string{"8.8.8.8:443", "1.1.1.1:80", "104.18.0.1:443"} {
		if err := checkRelayEgress(context.Background(), addr, nil); err != nil {
			t.Errorf("expected %s to be allowed, got %v", addr, err)
		}
	}
}

func TestCheckRelayEgress_ExemptsProxyHost(t *testing.T) {
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})
	proxyHosts := map[string]struct{}{"10.42.1.1": {}}
	// The egress proxy is private but must be reachable — it is the control point.
	if err := checkRelayEgress(context.Background(), "10.42.1.1:10808", proxyHosts); err != nil {
		t.Fatalf("proxy host must be exempt, got %v", err)
	}
	// A different private host is still blocked even when a proxy is configured.
	if err := checkRelayEgress(context.Background(), "10.0.0.5:80", proxyHosts); err == nil {
		t.Fatal("non-proxy internal host must still be blocked")
	}
}

func TestCheckRelayEgress_ExemptsOperatorWorker(t *testing.T) {
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})
	prev := system_setting.WorkerUrl
	t.Cleanup(func() { system_setting.WorkerUrl = prev })

	// An operator worker at an internal address is a trusted egress relay and
	// must stay reachable (user webhook/bark/gotify delivery routes through it).
	system_setting.WorkerUrl = "http://127.0.0.1:5608"
	if err := checkRelayEgress(context.Background(), "127.0.0.1:5608", nil); err != nil {
		t.Fatalf("operator worker host must be exempt, got %v", err)
	}
	// A different internal host is still blocked while a worker is configured.
	if err := checkRelayEgress(context.Background(), "10.0.0.9:80", nil); err == nil {
		t.Fatal("non-worker internal host must still be blocked")
	}
}

func TestCheckRelayEgress_NoopWhenProtectionDisabled(t *testing.T) {
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = false
	})
	if err := checkRelayEgress(context.Background(), "10.0.0.5:80", nil); err != nil {
		t.Fatalf("guard must be a no-op when SSRF protection is off, got %v", err)
	}
}

func TestCheckRelayEgress_NoopWhenAllowPrivate(t *testing.T) {
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = true
	})
	// Operators with a self-hosted private-IP LLM opt in via AllowPrivateIp; the
	// dial guard must honor it exactly as write-time validation does.
	if err := checkRelayEgress(context.Background(), "192.168.1.50:11434", nil); err != nil {
		t.Fatalf("AllowPrivateIp must permit private egress, got %v", err)
	}
}

func TestCheckRelayEgress_ResolvesHostname(t *testing.T) {
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})

	// Rebinding: hostname resolves to an internal address → blocked.
	withLookup(t, func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.5")}}, nil
	})
	if err := checkRelayEgress(context.Background(), "rebind.evil.example:443", nil); err == nil {
		t.Error("hostname resolving to internal IP must be blocked")
	}

	// Legit external hostname → allowed.
	withLookup(t, func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	})
	if err := checkRelayEgress(context.Background(), "api.example.com:443", nil); err != nil {
		t.Errorf("external hostname must be allowed, got %v", err)
	}

	// Mixed answer with one internal IP → blocked (fail closed on any internal).
	withLookup(t, func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("127.0.0.1")}}, nil
	})
	if err := checkRelayEgress(context.Background(), "mixed.example:443", nil); err == nil {
		t.Error("any internal IP in the answer set must block the dial")
	}
}

func TestCheckRelayEgress_ResolverErrorFailsOpen(t *testing.T) {
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})
	withLookup(t, func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return nil, errors.New("no such host")
	})
	if err := checkRelayEgress(context.Background(), "transient.example:443", nil); err != nil {
		t.Fatalf("resolver error must fail open (dial fails on its own), got %v", err)
	}
}

func TestNewRelayGuardedDialContext_BlocksBeforeDial(t *testing.T) {
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})
	dial := newRelayGuardedDialContext(&net.Dialer{})
	_, err := dial(context.Background(), "tcp", "10.0.0.5:80")
	if err == nil || !strings.Contains(err.Error(), "SSRF guard") {
		t.Fatalf("expected SSRF-guard block error, got %v", err)
	}
}

func TestNewRelayGuardedDialContext_DialsWhenAllowed(t *testing.T) {
	// Prove the guard forwards to the base dialer on the allow path by dialing a
	// real local listener. 127.0.0.1 is private, so opt in via AllowPrivateIp
	// (the same escape hatch a self-hosted-LLM operator would use).
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = true
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	dial := newRelayGuardedDialContext(&net.Dialer{})
	conn, err := dial(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("guarded dial to allowed target failed: %v", err)
	}
	_ = conn.Close()
}

func TestEgressProxyHosts_ParsesURLAndBareHostPort(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://10.42.1.1:10808")
	t.Setenv("HTTPS_PROXY", "proxy.internal:3128")
	t.Setenv("ALL_PROXY", "")
	hosts := egressProxyHosts()
	for _, want := range []string{"10.42.1.1", "proxy.internal"} {
		if _, ok := hosts[want]; !ok {
			t.Errorf("expected proxy host %q in %v", want, hosts)
		}
	}
}
