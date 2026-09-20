package middleware

// wire_message_402_test.go — cycle13 L3's real-chain proof that the token
// quota-exhausted/disabled 402 (auth.go's special-case branch around :558,
// fed by repo/token.go's ErrTokenQuotaExhausted-wrapping messages) answers
// in English on all three wire shapes, driven through the real TokenAuth()
// middleware and mountWireRejectionRouter (rejection_envelope_wire_test.go)
// — not a hand-built shape.
//
// Six cells: {OpenAI, Claude, Gemini} × {quota exhausted, disabled-with-
// available-quota}. "disabled-with-available-quota" is repo/token.go's
// Status==TokenStatusExhausted branch when QuotaAvailable() is true (an
// admin raised remain_quota without re-enabling the token) — auth.go routes
// it through the SAME 402 special-case as genuine exhaustion (both wrap
// repo.ErrTokenQuotaExhausted), so both cells answer HTTP 402 with
// errorCode types.ErrorCodeTokenQuotaExhausted.
//
// error.message is asserted ASCII-only on all six cells — the universal
// part of the fix (repo/token.go:159/183/209, cycle13 finding #6).
//
// The plan's oracle additionally asks for "code = token_quota_exhausted" on
// all six cells; empirically (probed against this tree) that literal string
// only appears on the OpenAI wire's error.code field
// (types.ToOpenAIError sets Code: e.errorCode). The Claude wire
// (types.ClaudeError) has no code-shaped field at all — only a vendor-
// taxonomy `type` ("billing_error" for a 402, from types.WireErrorType) —
// and the Gemini wire's `code` field is the HTTP status (402, an int) with
// `status` a google.rpc.Code name ("UNKNOWN" for 402, not in
// types.GeminiStatus's switch). So the OpenAI-specific code assertion is
// checked as the plan describes; the Claude/Gemini cells instead assert
// their own wire-idiomatic 402 markers (type="billing_error", code=402)
// since no "token_quota_exhausted" string exists on those wires to check.
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"gorm.io/gorm"
)

// wire402SeedToken creates a User + Token in the given exhaustion-adjacent
// state and returns the raw token key. status is always
// common.TokenStatusExhausted for this suite's two cells — see the
// file-level comment for what distinguishes them (remain/unlimited).
func wire402SeedToken(t *testing.T, db *gorm.DB, unlimited bool, remain int) string {
	t.Helper()
	sfx := common.GetRandomString(6)
	user := &repo.User{
		Username:    "wire402-" + sfx,
		DisplayName: "Wire402",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "wire402-" + sfx + "@local",
		TenantId:    "default",
		Quota:       1_000_000,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	key := common.GetRandomString(48)
	tok := &repo.Token{
		UserId:         user.Id,
		TenantId:       "default",
		Key:            key,
		Status:         common.TokenStatusExhausted,
		Name:           "wire402-" + sfx,
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		UnlimitedQuota: unlimited,
		RemainQuota:    remain,
	}
	if err := db.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	return key
}

// assertWire402MessageASCII decodes message out of body per the wire shape
// and asserts it contains no rune >= 0x80. Returns the decoded message for
// the caller's additional wire-specific assertions.
func assertWire402MessageASCII(t *testing.T, wire, body string) string {
	t.Helper()
	var message string
	switch wire {
	case "openai":
		var env struct {
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			t.Fatalf("%s: decode: %v; body=%s", wire, err, body)
		}
		message = env.Error.Message
	case "claude":
		var env struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			t.Fatalf("%s: decode: %v; body=%s", wire, err, body)
		}
		message = env.Error.Message
	case "gemini":
		var env struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			t.Fatalf("%s: decode: %v; body=%s", wire, err, body)
		}
		message = env.Error.Message
	default:
		t.Fatalf("unknown wire %q", wire)
	}
	if message == "" {
		t.Fatalf("%s: error.message is empty; body=%s", wire, body)
	}
	for _, r := range message {
		if r >= 0x80 {
			t.Errorf("%s: error.message contains non-ASCII rune %q: %s", wire, r, message)
			break
		}
	}
	return message
}

// TestWireMessage402_TokenQuotaExhausted_AllWiresASCII is the six-cell
// oracle: {OpenAI, Claude, Gemini} × {quota exhausted, disabled-with-
// available-quota}.
func TestWireMessage402_TokenQuotaExhausted_AllWiresASCII(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	exhaustedKey := wire402SeedToken(t, db, false, 0)
	disabledKey := wire402SeedToken(t, db, false, 5000)

	r := mountWireRejectionRouter()

	do := func(path, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		if strings.HasPrefix(path, "/v1beta") {
			req.Header.Set("x-goog-api-key", key)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	cases := []struct {
		name string
		wire string
		path string
		key  string
	}{
		{"openai_exhausted", "openai", "/v1/chat/completions", exhaustedKey},
		{"claude_exhausted", "claude", "/v1/messages", exhaustedKey},
		{"gemini_exhausted", "gemini", "/v1beta/models/gemini-pro:generateContent", exhaustedKey},
		{"openai_disabled", "openai", "/v1/chat/completions", disabledKey},
		{"claude_disabled", "claude", "/v1/messages", disabledKey},
		{"gemini_disabled", "gemini", "/v1beta/models/gemini-pro:generateContent", disabledKey},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := do(tc.path, tc.key)
			if w.Code != http.StatusPaymentRequired {
				t.Fatalf("status = %d, want 402; body=%s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			assertWire402MessageASCII(t, tc.wire, body)

			switch tc.wire {
			case "openai":
				var env struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal([]byte(body), &env); err != nil {
					t.Fatalf("decode: %v; body=%s", err, body)
				}
				if env.Error.Code != "token_quota_exhausted" {
					t.Errorf("openai error.code = %q, want %q", env.Error.Code, "token_quota_exhausted")
				}
			case "claude":
				var env struct {
					Error struct {
						Type string `json:"type"`
					} `json:"error"`
				}
				if err := json.Unmarshal([]byte(body), &env); err != nil {
					t.Fatalf("decode: %v; body=%s", err, body)
				}
				// No code-shaped field exists on the Claude wire (see the
				// file-level comment) — the closest machine-readable 402
				// marker is the vendor-taxonomy type.
				if env.Error.Type != "billing_error" {
					t.Errorf("claude error.type = %q, want %q (the 402 vendor-taxonomy value)", env.Error.Type, "billing_error")
				}
			case "gemini":
				var env struct {
					Error struct {
						Code int `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal([]byte(body), &env); err != nil {
					t.Fatalf("decode: %v; body=%s", err, body)
				}
				if env.Error.Code != http.StatusPaymentRequired {
					t.Errorf("gemini error.code = %d, want %d", env.Error.Code, http.StatusPaymentRequired)
				}
			}
		})
	}
}
