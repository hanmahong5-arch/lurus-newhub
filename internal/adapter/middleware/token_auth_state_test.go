package middleware

// token_auth_state_test.go — cycle 18 lane L3: every TokenAuth rejection
// must say WHY on the wire so the newapi bridge (hub-bridge.yaml's
// `error_page 401 = @newapi`) can replay only the keys the hub genuinely does
// not know. Before this lane a disabled/expired key and a transient DB
// failure all collapsed into the same 401 the bridge treats as "not ours" —
// a key revoked here kept working through newapi, and one DB hiccup moved
// the whole request (billing included) onto the other gateway silently.
//
// Contract locked here:
//   - unknown key            → 401 invalid_request, X-Lurus-Token-State: unknown
//   - TokenStatusDisabled    → 401 token_disabled,  X-Lurus-Token-State: disabled
//   - ExpiredTime in the past→ 401 token_expired,   X-Lurus-Token-State: expired
//   - DB lookup failure      → 503 query_data_error, Retry-After: 5,
//     X-Lurus-Token-State: lookup_failed
//
// The 401 status codes for disabled/expired are public contract
// (doc/product-integration-guide.md §B, TestL3TokenAuth_Disabled_Still401)
// and stay; only the lookup-failure path changes status.
//
// Harness: l3SetupTokenQuotaRouter/l3DoProbe from l3_token_quota_402_test.go.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestTokenAuthState_UnknownKey_401Unknown(t *testing.T) {
	r, _, cleanup := l3SetupTokenQuotaRouter(t, common.TokenStatusEnabled, true, 0)
	defer cleanup()

	w := l3DoProbe(t, r, common.GetRandomString(48))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get(tokenStateHeader); got != tokenStateUnknown {
		t.Errorf("%s = %q, want %q", tokenStateHeader, got, tokenStateUnknown)
	}
	if !strings.Contains(w.Body.String(), `"code":"invalid_request"`) {
		t.Errorf(`body = %s, want error.code "invalid_request"`, w.Body.String())
	}
}

func TestTokenAuthState_Disabled_401Disabled(t *testing.T) {
	r, key, cleanup := l3SetupTokenQuotaRouter(t, common.TokenStatusDisabled, true, 1000)
	defer cleanup()

	w := l3DoProbe(t, r, key)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get(tokenStateHeader); got != tokenStateDisabled {
		t.Errorf("%s = %q, want %q", tokenStateHeader, got, tokenStateDisabled)
	}
	if !strings.Contains(w.Body.String(), `"code":"token_disabled"`) {
		t.Errorf(`body = %s, want error.code "token_disabled"`, w.Body.String())
	}
}

func TestTokenAuthState_ExpiredByTime_401Expired(t *testing.T) {
	r, key, cleanup := l3SetupTokenQuotaRouter(t, common.TokenStatusEnabled, true, 1000)
	defer cleanup()
	if err := repo.DB.Model(&repo.Token{}).Where("`key` = ?", key).Update("expired_time", common.GetTimestamp()-1).Error; err != nil {
		t.Fatalf("set expired_time: %v", err)
	}

	w := l3DoProbe(t, r, key)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get(tokenStateHeader); got != tokenStateExpired {
		t.Errorf("%s = %q, want %q", tokenStateHeader, got, tokenStateExpired)
	}
	if !strings.Contains(w.Body.String(), `"code":"token_expired"`) {
		t.Errorf(`body = %s, want error.code "token_expired"`, w.Body.String())
	}
}

// TestTokenAuthState_LookupFailed_503 is the headline: a closed connection
// makes DB.First return a non-NotFound error, which must NOT be dressed up
// as an auth failure — the bridge would replay it to newapi and the caller
// would be billed there.
func TestTokenAuthState_LookupFailed_503(t *testing.T) {
	r, key, cleanup := l3SetupTokenQuotaRouter(t, common.TokenStatusEnabled, true, 1000)
	defer cleanup()
	sqlDB, err := repo.DB.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql.DB: %v", err)
	}

	w := l3DoProbe(t, r, key)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (lookup failure is not an auth verdict); body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get(tokenStateHeader); got != tokenStateLookupFailed {
		t.Errorf("%s = %q, want %q", tokenStateHeader, got, tokenStateLookupFailed)
	}
	if got := w.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q, want %q", got, "5")
	}
	if !strings.Contains(w.Body.String(), `"code":"query_data_error"`) {
		t.Errorf(`body = %s, want error.code "query_data_error"`, w.Body.String())
	}
	if strings.Contains(w.Body.String(), key[:3]) {
		t.Errorf("response body leaks key prefix: %s", w.Body.String())
	}
}
