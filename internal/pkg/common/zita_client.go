package common

import (
	"os"

	zita "github.com/hanmahong5-arch/zita-sdk-go"
)

// ZitaClient is the process-wide Lurus identity SDK client. nil when
// IDENTITY_PUBLIC_URL or IDENTITY_SESSION_SECRET is unset — handlers
// that need identity should guard with `if common.ZitaClient == nil`
// and fall back to the legacy OIDC direct path during the
// migration window (Layer C, ADR-0011).
var ZitaClient *zita.Client

// InitZitaClient builds the SDK client from IDENTITY_PUBLIC_URL +
// IDENTITY_SESSION_SECRET. Missing config is non-fatal — the SDK is
// optional during migration. Bad config (e.g. malformed URL) is
// logged and ignored; ZitaClient stays nil.
func InitZitaClient() {
	publicURL := os.Getenv("IDENTITY_PUBLIC_URL")
	sessionSecret := os.Getenv("IDENTITY_SESSION_SECRET")
	if publicURL == "" || sessionSecret == "" {
		SysLog("zita SDK disabled: IDENTITY_PUBLIC_URL or IDENTITY_SESSION_SECRET not set")
		return
	}
	client, err := zita.NewClient(zita.Config{
		PlatformURL:   publicURL,
		SessionSecret: sessionSecret,
	})
	if err != nil {
		SysError("zita SDK init failed: " + err.Error())
		return
	}
	ZitaClient = client
	SysLog("zita SDK initialized (PlatformURL=" + publicURL + ")")
}
