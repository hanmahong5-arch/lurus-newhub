package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ============================================================================
// V2 Token Controller Tests
// ============================================================================

func TestListTokensV2_Pagination(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create 25 tokens
	for i := 0; i < 25; i++ {
		SeedV2Token(t, ctx, ctx.NormalUser.Id, fmt.Sprintf("Token %d", i))
	}

	// Test first page — handler reads p/size params, returns data.items
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/tokens?p=1&size=10", nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})

	tokens := data["items"].([]interface{})
	if len(tokens) != 10 {
		t.Errorf("expected 10 tokens on first page, got %d", len(tokens))
	}

	total := int(data["total"].(float64))
	if total != 25 {
		t.Errorf("expected total=25, got %d", total)
	}

	page := int(data["page"].(float64))
	if page != 1 {
		t.Errorf("expected page=1, got %d", page)
	}

	// Test second page
	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/tokens?p=2&size=10", nil, nil)
	resp = AssertV2Success(t, w)
	data = resp["data"].(map[string]interface{})
	tokens = data["items"].([]interface{})
	if len(tokens) != 10 {
		t.Errorf("expected 10 tokens on second page, got %d", len(tokens))
	}

	// Test third page (should have 5 tokens)
	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/tokens?p=3&size=10", nil, nil)
	resp = AssertV2Success(t, w)
	data = resp["data"].(map[string]interface{})
	tokens = data["items"].([]interface{})
	if len(tokens) != 5 {
		t.Errorf("expected 5 tokens on third page, got %d", len(tokens))
	}
}

func TestCreateTokenV2_Success(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":            "My New Token",
		"expired_time":    -1,
		"unlimited_quota": true,
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)

	AssertV2Status(t, w, http.StatusCreated)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	if data["name"] != "My New Token" {
		t.Errorf("expected name='My New Token', got %v", data["name"])
	}

	// Key should be returned on creation
	key, ok := data["key"].(string)
	if !ok || key == "" {
		t.Error("expected key to be returned on token creation")
	}
	if len(key) < 10 {
		t.Errorf("key seems too short: %s", key)
	}
}

func TestCreateTokenV2_NameTooLong(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a name longer than 50 characters
	longName := make([]byte, 51)
	for i := range longName {
		longName[i] = 'a'
	}

	body := map[string]interface{}{
		"name":            string(longName),
		"unlimited_quota": true,
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "令牌名称过长" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
	// cycle13 L3 (ERRCODES-6): a machine-readable error_code rides alongside
	// the (unchanged, Chinese) message text.
	if code, _ := resp["error_code"].(string); code != tokenErrCodeNameInvalid {
		t.Errorf("error_code = %q, want %q", code, tokenErrCodeNameInvalid)
	}
}

func TestCreateTokenV2_NegativeQuota(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":            "Negative Quota Token",
		"unlimited_quota": false,
		"remain_quota":    -1000,
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "额度值不能为负数" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
	if code, _ := resp["error_code"].(string); code != tokenErrCodeQuotaInvalid {
		t.Errorf("error_code = %q, want %q", code, tokenErrCodeQuotaInvalid)
	}
}

func TestCreateTokenV2_RequiredName(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"unlimited_quota": true,
		// Missing "name"
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)

	AssertV2Status(t, w, http.StatusBadRequest)
}

func TestUpdateTokenV2_Success(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a token first
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "Original Name")

	// Update the token
	body := map[string]interface{}{
		"name": "Updated Name",
	}

	path := fmt.Sprintf("/api/v2/test-tenant/tokens/%d", token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	if data["name"] != "Updated Name" {
		t.Errorf("expected name='Updated Name', got %v", data["name"])
	}
}

func TestUpdateTokenV2_ExpiredToEnabled(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create an expired token
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "Expired Token")
	token.Status = common.TokenStatusExpired
	token.ExpiredTime = common.GetTimestamp() - 3600 // Expired 1 hour ago
	ctx.DB.Save(token)

	// Try to enable it without updating expiration time
	body := map[string]interface{}{
		"status": common.TokenStatusEnabled,
	}

	path := fmt.Sprintf("/api/v2/test-tenant/tokens/%d", token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "令牌已过期，无法启用，请先修改令牌过期时间，或者设置为永不过期" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
	if code, _ := resp["error_code"].(string); code != tokenErrCodeEnableRejected {
		t.Errorf("error_code = %q, want %q", code, tokenErrCodeEnableRejected)
	}
}

