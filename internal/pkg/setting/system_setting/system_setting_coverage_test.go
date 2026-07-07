package system_setting

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// snapshot/restore helpers keep this file -count=1 safe by resetting all
// mutable package-level state touched by the tests below.

func snapshotGlobals(t *testing.T) {
	t.Helper()

	origServerAddress := ServerAddress
	origWorkerUrl := WorkerUrl
	origWorkerValidKey := WorkerValidKey
	origWorkerAllowHttp := WorkerAllowHttpImageRequestEnabled
	origDiscord := defaultDiscordSettings
	origFetch := defaultFetchSetting
	origLegal := defaultLegalSettings
	origOIDC := defaultOIDCSettings
	origPasskey := defaultPasskeySettings

	t.Cleanup(func() {
		ServerAddress = origServerAddress
		WorkerUrl = origWorkerUrl
		WorkerValidKey = origWorkerValidKey
		WorkerAllowHttpImageRequestEnabled = origWorkerAllowHttp
		defaultDiscordSettings = origDiscord
		defaultFetchSetting = origFetch
		defaultLegalSettings = origLegal
		defaultOIDCSettings = origOIDC
		defaultPasskeySettings = origPasskey
	})
}

func TestGetDiscordSettings(t *testing.T) {
	snapshotGlobals(t)

	defaultDiscordSettings = DiscordSettings{
		Enabled:      true,
		ClientId:     "cid",
		ClientSecret: "secret",
	}

	got := GetDiscordSettings()
	if got != &defaultDiscordSettings {
		t.Fatalf("GetDiscordSettings() should return pointer to package-level default")
	}
	if !got.Enabled || got.ClientId != "cid" || got.ClientSecret != "secret" {
		t.Fatalf("unexpected DiscordSettings: %+v", *got)
	}
}

func TestGetFetchSetting(t *testing.T) {
	snapshotGlobals(t)

	got := GetFetchSetting()
	if got != &defaultFetchSetting {
		t.Fatalf("GetFetchSetting() should return pointer to package-level default")
	}
	// verify the exact default values as documented in fetch_setting.go
	if !got.EnableSSRFProtection {
		t.Fatalf("EnableSSRFProtection default should be true")
	}
	if got.AllowPrivateIp || got.DomainFilterMode || got.IpFilterMode || got.ApplyIPFilterForDomain {
		t.Fatalf("boolean flags should default to false: %+v", *got)
	}
	if len(got.DomainList) != 0 {
		t.Fatalf("DomainList should default to empty slice, got %v", got.DomainList)
	}
	if len(got.IpList) != 0 {
		t.Fatalf("IpList should default to empty slice, got %v", got.IpList)
	}
	wantPorts := []string{"80", "443", "8080", "8443"}
	if len(got.AllowedPorts) != len(wantPorts) {
		t.Fatalf("AllowedPorts length mismatch: got %v want %v", got.AllowedPorts, wantPorts)
	}
	for i, p := range wantPorts {
		if got.AllowedPorts[i] != p {
			t.Fatalf("AllowedPorts[%d] = %q, want %q", i, got.AllowedPorts[i], p)
		}
	}

	// mutate and re-fetch to prove the getter reflects live state, not a copy
	got.EnableSSRFProtection = false
	got2 := GetFetchSetting()
	if got2.EnableSSRFProtection {
		t.Fatalf("expected mutation via returned pointer to persist")
	}
}

func TestGetLegalSettings(t *testing.T) {
	snapshotGlobals(t)

	defaultLegalSettings = LegalSettings{
		UserAgreement: "ua-text",
		PrivacyPolicy: "pp-text",
	}
	got := GetLegalSettings()
	if got != &defaultLegalSettings {
		t.Fatalf("GetLegalSettings() should return pointer to package-level default")
	}
	if got.UserAgreement != "ua-text" || got.PrivacyPolicy != "pp-text" {
		t.Fatalf("unexpected LegalSettings: %+v", *got)
	}
}

func TestGetOIDCSettings(t *testing.T) {
	snapshotGlobals(t)

	defaultOIDCSettings = OIDCSettings{
		Enabled:               true,
		ClientId:              "oidc-client",
		ClientSecret:          "oidc-secret",
		WellKnown:             "https://issuer.example/.well-known/openid-configuration",
		AuthorizationEndpoint: "https://issuer.example/authorize",
		TokenEndpoint:         "https://issuer.example/token",
		UserInfoEndpoint:      "https://issuer.example/userinfo",
	}
	got := GetOIDCSettings()
	if got != &defaultOIDCSettings {
		t.Fatalf("GetOIDCSettings() should return pointer to package-level default")
	}
	if got.WellKnown != "https://issuer.example/.well-known/openid-configuration" {
		t.Fatalf("unexpected WellKnown: %s", got.WellKnown)
	}
	if got.AuthorizationEndpoint != "https://issuer.example/authorize" {
		t.Fatalf("unexpected AuthorizationEndpoint: %s", got.AuthorizationEndpoint)
	}
	if got.TokenEndpoint != "https://issuer.example/token" {
		t.Fatalf("unexpected TokenEndpoint: %s", got.TokenEndpoint)
	}
	if got.UserInfoEndpoint != "https://issuer.example/userinfo" {
		t.Fatalf("unexpected UserInfoEndpoint: %s", got.UserInfoEndpoint)
	}
}

