package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// mintIdentitySessionToken builds a valid HS256 lurus-platform session token
// (header.payload.sig) that common.ValidateIdentitySessionToken accepts.
func mintIdentitySessionToken(secret string, accountID int64) string {
	enc := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	header := enc([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadJSON, _ := json.Marshal(map[string]any{
		"iss": "lurus-platform",
		"sub": fmt.Sprintf("lurus:%d", accountID),
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	payload := enc(payloadJSON)
	body := header + "." + payload
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return body + "." + enc(mac.Sum(nil))
}

// mountSessionAuth wires a cookie-session engine that pre-seeds the session with
// preset values, then runs UserAuth. This drives authHelper's session-branch
// type-assertion guards without any token/DB dependency.
func mountSessionAuth(preset map[string]any) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("gap-r3-secret"))
	r.Use(sessions.Sessions("session", store))
	r.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		for k, v := range preset {
			s.Set(k, v)
		}
		_ = s.Save()
		c.Next()
	})
	r.GET("/p", UserAuth(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

// authHelper: session carries a non-string username (but valid int status/role)
// → the username type-assertion guard returns 401 (:197).
func TestAuthHelper_SessionUsernameWrongType_401(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	r := mountSessionAuth(map[string]any{
		"username": 12345, // int, not string
		"role":     common.RoleCommonUser,
		"status":   common.UserStatusEnabled,
		"id":       1,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/p", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for non-string session username; body=%s", w.Code, w.Body.String())
	}
}

// authHelper: valid username/role/status but a non-int session id → the user-ID
// type-assertion guard returns 401 (:225).
func TestAuthHelper_SessionIDWrongType_401(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	r := mountSessionAuth(map[string]any{
		"username": "sessuser",
		"role":     common.RoleCommonUser,
		"status":   common.UserStatusEnabled,
		"id":       "not-an-int", // string, not int
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/p", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for non-int session id; body=%s", w.Code, w.Body.String())
	}
}

// authHelper: an access token that resolves to a user with an INVALID role must
// be rejected (:112) — access-token path fails closed on malformed user info.
func TestAuthHelper_AccessToken_InvalidRole_Rejected(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	accTok := "acc-tok-invalidrole"
	user := &repo.User{
		Username: "accuser", Role: 999, // invalid role
		Status: common.UserStatusEnabled, Email: "acc@local", TenantId: "default",
		AccessToken: &accTok,
	}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Empty session → falls through to the access-token branch.
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("gap-r3-secret"))
	r.Use(sessions.Sessions("session", store))
	r.GET("/p", UserAuth(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	req.Header.Set("Authorization", accTok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// validUserInfo(role=999) is false → JSON success:false (HTTP 200 body-level reject).
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body-level reject; body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Success bool `json:"success"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Success {
		t.Errorf("success=true, want false for access token resolving to invalid-role user")
	}
}

// authHelper: a valid lurus-platform identity session token (HS256) resolves via
// the platform overview + local IDP-subject mapping to a local user — the SDK
// bridge auth-parity path (:91). No cross-tenant bleed: the mapped user's own
// identity is admitted.
func TestAuthHelper_IdentitySessionToken_ResolvesUser(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	const acct = int64(70011)
	const idpSub = "idp-subject-70011"

	prevSecret := common.IdentitySessionSecret
	common.IdentitySessionSecret = "session-secret-gap-r3"
	defer func() { common.IdentitySessionSecret = prevSecret }()

	// Platform overview endpoint returns the account's IDP subject.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"account":{"id":%d,"idp_subject":%q,"email":"bridge@local"}}`, acct, idpSub)
	}))
	defer srv.Close()
	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = srv.URL
	defer func() { common.IdentityServiceURL = prevURL }()

	// Seed the local user + IDP-subject mapping the bridge resolves to.
	user := &repo.User{
		Username: "bridgeuser", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "bridge@local", TenantId: "default", Quota: 1000,
	}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	mapping := &entity.UserIdentityMapping{
		LurusUserID: user.Id, IDPSubject: idpSub, TenantID: "default",
		Email: "bridge@local", IsActive: true,
	}
	if err := repo.DB.Create(mapping).Error; err != nil {
		t.Fatalf("create mapping: %v", err)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("gap-r3-secret"))
	r.Use(sessions.Sessions("session", store))
	r.GET("/p", UserAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"id": c.GetInt("id")})
	})
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	req.Header.Set("Authorization", "Bearer "+mintIdentitySessionToken(common.IdentitySessionSecret, acct))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for identity-session-token bridge; body=%s", w.Code, w.Body.String())
	}
	// The admitted identity must be the mapped local user (no cross-tenant bleed).
	var got struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.ID != user.Id {
		t.Errorf("resolved id = %d, want %d (mapped local user)", got.ID, user.Id)
	}
}