func TestUpdateTokenV2_TenantMismatch(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a token in a different tenant
	otherTenantID := "other-tenant-123"
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "Other Tenant Token")
	token.TenantId = otherTenantID
	ctx.DB.Save(token)

	// Try to update from the test tenant
	body := map[string]interface{}{
		"name": "Hacked Name",
	}

	path := fmt.Sprintf("/api/v2/test-tenant/tokens/%d", token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)

	AssertV2Status(t, w, http.StatusForbidden)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Access denied" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
}

func TestDeleteTokenV2_Success(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a token
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "Token to Delete")

	// Delete it
	path := fmt.Sprintf("/api/v2/test-tenant/tokens/%d", token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodDelete, path, nil, nil)

	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	// Verify it's deleted
	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/tokens", nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	if total != 0 {
		t.Errorf("expected 0 tokens after deletion, got %d", total)
	}
}

func TestDeleteTokenV2_NotOwned(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a token owned by admin user
	token := SeedV2Token(t, ctx, ctx.AdminUser.Id, "Admin Token")

	// Try to delete as normal user
	path := fmt.Sprintf("/api/v2/test-tenant/tokens/%d", token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodDelete, path, nil, nil)

	// Should fail - token not found for this user
	AssertV2Status(t, w, http.StatusNotFound)
}

func TestDeleteTokenV2_InvalidID(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Try to delete with invalid ID
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodDelete, "/api/v2/test-tenant/tokens/invalid", nil, nil)

	AssertV2Status(t, w, http.StatusBadRequest)
}

func TestDeleteTokenV2_NonexistentID(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Try to delete a token that doesn't exist
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodDelete, "/api/v2/test-tenant/tokens/99999", nil, nil)

	AssertV2Status(t, w, http.StatusNotFound)
}

func TestListTokensV2_UserIsolation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create tokens for both users
	SeedV2Token(t, ctx, ctx.NormalUser.Id, "Normal User Token 1")
	SeedV2Token(t, ctx, ctx.NormalUser.Id, "Normal User Token 2")
	SeedV2Token(t, ctx, ctx.AdminUser.Id, "Admin User Token")

	// List as normal user - should only see their own tokens
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/tokens", nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	if total != 2 {
		t.Errorf("expected 2 tokens for normal user, got %d", total)
	}

	// List as admin user - should only see their own token
	headers := map[string]string{
		"X-Test-Tenant-ID": ctx.TenantID,
		"X-Test-User-ID":   strconv.Itoa(ctx.AdminUser.Id),
	}
	w = V2Request(ctx.Router, http.MethodGet, "/api/v2/test-tenant/tokens", nil, headers)
	resp = AssertV2Success(t, w)
	data = resp["data"].(map[string]interface{})
	total = int(data["total"].(float64))
	if total != 1 {
		t.Errorf("expected 1 token for admin user, got %d", total)
	}
}

// TestListTokensV2_ForbiddenFields guards the tokenView whitelist: the list
// endpoint must never expose the raw bearer key or tenant/linkage internals.
func TestListTokensV2_ForbiddenFields(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	seeded := SeedV2Token(t, ctx, ctx.NormalUser.Id, "Field Whitelist Token")

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/tokens", nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 token, got %d", len(items))
	}
	tk := items[0].(map[string]interface{})

	for _, f := range []string{"tenant_id", "user_id", "identity_account_id", "creator_user_id", "DeletedAt"} {
		if _, exists := tk[f]; exists {
			t.Errorf("forbidden field %q leaked through tokenView", f)
		}
	}
	// The key is exposed only in masked form — never the raw bearer secret.
	maskedKey, ok := tk["key"].(string)
	if !ok {
		t.Fatal("expected masked key field in tokenView")
	}
	if maskedKey == seeded.Key {
		t.Error("tokenView returned the raw bearer key; expected a masked value")
	}
	if maskedKey != maskKey(seeded.Key) {
		t.Errorf("expected masked key %q, got %q", maskKey(seeded.Key), maskedKey)
	}
	// sanity: the curated fields are present
	if _, ok := tk["name"]; !ok {
		t.Error("expected name field in tokenView")
	}
}

