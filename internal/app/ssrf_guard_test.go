package app

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

// withFetchSetting mutates the process-global fetch policy for the duration of
// a test and restores it after. Not parallel-safe (the setting is a package
// global), so callers must not t.Parallel().
func withFetchSetting(t *testing.T, mutate func(fs *system_setting.FetchSetting)) {
	t.Helper()
	fs := system_setting.GetFetchSetting()
	prev := *fs
	mutate(fs)
	t.Cleanup(func() { *fs = prev })
}

func TestValidateOutboundURL_BlocksInternalTargets(t *testing.T) {
	// Default policy: SSRF protection on, private IPs denied. All host cases use
	// IP literals so the assertion is hermetic (no DNS dependency).
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})

	blocked := []string{
		"http://10.0.0.5",        // RFC1918
		"http://127.0.0.1",       // loopback
		"http://169.254.169.254", // cloud metadata / link-local
		"http://192.168.1.1",     // RFC1918
		"http://172.16.0.9",      // RFC1918
		"http://0.0.0.0:3000",    // unspecified — kernel routes to localhost
		"http://100.64.1.1",      // CGNAT / Tailscale range (this cluster's nodes)
		"http://100.122.83.20",   // R6 STAGE node via Tailscale
	}
	for _, u := range blocked {
		if err := ValidateOutboundURL(u); err == nil {
			t.Errorf("ValidateOutboundURL(%q) = nil, want blocked", u)
		}
	}
}

func TestValidateOutboundURL_AllowsPublicAndEmpty(t *testing.T) {
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})

	if err := ValidateOutboundURL(""); err != nil {
		t.Errorf("empty base_url should be a no-op, got %v", err)
	}
	// Public IP literal on an allowed port (443) — hermetic, no DNS.
	if err := ValidateOutboundURL("https://8.8.8.8"); err != nil {
		t.Errorf("public https target should pass, got %v", err)
	}
}

func TestValidateOutboundURL_RespectsOperatorOverrides(t *testing.T) {
	// allow_private_ip=true is the deliberate posture for self-hosted in-cluster
	// inference: a private target then passes.
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = true
	})
	if err := ValidateOutboundURL("http://10.0.0.5"); err != nil {
		t.Errorf("with allow_private_ip=true, private target should pass, got %v", err)
	}

	// Global kill-switch off ⇒ validation is a no-op.
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = false
	})
	if err := ValidateOutboundURL("http://169.254.169.254"); err != nil {
		t.Errorf("with SSRF protection disabled, validation must be a no-op, got %v", err)
	}
}

func TestValidateOutboundProxy_BlocksInternalHostRegardlessOfScheme(t *testing.T) {
	withFetchSetting(t, func(fs *system_setting.FetchSetting) {
		fs.EnableSSRFProtection = true
		fs.AllowPrivateIp = false
	})

	if err := ValidateOutboundProxy(""); err != nil {
		t.Errorf("empty proxy should be a no-op, got %v", err)
	}
	// socks5 scheme is unknown to the raw URL validator; the guard must still
	// reject an internal proxy host by normalizing to an http:// envelope.
	if err := ValidateOutboundProxy("socks5://10.0.0.5:8080"); err == nil {
		t.Error("socks5 proxy to a private host should be blocked")
	}
	// Public proxy host on an allowed port passes.
	if err := ValidateOutboundProxy("socks5://8.8.8.8:8080"); err != nil {
		t.Errorf("socks5 proxy to a public host should pass, got %v", err)
	}
	// Bare host:port (no scheme) must still be vetted, not silently trusted:
	// internal blocked, public allowed.
	if err := ValidateOutboundProxy("10.0.0.5:1080"); err == nil {
		t.Error("scheme-less proxy to a private host should be blocked")
	}
	if err := ValidateOutboundProxy("8.8.8.8:1080"); err != nil {
		t.Errorf("scheme-less proxy to a public host should pass, got %v", err)
	}
}
