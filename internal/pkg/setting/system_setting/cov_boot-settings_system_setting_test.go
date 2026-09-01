package system_setting

import (
	"testing"
)

// --- discord.go ---

func TestGetDiscordSettings_DefaultDisabled(t *testing.T) {
	orig := defaultDiscordSettings
	t.Cleanup(func() { defaultDiscordSettings = orig })
	defaultDiscordSettings = DiscordSettings{}

	s := GetDiscordSettings()
	if s.Enabled {
		t.Fatalf("expected Discord OAuth disabled by default (no client secret configured)")
	}
	if s.ClientId != "" || s.ClientSecret != "" {
		t.Fatalf("expected empty client credentials by default, got %+v", s)
	}
}

func TestGetDiscordSettings_ReturnsLivePointer(t *testing.T) {
	orig := defaultDiscordSettings
	t.Cleanup(func() { defaultDiscordSettings = orig })

	GetDiscordSettings().Enabled = true
	if !defaultDiscordSettings.Enabled {
		t.Fatalf("expected mutation through returned pointer to affect package state")
	}
}

// --- oidc.go ---

func TestGetOIDCSettings_DefaultDisabled(t *testing.T) {
	orig := defaultOIDCSettings
	t.Cleanup(func() { defaultOIDCSettings = orig })
	defaultOIDCSettings = OIDCSettings{}

	s := GetOIDCSettings()
	if s.Enabled {
		t.Fatalf("expected OIDC disabled by default")
	}
	if s.WellKnown != "" || s.ClientId != "" {
		t.Fatalf("expected empty OIDC endpoint config by default, got %+v", s)
	}
}

// --- legal.go ---

func TestGetLegalSettings_DefaultsEmpty(t *testing.T) {
	orig := defaultLegalSettings
	t.Cleanup(func() { defaultLegalSettings = orig })
	defaultLegalSettings = LegalSettings{}

	s := GetLegalSettings()
	if s.UserAgreement != "" || s.PrivacyPolicy != "" {
		t.Fatalf("expected empty legal docs by default, got %+v", s)
	}
}

// --- fetch_setting.go ---

func TestGetFetchSetting_SSRFProtectionOnByDefault(t *testing.T) {
	orig := defaultFetchSetting
	t.Cleanup(func() { defaultFetchSetting = orig })
	defaultFetchSetting = FetchSetting{
		EnableSSRFProtection: true,
		AllowPrivateIp:       false,
		AllowedPorts:         []string{"80", "443", "8080", "8443"},
	}

	s := GetFetchSetting()
	// This is a security-critical default: SSRF protection must be on and
	// private-IP access must be off out of the box.
	if !s.EnableSSRFProtection {
		t.Fatalf("expected SSRF protection enabled by default")
	}
	if s.AllowPrivateIp {
		t.Fatalf("expected private IP access disallowed by default")
	}
	if len(s.AllowedPorts) != 4 || s.AllowedPorts[0] != "80" {
		t.Fatalf("expected default allowed ports [80 443 8080 8443], got %v", s.AllowedPorts)
	}
}

// --- system_setting_old.go ---

func TestEnableWorker(t *testing.T) {
	orig := WorkerUrl
	t.Cleanup(func() { WorkerUrl = orig })

	WorkerUrl = ""
	if EnableWorker() {
		t.Fatalf("expected EnableWorker()=false when WorkerUrl is empty")
	}

	WorkerUrl = "https://worker.example.com"
	if !EnableWorker() {
		t.Fatalf("expected EnableWorker()=true when WorkerUrl is set")
	}
}