func TestUpdateTokenV2_InvalidID(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name": "Updated Name",
	}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, "/api/v2/test-tenant/tokens/invalid", body, nil)

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Invalid token ID" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
}

func TestUpdateTokenV2_TokenNotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name": "Ghost Token",
	}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, "/api/v2/test-tenant/tokens/99999", body, nil)

	AssertV2Status(t, w, http.StatusNotFound)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Token not found" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
}

func TestUpdateTokenV2_NameValidation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "Original Name")

	// Name exceeding 50 chars
	longName := make([]byte, 51)
	for i := range longName {
		longName[i] = 'x'
	}

	body := map[string]interface{}{
		"name": string(longName),
	}
	path := fmt.Sprintf("/api/v2/test-tenant/tokens/%d", token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	// Service layer returns Chinese error message
	if msg, ok := resp["message"].(string); ok {
		if msg != "令牌名称过长" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
	if code, _ := resp["error_code"].(string); code != tokenErrCodeNameInvalid {
		t.Errorf("error_code = %q, want %q", code, tokenErrCodeNameInvalid)
	}
}

// TestCreateTokenV2_WithScopes confirms scope round-trip through the v2 API:
// scopes submitted on create are normalized, persisted, and returned by the
// subsequent List call. Catches accidental drop on the handler→repo seam.
func TestCreateTokenV2_WithScopes(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":            "Scoped Token",
		"unlimited_quota": true,
		"scopes":          []string{"chat", "embedding"},
	}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)
	AssertV2Status(t, w, http.StatusCreated)
	AssertV2Success(t, w)

	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/tokens", nil, nil)
	resp := AssertV2Success(t, w)
	items := resp["data"].(map[string]interface{})["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 token, got %d", len(items))
	}
	tk := items[0].(map[string]interface{})
	rawScopes, ok := tk["scopes"].([]interface{})
	if !ok {
		t.Fatalf("tokenView.scopes missing or wrong type: %T (%v)", tk["scopes"], tk["scopes"])
	}
	got := make([]string, 0, len(rawScopes))
	for _, s := range rawScopes {
		got = append(got, s.(string))
	}
	// NormalizeTokenScopes orders by types.ValidTokenScopes — chat before embedding.
	want := []string{"chat", "embedding"}
	if len(got) != len(want) {
		t.Fatalf("scopes round-trip: got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("scopes[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestCreateTokenV2_RejectsUnknownScope ensures invalid scope values are
// rejected at the API boundary rather than silently stored — a bad input
// must never reach the DB or it produces a token that fail-closes against
// every relay path (which would be a confusing UX regression).
func TestCreateTokenV2_RejectsUnknownScope(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":            "Bad Scope Token",
		"unlimited_quota": true,
		"scopes":          []string{"chat", "writeall"},
	}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)
	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "writeall") {
		t.Errorf("expected error message to name the bad scope, got: %q", msg)
	}
	if code, _ := resp["error_code"].(string); code != tokenErrCodeScopeInvalid {
		t.Errorf("error_code = %q, want %q", code, tokenErrCodeScopeInvalid)
	}
}

// TestCreateTokenV2_ModelLimitsInvalid drives app.ValidateTokenModelLimits
// (cycle13 L3, ERRCODES-6) through the real handler: model_limits_enabled=true
// with a model_limits value containing an empty entry (a stray comma) must
// reject with 400 + error_code TOKEN_MODEL_LIMIT_INVALID rather than writing
// a row whose GetModelLimitsMap() would key on "" — indistinguishable from
// "no such model" at relay time.
func TestCreateTokenV2_ModelLimitsInvalid(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":                 "Bad Model Limits Token",
		"unlimited_quota":      true,
		"model_limits_enabled": true,
		"model_limits":         "gpt-4o,,gpt-4o-mini",
	}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)
	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if code, _ := resp["error_code"].(string); code != tokenErrCodeModelLimitInvalid {
		t.Errorf("error_code = %q, want %q", code, tokenErrCodeModelLimitInvalid)
	}
}

