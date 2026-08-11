package setting

import (
	"os"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// OIDC_CONSUMER_AUD_REQUIRED three-state gradual-enforcement flag for the
// audience ("aud") check on the consumer-facing OIDC gate
// (middleware.RequireOIDCToken, currently protecting POST /api/v2/lutu/search).
//
// Why this gate needs its own flag instead of reusing checkAudience directly:
// the tenant-mapped gate (OIDCAuth) accepts OIDC_CLIENT_ID — newhub's own
// client — as an audience, and that is correct there. The consumer gate is
// reached by first-party CONSUMER apps that each hold their OWN client_id
// (e.g. the Lutu APP), so a token legitimately issued to such an app never
// carries newhub's client_id in "aud". Enforcing the tenant allow-list here
// unchanged would 401 every legitimate consumer caller.
//
//	off     — issuer-only, byte-identical to the pre-flag behaviour.
//	log     (default) — validates and structured-logs a mismatch, but still
//	          admits the request. Costs nothing behaviourally and is the only
//	          way ops can learn which "aud" values real callers actually carry
//	          before committing to an allow-list.
//	enforce — reject (401) a token whose "aud" matches neither
//	          OIDC_CLIENT_ID, OIDC_ALLOWED_AUDIENCES nor
//	          OIDC_CONSUMER_AUDIENCES.
//
// The default differs from CREDIT_POOL_REQUIRED's "off" on purpose: that flag
// guards a hot relay path where "log" would be per-request noise, whereas this
// one guards a single low-volume route that is unreachable altogether unless
// OIDC_ENABLED=true — so "log" carries no production cost and closes the
// observability gap without an extra deploy cycle.
const (
	ConsumerAudRequiredOff     = "off"
	ConsumerAudRequiredLog     = "log"
	ConsumerAudRequiredEnforce = "enforce"
)

// GetConsumerAudRequired reads OIDC_CONSUMER_AUD_REQUIRED fresh on every call
// (same os.Getenv-per-call style as GetCreditPoolRequired, so tests can flip it
// with t.Setenv without a process restart).
//
// Any unrecognized value degrades to "log" — never to "enforce", so a typo can
// never lock out legitimate consumer traffic — and is reported via SysError.
func GetConsumerAudRequired() string {
	raw := os.Getenv("OIDC_CONSUMER_AUD_REQUIRED")
	switch raw {
	case "", ConsumerAudRequiredLog:
		return ConsumerAudRequiredLog
	case ConsumerAudRequiredOff:
		return ConsumerAudRequiredOff
	case ConsumerAudRequiredEnforce:
		return ConsumerAudRequiredEnforce
	default:
		common.SysError("invalid OIDC_CONSUMER_AUD_REQUIRED=" + raw + ", falling back to log (never fail-open to enforce)")
		return ConsumerAudRequiredLog
	}
}