// authHelper: the lurus-platform session-bearer path must resolve a user
// provisioned into ANY tenant, not just "default". Before the fix, the
// mapping lookup was pinned to tenant "default" — a bridge login for a user
// provisioned in some other tenant could never find its mapping and always
// fell through to a rejection, even though the token itself was valid.
// Twin of TestAuthHelper_IdentitySessionToken_ResolvesUser with tenant
// "tenantX".
func TestAuthHelper_IdentitySessionToken_ResolvesNonDefaultTenantUser(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	const acct = int64(70012)
	const idpSub = "idp-subject-70012-tenantx"

	prevSecret := common.IdentitySessionSecret
	common.IdentitySessionSecret = "session-secret-gap-r3-tenantx"
	defer func() { common.IdentitySessionSecret = prevSecret }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"account":{"id":%d,"idp_subject":%q,"email":"bridgex@local"}}`, acct, idpSub)
	}))
	defer srv.Close()
	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = srv.URL
	defer func() { common.IdentityServiceURL = prevURL }()

	tenant := &entity.Tenant{
		Id: "tenantX", Slug: "tenantX", Name: "Tenant X",
		Status: entity.TenantStatusEnabled, PlanType: entity.TenantPlanFree,
		MaxUsers: 10, MaxQuota: 1000,
	}
	if err := repo.DB.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	// Seed the local user + IDP-subject mapping the bridge resolves to, both
	// in the non-default tenant.
	user := &repo.User{
		Username: "bridgeuserx", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "bridgex@local", TenantId: "tenantX", Quota: 1000,
	}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	mapping := &entity.UserIdentityMapping{
		LurusUserID: user.Id, IDPSubject: idpSub, TenantID: "tenantX",
		Email: "bridgex@local", IsActive: true,
	}
	if err := repo.DB.Create(mapping).Error; err != nil {
		t.Fatalf("create mapping: %v", err)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("gap-r3-secret"))
	r.Use(sessions.Sessions("session", store))
	handler := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"id": c.GetInt("id"), "tenant_id": c.GetString("tenant_id")})
	}
	r.GET("/p", UserAuth(), handler)
	r.HEAD("/p", UserAuth(), handler)

	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	req.Header.Set("Authorization", "Bearer "+mintIdentitySessionToken(common.IdentitySessionSecret, acct))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for non-default-tenant bridge login; body=%s", w.Code, w.Body.String())
	}
	var got struct {
		ID       int    `json:"id"`
		TenantID string `json:"tenant_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.ID != user.Id {
		t.Errorf("resolved id = %d, want %d (the tenantX user)", got.ID, user.Id)
	}
	if got.TenantID != "tenantX" {
		t.Errorf("tenant_id = %q, want %q", got.TenantID, "tenantX")
	}

	// Control: a HEAD probe with no bearer at all still fails the ordinary
	// "no access token" way — proves the 200 above came from resolving the
	// real identity, not from some blanket admit on this route.
	headReq := httptest.NewRequest(http.MethodHead, "/p", nil)
	headW := httptest.NewRecorder()
	r.ServeHTTP(headW, headReq)
	var headBody struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(headW.Body.Bytes(), &headBody)
	if headBody.Success {
		t.Errorf("HEAD without a bearer succeeded; want success:false")
	}
	if !strings.Contains(headBody.Message, "access token") {
		t.Errorf("HEAD failure message = %q, want it to mention access token", headBody.Message)
	}
}

// TokenAuth: a valid token whose user row has vanished → GetUserCache errors →
// 500 (:413). Fails closed rather than serving an orphaned token.
func TestTokenAuth_UserCacheError_500(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()
	_, key, _ := seedUserToken(t)

	// Drop users so GetUserCache's DB lookup errors after token validation.
	if err := db.Migrator().DropTable(&repo.User{}); err != nil {
		t.Fatalf("drop users: %v", err)
	}

	r := mountTokenAuth("/v1/chat/completions")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-"+key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 when user cache lookup fails; body=%s", w.Code, w.Body.String())
	}
}

// PlaygroundAuth: session user has no tokens AND auto-create fails (tokens table
// gone) → 500 (:522).
func TestPlaygroundAuth_AutoCreateTokenError_500(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	user := &repo.User{
		Username: "pguser", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "pg@local", TenantId: "default",
	}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	// Drop tokens so both the lookup and the auto-create fail.
	if err := db.Migrator().DropTable(&repo.Token{}); err != nil {
		t.Fatalf("drop tokens: %v", err)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("gap-r3-secret"))
	r.Use(sessions.Sessions("session", store))
	r.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("id", user.Id)
		_ = s.Save()
		c.Next()
	})
	r.POST("/pg", PlaygroundAuth(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	req := httptest.NewRequest(http.MethodPost, "/pg", nil) // no Authorization → session path
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 when default-token auto-create fails; body=%s", w.Code, w.Body.String())
	}
}