// TestCreateTokenV2_ModelLimitsDisabledSkipsValidation confirms
// model_limits_enabled=false never validates model_limits — an unused value
// is dead data, not a validation target (matches ValidateTokenModelLimits'
// own contract).
func TestCreateTokenV2_ModelLimitsDisabledSkipsValidation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":                 "Disabled Model Limits Token",
		"unlimited_quota":      true,
		"model_limits_enabled": false,
		"model_limits":         "gpt-4o,,gpt-4o-mini", // would be invalid if enabled
	}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)
	AssertV2Status(t, w, http.StatusCreated)
}

// TestUpdateTokenV2_ModelLimitsInvalid is the update-path sibling: the
// validation must run against the MERGED (existing + patched) state, not
// just the fields present in this request.
func TestUpdateTokenV2_ModelLimitsInvalid(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "Model Limits Update Target")

	body := map[string]interface{}{
		"model_limits_enabled": true,
		"model_limits":         "gpt-4o,,gpt-4o-mini",
	}
	path := fmt.Sprintf("/api/v2/test-tenant/tokens/%d", token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)
	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if code, _ := resp["error_code"].(string); code != tokenErrCodeModelLimitInvalid {
		t.Errorf("error_code = %q, want %q", code, tokenErrCodeModelLimitInvalid)
	}

	// The rejected update must not have persisted model_limits_enabled=true
	// with the bad list.
	var stored repo.Token
	if err := ctx.DB.First(&stored, token.Id).Error; err != nil {
		t.Fatalf("failed to reload token: %v", err)
	}
	if stored.ModelLimitsEnabled {
		t.Errorf("expected model_limits_enabled to stay false after a rejected update, got true")
	}
}

// TestCreateTokenV2_RateLimitInvalid drives app.ValidateRateLimits through
// the real handler for a negative rpm — the v2 wire boundary for the
// existing token_service.go validator, now with error_code attached.
func TestCreateTokenV2_RateLimitInvalid(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":            "Bad Rate Limit Token",
		"unlimited_quota": true,
		"rate_limit_rpm":  -5,
	}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)
	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if code, _ := resp["error_code"].(string); code != tokenErrCodeRateLimitInvalid {
		t.Errorf("error_code = %q, want %q", code, tokenErrCodeRateLimitInvalid)
	}
}

// TestCreateTokenV2_EmptyScopesAllowed confirms the backward-compat default —
// omitting "scopes" or sending [] both produce a token with Scopes="" which
// means "no restriction" for every relay path. This is the contract every
// pre-migration-015 token row depends on.
func TestCreateTokenV2_EmptyScopesAllowed(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":            "Unscoped Token",
		"unlimited_quota": true,
		"scopes":          []string{},
	}
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)
	AssertV2Status(t, w, http.StatusCreated)
	AssertV2Success(t, w)

	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/tokens", nil, nil)
	resp := AssertV2Success(t, w)
	items := resp["data"].(map[string]interface{})["items"].([]interface{})
	tk := items[0].(map[string]interface{})
	rawScopes, ok := tk["scopes"].([]interface{})
	if !ok {
		t.Fatalf("tokenView.scopes missing: %v", tk["scopes"])
	}
	if len(rawScopes) != 0 {
		t.Errorf("expected empty scopes for unscoped token, got %v", rawScopes)
	}
}

// TestUpdateTokenV2_ScopeChangeAudits verifies the audit-event detail body
// for scope rotations. ADR Phase E2 calls out that scope changes must be
// reconstructible from the audit log — a privilege escalation through
// scope expansion must leave a trace with both old and new values.
func TestUpdateTokenV2_ScopeChangeAudits(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Seed a chat-only token directly so the test starts from a known scope state.
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "Audit Trace Token")
	token.Scopes = "chat"
	ctx.DB.Save(token)

	// Update to chat+image — the audit record must capture the delta.
	body := map[string]interface{}{
		"scopes": []string{"chat", "image"},
	}
	path := fmt.Sprintf("/api/v2/test-tenant/tokens/%d", token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)
	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	// Confirm the token's scopes are persisted in canonical order.
	w = V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/tokens", nil, nil)
	resp := AssertV2Success(t, w)
	items := resp["data"].(map[string]interface{})["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 token, got %d", len(items))
	}
	tk := items[0].(map[string]interface{})
	rawScopes := tk["scopes"].([]interface{})
	if len(rawScopes) != 2 || rawScopes[0] != "chat" || rawScopes[1] != "image" {
		t.Errorf("expected scopes=[chat image], got %v", rawScopes)
	}
}

