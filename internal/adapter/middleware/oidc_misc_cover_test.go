package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// -------------------- RequireRole / RequireAnyRole --------------------

func mountRequireRole(mw gin.HandlerFunc, roles []string) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		if roles != nil {
			c.Set("tenant_context", &TenantContext{TenantID: "t1", Roles: roles})
		}
		c.Next()
	})
	r.GET("/x", mw, func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

func serve(r *gin.Engine) int {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	return w.Code
}

func TestRequireRole_Branches(t *testing.T) {
	if code := serve(mountRequireRole(RequireRole("admin"), nil)); code != http.StatusUnauthorized {
		t.Errorf("no tenant ctx = %d, want 401", code)
	}
	if code := serve(mountRequireRole(RequireRole("admin"), []string{"admin", "user"})); code != http.StatusOK {
		t.Errorf("role present = %d, want 200", code)
	}
	if code := serve(mountRequireRole(RequireRole("admin"), []string{"user"})); code != http.StatusForbidden {
		t.Errorf("role absent = %d, want 403", code)
	}
}

func TestRequireAnyRole_Branches(t *testing.T) {
	if code := serve(mountRequireRole(RequireAnyRole("admin", "root"), nil)); code != http.StatusUnauthorized {
		t.Errorf("no tenant ctx = %d, want 401", code)
	}
	if code := serve(mountRequireRole(RequireAnyRole("admin", "root"), []string{"root"})); code != http.StatusOK {
		t.Errorf("one matching role = %d, want 200", code)
	}
	if code := serve(mountRequireRole(RequireAnyRole("admin", "root"), []string{"viewer"})); code != http.StatusForbidden {
		t.Errorf("no matching role = %d, want 403", code)
	}
}

// -------------------- extractRoles shapes --------------------

func TestExtractRoles_Shapes(t *testing.T) {
	// key-only (project-roles style)
	got := extractRoles(map[string]interface{}{"admin": map[string]interface{}{"org1": "domain"}})
	if len(got) != 1 || got[0] != "admin" {
		t.Errorf("key-only shape = %v, want [admin]", got)
	}
	// string value
	got = extractRoles(map[string]interface{}{"k": "editor"})
	if !containsStr(got, "k") || !containsStr(got, "editor") {
		t.Errorf("string-value shape = %v, want k+editor", got)
	}
	// []interface{} value
	got = extractRoles(map[string]interface{}{"grp": []interface{}{"r1", "r2"}})
	if !containsStr(got, "r1") || !containsStr(got, "r2") {
		t.Errorf("slice-value shape = %v, want r1+r2", got)
	}
	if extractRoles(nil) != nil {
		t.Errorf("nil roles map must yield nil slice")
	}
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// -------------------- applyConfigurableClaims --------------------

func TestApplyConfigurableClaims_NeutralNoOp(t *testing.T) {
	// With neutral default keys the function returns before re-parsing —
	// claims stay whatever they were.
	var claims OIDCClaims
	applyConfigurableClaims("irrelevant.token.here", &claims)
	if claims.OrgID != "" {
		t.Errorf("neutral keys must not mutate claims; OrgID=%q", claims.OrgID)
	}
}

func TestApplyConfigurableClaims_CustomKey(t *testing.T) {
	prev := oidcClaimKeys
	defer func() { oidcClaimKeys = prev }()
	oidcClaimKeys = claimKeySet{
		OrgID: "custom_org", OrgDomain: "org_domain",
		ResourceOwnerID: "resource_owner_id", ResourceOwnerName: "resource_owner_name", Roles: "roles",
	}
	// Build a token carrying the custom claim (signature irrelevant — the
	// function re-parses unverified).
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"custom_org": "org-777"})
	signed, err := tok.SignedString([]byte("k"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	var claims OIDCClaims
	applyConfigurableClaims(signed, &claims)
	if claims.OrgID != "org-777" {
		t.Errorf("OrgID = %q, want org-777 (from custom claim key)", claims.OrgID)
	}
}

// -------------------- envOr --------------------

func TestEnvOr(t *testing.T) {
	const key = "COVER_ENVOR_TEST"
	_ = os.Unsetenv(key)
	if got := envOr(key, "fallback"); got != "fallback" {
		t.Errorf("unset env = %q, want fallback", got)
	}
	_ = os.Setenv(key, "actual")
	defer func() { _ = os.Unsetenv(key) }()
	if got := envOr(key, "fallback"); got != "actual" {
		t.Errorf("set env = %q, want actual", got)
	}
}

// -------------------- InitOIDCAuth disabled --------------------

func TestInitOIDCAuth_Disabled(t *testing.T) {
	prevEnabled := oidcEnabled
	prevEnv := os.Getenv("OIDC_ENABLED")
	_ = os.Unsetenv("OIDC_ENABLED")
	defer func() {
		oidcEnabled = prevEnabled
		if prevEnv != "" {
			_ = os.Setenv("OIDC_ENABLED", prevEnv)
		}
	}()
	if err := InitOIDCAuth(); err != nil {
		t.Errorf("InitOIDCAuth (disabled) returned error: %v", err)
	}
	if oidcEnabled {
		t.Errorf("oidcEnabled must be false when OIDC_ENABLED unset")
	}
}

// -------------------- JWKS manager lifecycle --------------------

func TestNewJWKSManagerWithContext_FetchAndStop(t *testing.T) {
	_, pub := generateTestRSAKeyPair(t)
	jwks := JWKSet{Keys: []JWK{rsaPublicKeyToJWK(pub, "mgr-kid")}}
	srv := createTestJWKSServer(t, jwks)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	mgr := NewJWKSManagerWithContext(ctx, srv.URL)
	if mgr == nil {
		t.Fatalf("NewJWKSManagerWithContext returned nil")
	}
	// Initial refresh should have populated the key.
	if key, err := mgr.getKeyWithRefresh("mgr-kid"); err != nil || key == nil {
		t.Errorf("getKeyWithRefresh(mgr-kid) err=%v key=%v, want the fetched key", err, key)
	}
	// Cancel drives the background autoRefreshWithContext loop to return.
	cancel()
	time.Sleep(20 * time.Millisecond)
}

// -------------------- mapOIDCUserToLurus --------------------

func seedTenantWithMapping(t *testing.T, orgID, tenantID, subject string, status int) int {
	t.Helper()
	tn := &entity.Tenant{Id: tenantID, Slug: tenantID, Name: tenantID, Status: status, IDPOrgID: orgID, PlanType: entity.TenantPlanFree, MaxUsers: 10, MaxQuota: 1000, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := repo.DB.Create(tn).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	user := &repo.User{Username: "oidc-" + subject, Role: 1, Status: 1, Email: subject + "@local", TenantId: tenantID}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	mapping := &entity.UserIdentityMapping{LurusUserID: user.Id, IDPSubject: subject, TenantID: tenantID, Email: user.Email, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := repo.DB.Create(mapping).Error; err != nil {
		t.Fatalf("create mapping: %v", err)
	}
	return user.Id
}

func TestMapOIDCUserToLurus_HappyPath(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	uid := seedTenantWithMapping(t, "org-happy", "tenant-happy", "sub-happy", entity.TenantStatusEnabled)

	claims := &OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "sub-happy"},
		OrgID:            "org-happy",
		Email:            "sub-happy@local",
	}
	tid, gotUID, err := mapOIDCUserToLurus(claims)
	if err != nil {
		t.Fatalf("mapOIDCUserToLurus: %v", err)
	}
	if tid != "tenant-happy" || gotUID != uid {
		t.Errorf("got (%s,%d), want (tenant-happy,%d)", tid, gotUID, uid)
	}
}

func TestMapOIDCUserToLurus_TenantNotFound(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	claims := &OIDCClaims{OrgID: "no-such-org"}
	if _, _, err := mapOIDCUserToLurus(claims); err == nil {
		t.Errorf("expected error for unknown org (auto-create disabled)")
	}
}

func TestMapOIDCUserToLurus_TenantDisabled(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	seedTenantWithMapping(t, "org-dis", "tenant-dis", "sub-dis", entity.TenantStatusSuspended)
	claims := &OIDCClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: "sub-dis"}, OrgID: "org-dis"}
	if _, _, err := mapOIDCUserToLurus(claims); err == nil {
		t.Errorf("expected error for suspended tenant")
	}
}

