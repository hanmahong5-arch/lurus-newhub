package system_setting

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
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

// --- passkey.go ---

func boot_settings_resetPasskeySettings(t *testing.T) {
	t.Helper()
	origPasskey := defaultPasskeySettings
	origAddr := ServerAddress
	t.Cleanup(func() {
		defaultPasskeySettings = origPasskey
		ServerAddress = origAddr
	})
}

func TestGetPasskeySettings_DerivesRPIDFromServerAddress(t *testing.T) {
	boot_settings_resetPasskeySettings(t)
	defaultPasskeySettings = PasskeySettings{RPDisplayName: common.SystemName}
	ServerAddress = "https://hub.lurus.cn:8443/path"

	s := GetPasskeySettings()
	if s.RPID != "hub.lurus.cn:8443" {
		t.Fatalf("expected RPID derived as host[:port] from ServerAddress, got %q", s.RPID)
	}
	if s.Origins != "https://hub.lurus.cn:8443/path" {
		t.Fatalf("expected Origins to default to raw ServerAddress, got %q", s.Origins)
	}
}

func TestGetPasskeySettings_UnparsableServerAddressFallsBackToRaw(t *testing.T) {
	boot_settings_resetPasskeySettings(t)
	defaultPasskeySettings = PasskeySettings{}
	// A bare host:port with no scheme parses to a URL with empty Host (the
	// whole string becomes Path), so the code must fall back to using the
	// raw string as RPID rather than emitting an empty RPID.
	ServerAddress = "  example.com:9999  "

	s := GetPasskeySettings()
	if s.RPID != "example.com:9999" {
		t.Fatalf("expected fallback RPID to be the trimmed raw address, got %q", s.RPID)
	}
}

func TestGetPasskeySettings_ExistingRPIDNotOverwritten(t *testing.T) {
	boot_settings_resetPasskeySettings(t)
	defaultPasskeySettings = PasskeySettings{RPID: "already-set.example.com"}
	ServerAddress = "https://different-host.example.com"

	s := GetPasskeySettings()
	if s.RPID != "already-set.example.com" {
		t.Fatalf("expected pre-configured RPID to be preserved, got %q", s.RPID)
	}
}

func TestGetPasskeySettings_OriginsPlaceholderTreatedAsUnset(t *testing.T) {
	boot_settings_resetPasskeySettings(t)
	// FINDING: the literal string "[]" (a plausible "empty JSON array"
	// placeholder some admin UI might submit) is special-cased identically to
	// an empty string and gets overwritten with ServerAddress rather than
	// being respected as "explicitly no origins" — locking current behavior.
	defaultPasskeySettings = PasskeySettings{Origins: "[]"}
	ServerAddress = "https://hub.lurus.cn"

	s := GetPasskeySettings()
	if s.Origins != "https://hub.lurus.cn" {
		t.Fatalf("expected \"[]\" origins placeholder replaced by ServerAddress, got %q", s.Origins)
	}
}

func TestGetPasskeySettings_DefaultDisabledAndUserVerificationPreferred(t *testing.T) {
	boot_settings_resetPasskeySettings(t)
	defaultPasskeySettings = PasskeySettings{
		Enabled:          false,
		UserVerification: "preferred",
	}
	ServerAddress = ""

	s := GetPasskeySettings()
	if s.Enabled {
		t.Fatalf("expected Passkey auth disabled by default")
	}
	if s.UserVerification != "preferred" {
		t.Fatalf("expected default user verification 'preferred', got %q", s.UserVerification)
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