// TestGetPasskeySettings_DefaultDisplayName exercises the plain getter path
// where RPID/Origins are already populated so no derivation branches fire.
func TestGetPasskeySettings_DefaultDisplayName(t *testing.T) {
	snapshotGlobals(t)

	defaultPasskeySettings = PasskeySettings{
		RPDisplayName: common.SystemName,
		RPID:          "already-set.example",
		Origins:       "https://already-set.example",
	}
	ServerAddress = "https://ignored.example"

	got := GetPasskeySettings()
	if got.RPID != "already-set.example" {
		t.Fatalf("RPID should not be overwritten when already set, got %q", got.RPID)
	}
	if got.Origins != "https://already-set.example" {
		t.Fatalf("Origins should not be overwritten when already set, got %q", got.Origins)
	}
	if got.RPDisplayName != common.SystemName {
		t.Fatalf("RPDisplayName mismatch: got %q want %q", got.RPDisplayName, common.SystemName)
	}
}

// TestGetPasskeySettings_DerivesRPIDFromValidURL covers the branch where RPID
// is empty, ServerAddress is a parseable absolute URL with a host, and RPID
// gets set to that host.
func TestGetPasskeySettings_DerivesRPIDFromValidURL(t *testing.T) {
	snapshotGlobals(t)

	defaultPasskeySettings = PasskeySettings{}
	ServerAddress = "https://newapi.pro:8443/base"

	got := GetPasskeySettings()
	if got.RPID != "newapi.pro:8443" {
		t.Fatalf("expected RPID derived from URL host, got %q", got.RPID)
	}
	if got.Origins != "https://newapi.pro:8443/base" {
		t.Fatalf("expected Origins to fall back to ServerAddress, got %q", got.Origins)
	}
}

// TestGetPasskeySettings_FallsBackToRawServerAddress covers the branch where
// ServerAddress does not parse to a URL with a host (e.g. bare host:port with
// no scheme), so RPID falls back to the trimmed raw ServerAddress string.
func TestGetPasskeySettings_FallsBackToRawServerAddress(t *testing.T) {
	snapshotGlobals(t)

	defaultPasskeySettings = PasskeySettings{}
	ServerAddress = "  plain-host-no-scheme:9999  "

	got := GetPasskeySettings()
	if got.RPID != "plain-host-no-scheme:9999" {
		t.Fatalf("expected RPID to fall back to trimmed raw ServerAddress, got %q", got.RPID)
	}
}

// TestGetPasskeySettings_OriginsPlaceholderResets covers the Origins == "[]"
// legacy placeholder branch, which should be replaced by ServerAddress too.
func TestGetPasskeySettings_OriginsPlaceholderResets(t *testing.T) {
	snapshotGlobals(t)

	defaultPasskeySettings = PasskeySettings{
		RPID:    "already-set.example",
		Origins: "[]",
	}
	ServerAddress = "https://real-origin.example"

	got := GetPasskeySettings()
	if got.Origins != "https://real-origin.example" {
		t.Fatalf("expected Origins placeholder [] to be replaced by ServerAddress, got %q", got.Origins)
	}
}

// TestGetPasskeySettings_EmptyServerAddressLeavesRPIDEmpty covers the guard
// where ServerAddress is empty, so RPID stays empty (no derivation attempted).
func TestGetPasskeySettings_EmptyServerAddressLeavesRPIDEmpty(t *testing.T) {
	snapshotGlobals(t)

	defaultPasskeySettings = PasskeySettings{}
	ServerAddress = ""

	got := GetPasskeySettings()
	if got.RPID != "" {
		t.Fatalf("expected RPID to remain empty when ServerAddress is empty, got %q", got.RPID)
	}
	if got.Origins != "" {
		t.Fatalf("expected Origins to remain empty when ServerAddress is empty, got %q", got.Origins)
	}
}

func TestEnableWorker(t *testing.T) {
	snapshotGlobals(t)

	tests := []struct {
		name      string
		workerUrl string
		want      bool
	}{
		{name: "empty worker url disables worker", workerUrl: "", want: false},
		{name: "non-empty worker url enables worker", workerUrl: "https://worker.example", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			WorkerUrl = tt.workerUrl
			if got := EnableWorker(); got != tt.want {
				t.Fatalf("EnableWorker() with WorkerUrl=%q = %v, want %v", tt.workerUrl, got, tt.want)
			}
		})
	}
}