func TestMapOIDCUserToLurus_MappingNotFound(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	// Tenant exists and is enabled, but no mapping for this subject.
	tn := &entity.Tenant{Id: "tenant-nomap", Slug: "tenant-nomap", Name: "n", Status: entity.TenantStatusEnabled, IDPOrgID: "org-nomap", PlanType: entity.TenantPlanFree, MaxUsers: 10, MaxQuota: 1000, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := repo.DB.Create(tn).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	claims := &OIDCClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: "ghost-sub"}, OrgID: "org-nomap"}
	if _, _, err := mapOIDCUserToLurus(claims); err == nil {
		t.Errorf("expected error for missing user mapping (auto-create disabled)")
	}
}

// SECURITY (cross-tenant isolation): a blank org_id must never resolve to a
// tenant. Even with auto-create enabled, mapping must fail and must NOT create
// a tenant keyed on an empty zitadel_org_id — otherwise every org_id-less user
// from any unrelated IdP would be folded into one shared tenant.
func TestMapOIDCUserToLurus_EmptyOrgID_Rejected(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	_ = os.Setenv("OIDC_AUTO_CREATE_TENANT", "true")
	_ = os.Setenv("OIDC_AUTO_CREATE_USER", "true")
	defer func() {
		_ = os.Unsetenv("OIDC_AUTO_CREATE_TENANT")
		_ = os.Unsetenv("OIDC_AUTO_CREATE_USER")
	}()

	for _, org := range []string{"", "   "} {
		claims := &OIDCClaims{
			RegisteredClaims: jwt.RegisteredClaims{Subject: "blank-org-sub"},
			OrgID:            org,
			Email:            "blank@nowhere.test",
		}
		if _, _, err := mapOIDCUserToLurus(claims); err == nil {
			t.Errorf("OrgID=%q: expected mapping error, got nil", org)
		}
	}

	// No tenant may have been created for a blank org_id.
	var count int64
	if err := repo.DB.Model(&entity.Tenant{}).
		Where("zitadel_org_id = ? OR zitadel_org_id = ?", "", "   ").
		Count(&count).Error; err != nil {
		t.Fatalf("count tenants: %v", err)
	}
	if count != 0 {
		t.Errorf("blank org_id created %d tenant(s), want 0", count)
	}
}

// Regression guard: a non-empty org_id still maps successfully — the empty-org
// rejection must not over-tighten and lock out legitimate tenants.
func TestMapOIDCUserToLurus_NonEmptyOrgID_StillMaps(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	uid := seedTenantWithMapping(t, "org-ok", "tenant-ok", "sub-ok", entity.TenantStatusEnabled)

	claims := &OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "sub-ok"},
		OrgID:            "org-ok",
		Email:            "sub-ok@local",
	}
	tid, gotUID, err := mapOIDCUserToLurus(claims)
	if err != nil {
		t.Fatalf("non-empty org must still map: %v", err)
	}
	if tid != "tenant-ok" || gotUID != uid {
		t.Errorf("got (%s,%d), want (tenant-ok,%d)", tid, gotUID, uid)
	}
}

// -------------------- misc: abortWithMidjourneyMessage --------------------

func TestAbortWithMidjourneyMessage(t *testing.T) {
	c, w := newTestContext(http.MethodPost, "/mj/submit/imagine", "", "")
	abortWithMidjourneyMessage(c, http.StatusBadRequest, 4, "bad mj request")
	if !c.IsAborted() {
		t.Errorf("context must be aborted")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