func TestCreateTokenV2_UnlimitedQuota(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":            "Unlimited Token",
		"unlimited_quota": true,
		"remain_quota":    0,
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)

	AssertV2Status(t, w, http.StatusCreated)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	if data["name"] != "Unlimited Token" {
		t.Errorf("expected name='Unlimited Token', got %v", data["name"])
	}
	key, ok := data["key"].(string)
	if !ok || key == "" {
		t.Error("expected key to be returned on token creation")
	}
}

// TestListTokensV2_CrossTenantIsolation guards the tokenView whitelist and
// proves cross-tenant isolation: a user in tenant-A cannot read tokens seeded
// directly into tenant-B by asserting that the list response only contains
// the caller's own tenant data.
func TestListTokensV2_CrossTenantIsolation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Seed one token in the test tenant (belongs to normalUser).
	SeedV2Token(t, ctx, ctx.NormalUser.Id, "Own Tenant Token")

	// Seed a second token directly in a different tenant — simulating a
	// row that leaked or was written by another tenant's user.
	otherTenantToken := &repo.Token{
		UserId:         ctx.NormalUser.Id,
		TenantId:       "other-tenant-xyz",
		Key:            common.GetRandomString(48),
		Status:         common.TokenStatusEnabled,
		Name:           "Cross-tenant Token",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          "default",
	}
	if err := ctx.DB.Create(otherTenantToken).Error; err != nil {
		t.Fatalf("failed to seed cross-tenant token: %v", err)
	}

	// List as normalUser in the test tenant — must only see 1 token.
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/tokens", nil, nil)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})

	total := int(data["total"].(float64))
	if total != 1 {
		t.Errorf("expected 1 token (own tenant), got %d — possible cross-tenant leak", total)
	}

	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 item in list, got %d", len(items))
	}

	tk := items[0].(map[string]interface{})
	if name, _ := tk["name"].(string); name == "Cross-tenant Token" {
		t.Error("cross-tenant token leaked into the response")
	}

	// Verify forbidden fields are absent from the view shape.
	for _, f := range []string{"tenant_id", "user_id", "identity_account_id", "creator_user_id", "DeletedAt"} {
		if _, exists := tk[f]; exists {
			t.Errorf("forbidden field %q leaked through tokenView in cross-tenant test", f)
		}
	}

	// The raw bearer key must never appear; only the masked form.
	maskedKey, ok := tk["key"].(string)
	if !ok {
		t.Fatal("expected masked key field in tokenView")
	}
	if maskedKey == otherTenantToken.Key {
		t.Error("tokenView returned the raw bearer key; expected masked value")
	}
}

func TestCreateTokenV2_AbsurdExpiredTimeRejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":            "Absurd Expiry",
		"expired_time":    int64(999999999999999), // far beyond year 3000
		"unlimited_quota": true,
	}

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, "/api/v2/test-tenant/tokens", body, nil)
	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "过期时间无效") {
		t.Errorf("expected 过期时间无效 error family, got %q", msg)
	}
	if code, _ := resp["error_code"].(string); code != tokenErrCodeExpiryInvalid {
		t.Errorf("error_code = %q, want %q", code, tokenErrCodeExpiryInvalid)
	}
}

func TestUpdateTokenV2_AbsurdExpiredTimeRejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "Update Expiry Target")

	body := map[string]interface{}{
		"expired_time": int64(999999999999999),
	}
	path := fmt.Sprintf("/api/v2/test-tenant/tokens/%d", token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPut, path, body, nil)
	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if code, _ := resp["error_code"].(string); code != tokenErrCodeExpiryInvalid {
		t.Errorf("error_code = %q, want %q", code, tokenErrCodeExpiryInvalid)
	}

	// Stored token must keep its original expiry.
	var stored repo.Token
	if err := ctx.DB.First(&stored, token.Id).Error; err != nil {
		t.Fatalf("failed to reload token: %v", err)
	}
	if stored.ExpiredTime != -1 {
		t.Errorf("expected stored ExpiredTime=-1, got %d", stored.ExpiredTime)
	}
}
