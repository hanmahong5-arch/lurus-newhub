package handler

// ============================================================================
// TOTP step-up: unauthenticated / already-enrolled / already-confirmed /
// decrypt-failure edge cases not covered by totp_flow_test.go's happy-path
// end-to-end walk. Reuses that file's setupTotpFlowDB / buildTotpFlowRouter /
// doJSON scaffolding (same package).
// ============================================================================

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	entity "github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// TestHandlerIdentityLog_Totp_Unauthenticated proves every TOTP endpoint
// refuses a request that never resolved a caller id (id==0), regardless of
// enrollment state, before ever touching the DB.
func TestHandlerIdentityLog_Totp_Unauthenticated(t *testing.T) {
	cleanup := handlerIdentityLogSetupTotpDB(t, 30)
	defer cleanup()
	r := buildTotpFlowRouter(0) // id stuck at 0, mirrors a session that lost the caller.

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"status", http.MethodGet, "/api/user/totp/status", ""},
		{"enroll", http.MethodPost, "/api/user/totp/enroll", "{}"},
		{"confirm", http.MethodPost, "/api/user/totp/confirm", `{"code":"123456"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, env := doJSON(t, r, tc.method, tc.path, tc.body, nil)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("code = %d, want 401 (body=%s)", w.Code, w.Body.String())
			}
			if env.Success {
				t.Errorf("success = true, want false")
			}
			if env.Message != "未登录" {
				t.Errorf("message = %q, want 未登录", env.Message)
			}
		})
	}
}

// TestHandlerIdentityLog_TotpDisable_Unauthenticated calls TotpDisable
// directly (bypassing the SecureVerificationRequired middleware, which is a
// separate concern) to prove the handler's own id==0 guard fires.
func TestHandlerIdentityLog_TotpDisable_Unauthenticated(t *testing.T) {
	cleanup := handlerIdentityLogSetupTotpDB(t, 31)
	defer cleanup()

	c, w := handlerIdentityLogCtx(http.MethodPost, "/api/user/totp/disable", "{}")
	// id left unset -> c.GetInt("id") == 0
	TotpDisable(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401 (body=%s)", w.Code, w.Body.String())
	}
}

// TestHandlerIdentityLog_TotpEnroll_AlreadyEnabledRejected proves re-calling
// enroll on an account that already confirmed a factor is refused (must
// disable first) rather than silently rotating the active secret.
func TestHandlerIdentityLog_TotpEnroll_AlreadyEnabledRejected(t *testing.T) {
	cleanup := handlerIdentityLogSetupTotpDB(t, 32)
	defer cleanup()
	r := buildTotpFlowRouter(32)

	if err := repo.UpsertUserTOTP(&entity.UserTOTP{
		UserId:          32,
		SecretEncrypted: "v1:already-enrolled-marker",
		Enabled:         true,
		CreatedAt:       common.GetTimestamp(),
		ConfirmedAt:     common.GetTimestamp(),
	}); err != nil {
		t.Fatalf("seed enrollment: %v", err)
	}

	w, env := doJSON(t, r, http.MethodPost, "/api/user/totp/enroll", "{}", nil)
	if w.Code != http.StatusBadRequest || env.Success {
		t.Fatalf("re-enroll while active: code=%d success=%v body=%s", w.Code, env.Success, w.Body.String())
	}
	if env.Message != "两步验证已启用，如需重新配置请先禁用" {
		t.Errorf("message = %q", env.Message)
	}
	// The pre-existing active secret must survive untouched — no silent rotation.
	rec, err := repo.GetUserTOTP(32)
	if err != nil || rec == nil || rec.SecretEncrypted != "v1:already-enrolled-marker" {
		t.Fatalf("active secret was mutated by a rejected enroll call: %+v err=%v", rec, err)
	}
}

// TestHandlerIdentityLog_TotpConfirm_EdgeCases covers the confirm branches
// that never touch a real code: missing enrollment record, an already-active
// enrollment, and a malformed body.
func TestHandlerIdentityLog_TotpConfirm_EdgeCases(t *testing.T) {
	t.Run("never_enrolled", func(t *testing.T) {
		cleanup := handlerIdentityLogSetupTotpDB(t, 33)
		defer cleanup()
		r := buildTotpFlowRouter(33)

		w, env := doJSON(t, r, http.MethodPost, "/api/user/totp/confirm", `{"code":"123456"}`, nil)
		if w.Code != http.StatusBadRequest || env.Success {
			t.Fatalf("confirm without enrollment: code=%d success=%v", w.Code, env.Success)
		}
		if env.Message != "请先获取两步验证密钥" {
			t.Errorf("message = %q", env.Message)
		}
	})

	t.Run("already_active", func(t *testing.T) {
		cleanup := handlerIdentityLogSetupTotpDB(t, 34)
		defer cleanup()
		r := buildTotpFlowRouter(34)
		if err := repo.UpsertUserTOTP(&entity.UserTOTP{
			UserId: 34, SecretEncrypted: "v1:x", Enabled: true,
			CreatedAt: common.GetTimestamp(), ConfirmedAt: common.GetTimestamp(),
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		w, env := doJSON(t, r, http.MethodPost, "/api/user/totp/confirm", `{"code":"123456"}`, nil)
		if w.Code != http.StatusBadRequest || env.Success {
			t.Fatalf("confirm already active: code=%d success=%v", w.Code, env.Success)
		}
		if env.Message != "两步验证已启用" {
			t.Errorf("message = %q", env.Message)
		}
	})

	t.Run("empty_code_rejected_before_db", func(t *testing.T) {
		cleanup := handlerIdentityLogSetupTotpDB(t, 35)
		defer cleanup()
		r := buildTotpFlowRouter(35)
		w, env := doJSON(t, r, http.MethodPost, "/api/user/totp/confirm", `{"code":""}`, nil)
		if w.Code != http.StatusBadRequest || env.Success {
			t.Fatalf("empty code: code=%d success=%v", w.Code, env.Success)
		}
		if env.Message != "请输入验证码" {
			t.Errorf("message = %q", env.Message)
		}
	})

	t.Run("malformed_json_rejected", func(t *testing.T) {
		cleanup := handlerIdentityLogSetupTotpDB(t, 36)
		defer cleanup()
		r := buildTotpFlowRouter(36)
		w, env := doJSON(t, r, http.MethodPost, "/api/user/totp/confirm", `{not-json`, nil)
		if w.Code != http.StatusBadRequest || env.Success {
			t.Fatalf("malformed json: code=%d success=%v", w.Code, env.Success)
		}
	})

	t.Run("decrypt_failure_surfaces_as_api_error", func(t *testing.T) {
		// A pending record whose ciphertext lost the "v1:" version prefix (e.g.
		// corrupted storage / manual DB edit) must not be treated as a valid
		// code mismatch — it is a distinct decrypt failure.
		cleanup := handlerIdentityLogSetupTotpDB(t, 37)
		defer cleanup()
		r := buildTotpFlowRouter(37)
		if err := repo.UpsertUserTOTP(&entity.UserTOTP{
			UserId: 37, SecretEncrypted: "corrupted-no-version-prefix", Enabled: false,
			CreatedAt: common.GetTimestamp(),
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		w, env := doJSON(t, r, http.MethodPost, "/api/user/totp/confirm", `{"code":"123456"}`, nil)
		if env.Success {
			t.Fatalf("decrypt failure must not report success: code=%d body=%s", w.Code, w.Body.String())
		}
		if !strings.Contains(env.Message, "decrypt") {
			t.Errorf("message should surface the decrypt failure, got %q", env.Message)
		}
		// The pending record must survive (still awaiting confirm), not be
		// silently dropped by the failed attempt.
		rec, err := repo.GetUserTOTP(37)
		if err != nil || rec == nil || rec.Enabled {
			t.Fatalf("pending enrollment must remain pending after decrypt failure: rec=%+v err=%v", rec, err)
		}
	})
}

// TestHandlerIdentityLog_TotpDisable_NeverEnrolled proves disabling a factor
// that was never enrolled is a rejected no-op, not a silent success.
func TestHandlerIdentityLog_TotpDisable_NeverEnrolled(t *testing.T) {
	cleanup := handlerIdentityLogSetupTotpDB(t, 38)
	defer cleanup()

	c, w := handlerIdentityLogCtx(http.MethodPost, "/api/user/totp/disable", "{}")
	c.Set("id", 38)
	TotpDisable(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	body := r2authBody(t, w)
	if success, _ := body["success"].(bool); success {
		t.Errorf("success = true, want false")
	}
	if body["message"] != "两步验证未启用" {
		t.Errorf("message = %v", body["message"])
	}
}

// TestHandlerIdentityLog_TotpEnroll_AccountFallsBackToEmail proves a user
// with no username still gets a usable otpauth:// label (falls back to
// email) instead of an empty account identifier that authenticator apps
// would render blank.
func TestHandlerIdentityLog_TotpEnroll_AccountFallsBackToEmail(t *testing.T) {
	cleanup := handlerIdentityLogSetupTotpDB(t, 39)
	defer cleanup()
	// Blank the seeded username, keep only email, so TotpEnroll's fallback fires.
	if err := repo.DB.Model(&repo.User{}).Where("id = ?", 39).Update("username", "").Error; err != nil {
		t.Fatalf("blank username: %v", err)
	}
	r := buildTotpFlowRouter(39)

	w, env := doJSON(t, r, http.MethodPost, "/api/user/totp/enroll", "{}", nil)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("enroll: code=%d body=%s", w.Code, w.Body.String())
	}
	var data struct {
		OtpauthURL string `json:"otpauth_url"`
	}
	_ = decodeEnvData(t, env, &data)
	wantLabel := "totp39@test.local" // seeded email for user id 39
	if !handlerIdentityLogContains(data.OtpauthURL, wantLabel) {
		t.Errorf("otpauth_url = %q, expected it to carry the email fallback %q", data.OtpauthURL, wantLabel)
	}
}

// --- local helpers (handler_identity_log-prefixed to avoid clashing with the
// package's other cov_ files and pre-existing scaffolding) ---

func handlerIdentityLogSetupTotpDB(t *testing.T, userId int) func() {
	return setupTotpFlowDB(t, userId)
}

func handlerIdentityLogCtx(method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, w
}

func decodeEnvData(t *testing.T, env apiEnvelope, out interface{}) error {
	t.Helper()
	return json.Unmarshal(env.Data, out)
}

func handlerIdentityLogContains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
