package common

import "testing"

// TestInitZitaClient_ValidConfigBuildsClient covers the happy path: both env
// vars set to a well-formed http(s) URL + non-empty secret must build a
// non-nil ZitaClient rather than leaving it nil (the SDK-optional default).
func TestInitZitaClient_ValidConfigBuildsClient(t *testing.T) {
	orig := ZitaClient
	t.Cleanup(func() { ZitaClient = orig })
	ZitaClient = nil

	t.Setenv("IDENTITY_PUBLIC_URL", "https://identity.internal.lurus.cn")
	t.Setenv("IDENTITY_SESSION_SECRET", "a-shared-session-secret")

	InitZitaClient()

	if ZitaClient == nil {
		t.Error("ZitaClient must be non-nil after a valid PlatformURL+SessionSecret")
	}
}

// TestInitZitaClient_MalformedURLStaysNil covers the SDK constructor error
// branch: a non-http(s) scheme is rejected by the SDK's own validation, and
// InitZitaClient must swallow that error (log + return) rather than panicking
// or leaving a half-built client installed.
func TestInitZitaClient_MalformedURLStaysNil(t *testing.T) {
	orig := ZitaClient
	t.Cleanup(func() { ZitaClient = orig })
	ZitaClient = nil

	t.Setenv("IDENTITY_PUBLIC_URL", "ftp://identity.internal.lurus.cn")
	t.Setenv("IDENTITY_SESSION_SECRET", "a-shared-session-secret")

	InitZitaClient()

	if ZitaClient != nil {
		t.Error("ZitaClient must stay nil when the SDK rejects a non-http(s) PlatformURL")
	}
}

// TestInitZitaClient_OnlyOneEnvSetStaysDisabled covers the partial-config
// guard: setting only one of the two required env vars must not attempt to
// build a client (matches the "|| " short-circuit in the disabled check).
func TestInitZitaClient_OnlyOneEnvSetStaysDisabled(t *testing.T) {
	orig := ZitaClient
	t.Cleanup(func() { ZitaClient = orig })
	ZitaClient = nil

	t.Setenv("IDENTITY_PUBLIC_URL", "https://identity.internal.lurus.cn")
	t.Setenv("IDENTITY_SESSION_SECRET", "")

	InitZitaClient()

	if ZitaClient != nil {
		t.Error("ZitaClient must stay nil when only IDENTITY_PUBLIC_URL is set")
	}
}
