package middleware

import (
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// TokenAuth: key delivered via the midjourney mj-api-secret header.
func TestTokenAuth_MJSecretHeader(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	_, key, _ := seedUserToken(t)
	r := mountTokenAuth("/mj/submit/imagine")
	req := httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", nil)
	req.Header.Set("mj-api-secret", "sk-"+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for mj-api-secret auth; body=%s", w.Code, w.Body.String())
	}
}

// TokenAuth: an exhausted token is rejected with 402 (a per-TOKEN spending-cap
// rejection, D4/B2) carrying token-management guidance in its metadata — NOT
// a wallet topup_url, since a wallet top-up cannot raise a token's own
// remain_quota (see token_service.go's remedy: edit the token or set it
// unlimited). The response also carries "(request id: ...)" like every other
// TokenAuth rejection (B5).
func TestTokenAuth_ExhaustedToken_402(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	user := &repo.User{Username: "exh", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "exh@local", TenantId: "default"}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	key := common.GetRandomString(48)
	tok := &repo.Token{UserId: user.Id, TenantId: "default", Key: key, Status: common.TokenStatusExhausted, Name: "exh", ExpiredTime: -1, CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp()}
	if err := repo.DB.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	r := mountTokenAuth("/v1/chat/completions")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-"+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402 for exhausted token; body=%s", w.Code, w.Body.String())
	}

	var body struct {
		Error struct {
			Message  string          `json:"message"`
			Code     string          `json:"code"`
			Metadata json.RawMessage `json:"metadata"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v; body=%s", err, w.Body.String())
	}
	if body.Error.Code != "token_quota_exhausted" {
		t.Errorf("error.code = %q, want %q; body=%s", body.Error.Code, "token_quota_exhausted", w.Body.String())
	}
	if !strings.Contains(body.Error.Message, "(request id:") {
		t.Errorf("error.message = %q, want it to contain %q", body.Error.Message, "(request id:")
	}

	var meta struct {
		Reason                string  `json:"reason"`
		TokenRemainQuotaUnits *int    `json:"token_remain_quota_units"`
		TopupURL              *string `json:"topup_url"`
	}
	if err := json.Unmarshal(body.Error.Metadata, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v; metadata=%s", err, body.Error.Metadata)
	}
	if meta.Reason != "token_quota_exhausted" {
		t.Errorf("metadata.reason = %q, want %q", meta.Reason, "token_quota_exhausted")
	}
	if meta.TokenRemainQuotaUnits == nil {
		t.Errorf("metadata.token_remain_quota_units missing; metadata=%s", body.Error.Metadata)
	}
	if meta.TopupURL != nil {
		t.Errorf("metadata must NOT carry topup_url for a per-token cap 402, got: %s", body.Error.Metadata)
	}
}

// flexAuthViaToken: a token whose user row is missing surfaces a 500.
func TestFlexAuth_TokenOrphanUser_500(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	key := common.GetRandomString(48)
	// Token references a non-existent user id.
	tok := &repo.Token{UserId: 987654, TenantId: "default", Key: key, Status: common.TokenStatusEnabled, Name: "orphan", ExpiredTime: -1, UnlimitedQuota: true, CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp()}
	if err := repo.DB.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	r := mountFlex()
	req := httptest.NewRequest(http.MethodGet, "/flex", nil)
	req.Header.Set("Authorization", "Bearer sk-"+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 when token's user is missing; body=%s", w.Code, w.Body.String())
	}
}

// GetTenantContext error branches.
func TestGetTenantContext_Errors(t *testing.T) {
	c1, _ := newTestContext(http.MethodGet, "/x", "", "")
	if _, err := GetTenantContext(c1); err == nil {
		t.Errorf("expected error when tenant_context absent")
	}
	c2, _ := newTestContext(http.MethodGet, "/x", "", "")
	c2.Set("tenant_context", "wrong-type")
	if _, err := GetTenantContext(c2); err == nil {
		t.Errorf("expected error when tenant_context has wrong type")
	}
}

// getKeyWithRefresh: a cache miss triggers a JWKS refresh; a still-missing kid
// then returns an error.
func TestGetKeyWithRefresh_MissTriggersRefresh(t *testing.T) {
	_, pub := generateTestRSAKeyPair(t)
	jwks := JWKSet{Keys: []JWK{rsaPublicKeyToJWK(pub, "known-kid")}}
	srv := createTestJWKSServer(t, jwks)
	defer srv.Close()

	mgr := &JWKSManager{
		jwksURI:            srv.URL,
		publicKeys:         make(map[string]*rsa.PublicKey),
		minRefreshInterval: 0, // allow immediate refresh on miss
	}
	if err := mgr.refreshKeys(); err != nil {
		t.Fatalf("refreshKeys: %v", err)
	}
	if key, err := mgr.getKeyWithRefresh("known-kid"); err != nil || key == nil {
		t.Errorf("known kid err=%v key=%v, want the key", err, key)
	}
	if _, err := mgr.getKeyWithRefresh("absent-kid"); err == nil {
		t.Errorf("absent kid must return an error after refresh")
	}
}

// OIDCAuth session fallback: a session user id that maps to no row → the
// fallback declines and OIDCAuth returns 401.
func TestOIDCAuth_SessionFallback_MissingUser_401(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()
	r := sessionOIDCRouter(map[string]interface{}{
		"id": 555555, // no such user in the integration DB
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/p", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 when session user id resolves to no row", w.Code)
	}
	_ = gin.Mode()
}
