package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

// ============================================================================
// OIDCAuth Integration Tests
// Tests for the full authentication flow including JWT validation and user mapping
// ============================================================================

type integrationTestContext struct {
	Router     *gin.Engine
	DB         *gorm.DB
	Cleanup    func()
	PrivateKey *rsa.PrivateKey
	PublicKey  *rsa.PublicKey
	JWKSURL    string
	Issuer     string
	ClientID   string
	Tenant     *repo.Tenant
	User       *repo.User
	Mapping    *repo.UserIdentityMapping
}

// setupIntegrationTest sets up the integration test environment
func setupIntegrationTest(t *testing.T) *integrationTestContext {
	t.Helper()
	gin.SetMode(gin.TestMode)

	// Generate RSA key pair for signing JWTs
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	publicKey := &privateKey.PublicKey

	// Create JWKS server
	jwk := rsaPublicKeyToJWK(publicKey, "test-integration-kid")
	jwksSet := JWKSet{Keys: []JWK{jwk}}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwksSet)
	}))

	// Setup in-memory database
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	// Migrate tables
	db.AutoMigrate(
		&repo.User{},
		&repo.Token{},
		&repo.Tenant{},
		&repo.UserIdentityMapping{},
		&repo.TenantConfig{},
	)

	// Save previous state
	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevRedis := common.RedisEnabled

	repo.DB = db
	repo.LOG_DB = db
	common.UsingSQLite = true
	common.RedisEnabled = false

	// Initialize OptionMap
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()

	// Create test tenant
	tenant := &repo.Tenant{
		Id:           "integration-test-tenant",
		Name:         "Integration Test Tenant",
		Slug:         "integration-test",
		Status:       repo.TenantStatusEnabled,
		IDPOrgID: "org_integration_test",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	db.Create(tenant)

	// Create test user
	user := &repo.User{
		Username:    "integration_user",
		DisplayName: "Integration User",
		Email:       "integration@test.local",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		TenantId:    tenant.Id,
		Quota:       1000000,
	}
	db.Create(user)

	// Create user mapping
	mapping := &repo.UserIdentityMapping{
		TenantID:      tenant.Id,
		IDPSubject: "zitadel_integration_user",
		LurusUserID:   user.Id,
		Email:         user.Email,
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	db.Create(mapping)

	// Configure environment
	issuer := "https://zitadel.integration.test"
	clientID := "integration-client-id"

	os.Setenv("OIDC_ENABLED", "true")
	os.Setenv("OIDC_ISSUER", issuer)
	os.Setenv("OIDC_JWKS_URI", jwksServer.URL)
	os.Setenv("OIDC_CLIENT_ID", clientID)
	os.Setenv("OIDC_AUTO_CREATE_TENANT", "false")
	os.Setenv("OIDC_AUTO_CREATE_USER", "false")

	// Initialize Zitadel auth
	oidcEnabled = true
	oidcIssuers = []string{issuer}
	oidcJwksURI = jwksServer.URL
	oidcClientID = clientID

	// Create JWKS manager
	jwksManager = &JWKSManager{
		jwksURI:            jwksServer.URL,
		publicKeys:         make(map[string]*rsa.PublicKey),
		minRefreshInterval: 0,
	}
	jwksManager.refreshKeys()

	// Setup router
	router := gin.New()
	router.GET("/protected", OIDCAuth(), func(c *gin.Context) {
		tenantCtx, _ := GetTenantContext(c)
		c.JSON(http.StatusOK, gin.H{
			"success":   true,
			"tenant_id": tenantCtx.TenantID,
			"user_id":   tenantCtx.UserID,
			"email":     tenantCtx.Email,
		})
	})

	cleanup := func() {
		jwksServer.Close()
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.RedisEnabled = prevRedis
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
		os.Unsetenv("OIDC_ENABLED")
		os.Unsetenv("OIDC_ISSUER")
		os.Unsetenv("OIDC_JWKS_URI")
		os.Unsetenv("OIDC_CLIENT_ID")
	}

	return &integrationTestContext{
		Router:     router,
		DB:         db,
		Cleanup:    cleanup,
		PrivateKey: privateKey,
		PublicKey:  publicKey,
		JWKSURL:    jwksServer.URL,
		Issuer:     issuer,
		ClientID:   clientID,
		Tenant:     tenant,
		User:       user,
		Mapping:    mapping,
	}
}

// createIntegrationJWT creates a signed JWT for testing
func (ctx *integrationTestContext) createJWT(t *testing.T, claims OIDCClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test-integration-kid"

	signed, err := token.SignedString(ctx.PrivateKey)
	if err != nil {
		t.Fatalf("failed to sign JWT: %v", err)
	}
	return signed
}

func TestOIDCAuth_ValidToken_UserMapping(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	// Create valid claims
	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    ctx.Issuer,
			Subject:   "zitadel_integration_user",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email:             "integration@test.local",
		EmailVerified:     true,
		Name:              "Integration User",
		PreferredUsername: "integration_user",
		OrgID:             "org_integration_test",
		OrgDomain:         "integration.test",
	}

	token := ctx.createJWT(t, claims)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	ctx.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d, body: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["success"] != true {
		t.Error("expected success=true")
	}
	if resp["tenant_id"] != ctx.Tenant.Id {
		t.Errorf("expected tenant_id=%s, got %v", ctx.Tenant.Id, resp["tenant_id"])
	}
	if resp["user_id"].(float64) != float64(ctx.User.Id) {
		t.Errorf("expected user_id=%d, got %v", ctx.User.Id, resp["user_id"])
	}
}

