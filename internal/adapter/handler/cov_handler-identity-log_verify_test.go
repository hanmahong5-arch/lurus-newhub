package handler

// ============================================================================
// UniversalVerify (secure_verification.go) edge cases not already exercised
// by totp_flow_test.go: unauthenticated, disabled account, non-existent
// account, unsupported method while unenrolled, and a corrupted TOTP secret
// surfacing as a decrypt failure mid-verify. Reuses buildTotpFlowRouter /
// setupTotpFlowDB / doJSON from totp_flow_test.go (same package).
// ============================================================================

import (
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	entity "github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// TestHandlerIdentityLog_Verify_Unauthenticated proves the money/session-step-up
// gate refuses a request with no resolved caller before touching the DB.
func TestHandlerIdentityLog_Verify_Unauthenticated(t *testing.T) {
	cleanup := setupTotpFlowDB(t, 40)
	defer cleanup()
	r := buildTotpFlowRouter(0)

	w, env := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"session"}`, nil)
	if w.Code != http.StatusUnauthorized || env.Success {
		t.Fatalf("code=%d success=%v, want 401/false (body=%s)", w.Code, env.Success, w.Body.String())
	}
}

// TestHandlerIdentityLog_Verify_DisabledAccountRejected proves a disabled
// account cannot self-stamp a secure-verification session even though it
// still resolves a caller id — disabled must be checked before granting any
// step-up trust.
func TestHandlerIdentityLog_Verify_DisabledAccountRejected(t *testing.T) {
	cleanup := setupTotpFlowDB(t, 41)
	defer cleanup()
	if err := repo.DB.Model(&repo.User{}).Where("id = ?", 41).Update("status", common.UserStatusDisabled).Error; err != nil {
		t.Fatalf("disable user: %v", err)
	}
	r := buildTotpFlowRouter(41)

	w, env := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"session"}`, nil)
	if w.Code != http.StatusForbidden || env.Success {
		t.Fatalf("code=%d success=%v, want 403/false (body=%s)", w.Code, env.Success, w.Body.String())
	}
	if env.Message != "该用户已被禁用" {
		t.Errorf("message = %q", env.Message)
	}
}

// TestHandlerIdentityLog_Verify_UnknownAccountErrors proves a caller id that
// doesn't resolve to any user record (e.g. deleted between session issuance
// and use) fails closed via ApiError rather than crashing or passing.
func TestHandlerIdentityLog_Verify_UnknownAccountErrors(t *testing.T) {
	cleanup := setupTotpFlowDB(t, 42)
	defer cleanup()
	r := buildTotpFlowRouter(999999) // never seeded

	w, env := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"session"}`, nil)
	if env.Success {
		t.Fatalf("unknown account must not verify successfully: code=%d body=%s", w.Code, w.Body.String())
	}
}

// TestHandlerIdentityLog_Verify_UnenrolledUnsupportedMethod proves that
// before TOTP enrollment, only method "session" is accepted — an unknown
// method string is rejected with a distinct 400 rather than silently
// defaulting to session trust.
func TestHandlerIdentityLog_Verify_UnenrolledUnsupportedMethod(t *testing.T) {
	cleanup := setupTotpFlowDB(t, 43)
	defer cleanup()
	r := buildTotpFlowRouter(43)

	w, env := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"webauthn"}`, nil)
	if w.Code != http.StatusBadRequest || env.Success {
		t.Fatalf("code=%d success=%v, want 400/false (body=%s)", w.Code, env.Success, w.Body.String())
	}
	if env.Message != "不支持的验证方式" {
		t.Errorf("message = %q", env.Message)
	}
}

// TestHandlerIdentityLog_Verify_EmptyBodyDefaultsToSession proves an empty
// request body (legacy caller) degrades to method=session rather than being
// rejected as malformed input.
func TestHandlerIdentityLog_Verify_EmptyBodyDefaultsToSession(t *testing.T) {
	cleanup := setupTotpFlowDB(t, 44)
	defer cleanup()
	r := buildTotpFlowRouter(44)

	w, env := doJSON(t, r, http.MethodPost, "/api/verify", "", nil)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("empty body should legacy-degrade to session verify: code=%d body=%s", w.Code, w.Body.String())
	}
}

// TestHandlerIdentityLog_Verify_EnrolledDecryptFailure proves a corrupted
// stored secret on an *active* enrollment surfaces as a verify failure (not
// a false-positive pass, not a panic) when the caller submits method=totp.
func TestHandlerIdentityLog_Verify_EnrolledDecryptFailure(t *testing.T) {
	cleanup := setupTotpFlowDB(t, 45)
	defer cleanup()
	if err := repo.UpsertUserTOTP(&entity.UserTOTP{
		UserId: 45, SecretEncrypted: "not-a-versioned-ciphertext", Enabled: true,
		CreatedAt: common.GetTimestamp(), ConfirmedAt: common.GetTimestamp(),
	}); err != nil {
		t.Fatalf("seed corrupted enrollment: %v", err)
	}
	r := buildTotpFlowRouter(45)

	w, env := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"totp","code":"123456"}`, nil)
	if env.Success {
		t.Fatalf("corrupted secret must not verify successfully: code=%d body=%s", w.Code, w.Body.String())
	}
}