func TestOIDCAuth_DisabledTenant(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	// Disable the tenant
	ctx.Tenant.Status = repo.TenantStatusDisabled
	ctx.DB.Save(ctx.Tenant)

	// Create valid claims
	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    ctx.Issuer,
			Subject:   "zitadel_integration_user",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email: "integration@test.local",
		OrgID: "org_integration_test",
	}

	token := ctx.createJWT(t, claims)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	ctx.Router.ServeHTTP(w, req)

	// Should fail because tenant is disabled
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 for disabled tenant, got %d", w.Code)
	}
}

func TestOIDCAuth_MissingHeader(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	// No Authorization header
	w := httptest.NewRecorder()

	ctx.Router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for missing header, got %d", w.Code)
	}
}

func TestOIDCAuth_ExpiredToken(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	// Create expired claims
	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    ctx.Issuer,
			Subject:   "zitadel_integration_user",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)), // Expired
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
		Email: "integration@test.local",
		OrgID: "org_integration_test",
	}

	token := ctx.createJWT(t, claims)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	ctx.Router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for expired token, got %d", w.Code)
	}
}

func TestOIDCAuth_InvalidSignature(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	// Generate a different key pair
	differentKey, _ := rsa.GenerateKey(rand.Reader, 2048)

	// Create claims
	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    ctx.Issuer,
			Subject:   "zitadel_integration_user",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email: "integration@test.local",
		OrgID: "org_integration_test",
	}

	// Sign with different key
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test-integration-kid"
	signed, _ := token.SignedString(differentKey)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	w := httptest.NewRecorder()

	ctx.Router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for invalid signature, got %d", w.Code)
	}
}

func TestOIDCAuth_WrongIssuer(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	// Create claims with wrong issuer
	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://wrong-issuer.example.com",
			Subject:   "zitadel_integration_user",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email: "integration@test.local",
		OrgID: "org_integration_test",
	}

	token := ctx.createJWT(t, claims)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	ctx.Router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 for wrong issuer, got %d", w.Code)
	}
}

func TestOIDCAuth_InvalidBearerFormat(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	testCases := []struct {
		name   string
		header string
	}{
		{"no bearer prefix", "some-token"},
		{"basic auth", "Basic dXNlcjpwYXNz"},
		{"empty bearer", "Bearer "},
		{"just bearer", "Bearer"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.Header.Set("Authorization", tc.header)
			w := httptest.NewRecorder()

			ctx.Router.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("expected status 401 for %s, got %d", tc.name, w.Code)
			}
		})
	}
}

func TestOIDCAuth_MalformedToken(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	testCases := []struct {
		name  string
		token string
	}{
		{"random string", "not-a-valid-jwt-token"},
		{"two parts", "part1.part2"},
		{"four parts", "part1.part2.part3.part4"},
		{"invalid base64", "!!!.!!!.!!!"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			w := httptest.NewRecorder()

			ctx.Router.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("expected status 401 for %s, got %d", tc.name, w.Code)
			}
		})
	}
}

func TestOIDCAuth_UserMappingNotFound(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	// Create claims for a user that doesn't have a mapping
	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    ctx.Issuer,
			Subject:   "unknown_zitadel_user", // No mapping for this user
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email: "unknown@test.local",
		OrgID: "org_integration_test",
	}

	token := ctx.createJWT(t, claims)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	ctx.Router.ServeHTTP(w, req)

	// Should fail because user mapping doesn't exist (auto-create is disabled)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 for missing user mapping, got %d", w.Code)
	}
}

func TestRequireRole_Success(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	// Add a protected route that requires admin role
	ctx.Router.GET("/admin-only", OIDCAuth(), RequireRole("admin"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	// Create claims with admin role
	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    ctx.Issuer,
			Subject:   "zitadel_integration_user",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email: "integration@test.local",
		OrgID: "org_integration_test",
		Roles: map[string]interface{}{
			"admin": map[string]interface{}{},
		},
	}

	token := ctx.createJWT(t, claims)

	req := httptest.NewRequest(http.MethodGet, "/admin-only", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	ctx.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d, body: %s", w.Code, w.Body.String())
	}
}

func TestRequireRole_Forbidden(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	// Add a protected route that requires admin role
	ctx.Router.GET("/admin-only-forbidden", OIDCAuth(), RequireRole("admin"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	// Create claims without admin role
	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    ctx.Issuer,
			Subject:   "zitadel_integration_user",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email: "integration@test.local",
		OrgID: "org_integration_test",
		Roles: map[string]interface{}{
			"viewer": map[string]interface{}{}, // Not admin
		},
	}

	token := ctx.createJWT(t, claims)

	req := httptest.NewRequest(http.MethodGet, "/admin-only-forbidden", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	ctx.Router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}
}

func TestRequireAnyRole_Success(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	// Add a protected route that requires admin or editor role
	ctx.Router.GET("/any-role", OIDCAuth(), RequireAnyRole("admin", "editor"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	// Create claims with editor role (one of the required roles)
	claims := OIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    ctx.Issuer,
			Subject:   "zitadel_integration_user",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email: "integration@test.local",
		OrgID: "org_integration_test",
		Roles: map[string]interface{}{
			"editor": map[string]interface{}{},
		},
	}

	token := ctx.createJWT(t, claims)

	req := httptest.NewRequest(http.MethodGet, "/any-role", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	ctx.Router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}
