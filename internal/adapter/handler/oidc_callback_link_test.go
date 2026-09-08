package handler

// oidc_callback_link_test.go — L4 (BILL-B2): OIDCCallback must persist the
// platform-account link onto the user row, not just the session. Before this
// fix the callback only did session.Set("identity_account_id", ...) — the
// browser-SSO login path never called repo.LinkUserPlatformAccount (only the
// /internal provisioning self-heal path did), so an SSO-only user's
// users.lurus_account_id stayed NULL forever and every token minted for them
// (which reads the user ROW, not the session) never engaged the wallet-debit
// gate.
//
// Full round trip against a real httptest IdP (token endpoint + JWKS),
// mirroring the pattern in middleware/oidc_auth_integration_test.go — the
// only difference is going through the exported middleware.InitOIDCAuth()
// entrypoint (jwksManager is unexported, so this package can't poke it
// directly like the middleware-package tests do).

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const oidcCallbackTestKid = "oidc-callback-link-test-kid"

// oidcCallbackTestKeyOnce/oidcCallbackTestKey: middleware.InitOIDCAuth's
// jwksManager is created behind a package-level sync.Once (see
// middleware/oidc_auth.go), so only the FIRST call in this test binary
// actually creates the manager — every later harness reuses that same cached
// key set instead of re-fetching from its own (possibly already-closed) JWKS
// server. Signing every harness's tokens with the SAME RSA key under the
// SAME kid keeps verification working regardless of which harness's JWKS
// response originally seeded the cache.
var (
	oidcCallbackTestKeyOnce sync.Once
	oidcCallbackTestKey     *rsa.PrivateKey
)

func oidcCallbackSharedKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	oidcCallbackTestKeyOnce.Do(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("generate RSA key: %v", err)
		}
		oidcCallbackTestKey = key
	})
	return oidcCallbackTestKey
}

// rsaPublicKeyToJWKForCallbackTest converts an RSA public key to the exported
// middleware.JWK wire shape (mirrors middleware's own unexported
// rsaPublicKeyToJWK, duplicated here because this package can't reach it).
func rsaPublicKeyToJWKForCallbackTest(pub *rsa.PublicKey) middleware.JWK {
	return middleware.JWK{
		Kty: "RSA",
		Use: "sig",
		Kid: oidcCallbackTestKid,
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// oidcCallbackTestHarness wires a real httptest IdP (token + JWKS endpoints),
// a real httptest platform identity service (account resolver seam), a
// SQLite-backed repo.DB with a pre-seeded tenant, and a gin router carrying
// real session middleware in front of OIDCCallback.
type oidcCallbackTestHarness struct {
	router     *gin.Engine
	privateKey *rsa.PrivateKey
	issuer     string
	clientID   string
	tenantSlug string
	tenantID   string
	identityID int64 // account id the identity server resolves every subject to
	cleanup    func()
}

func setupOIDCCallbackHarness(t *testing.T, resolveAccountID int64) *oidcCallbackTestHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	v2ctx := SetupV2TestRouter(t)

	privateKey := oidcCallbackSharedKey(t)
	jwk := rsaPublicKeyToJWKForCallbackTest(&privateKey.PublicKey)
	jwksSet := middleware.JWKSet{Keys: []middleware.JWK{jwk}}

	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(jwksSet)
		case "/oauth/v2/token":
			// The fake "authorization code" IS the pre-signed ID token — this
			// test controls the code value end to end (it never goes through a
			// real authorize redirect), so echoing it back as id_token is the
			// simplest way to hand OIDCCallback a token it can verify against
			// the JWKS server above.
			_ = r.ParseForm()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token":  "test-access-token",
				"refresh_token": "test-refresh-token",
				"id_token":      r.PostFormValue("code"),
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	identitySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":          resolveAccountID,
			"idp_subject": "unused",
		})
	}))

	clientID := "callback-link-test-client"

	t.Setenv("OIDC_ENABLED", "true")
	t.Setenv("OIDC_ISSUER", idp.URL)
	t.Setenv("OIDC_JWKS_URI", idp.URL+"/jwks")
	t.Setenv("OIDC_CLIENT_ID", clientID)
	t.Setenv("OIDC_ENABLE_PKCE", "false")
	t.Setenv("OIDC_AUTO_CREATE_USER", "true")
	t.Setenv("OIDC_AUTO_CREATE_TENANT", "false")
	t.Setenv("OIDC_CLIENT_SECRET", "")

	prevIdentityURL := common.IdentityServiceURL
	common.IdentityServiceURL = identitySrv.URL
	t.Cleanup(func() { common.IdentityServiceURL = prevIdentityURL })

	if err := middleware.InitOIDCAuth(); err != nil {
		t.Fatalf("InitOIDCAuth: %v", err)
	}

	router := gin.New()
	store := cookie.NewStore([]byte("oidc-callback-link-test-secret"))
	router.Use(sessions.Sessions("nh_test_session", store))
	router.GET("/api/v2/oauth/callback", OIDCCallback)

	cleanup := func() {
		idp.Close()
		identitySrv.Close()
	}

	return &oidcCallbackTestHarness{
		router:     router,
		privateKey: privateKey,
		issuer:     idp.URL,
		clientID:   clientID,
		tenantSlug: v2ctx.TenantID, // SetupV2TestRouter uses the same value for Tenant.Id and Tenant.Slug
		tenantID:   v2ctx.TenantID,
		identityID: resolveAccountID,
		cleanup:    cleanup,
	}
}

// signIDToken builds and signs a valid ID token for the given subject/email.
func (h *oidcCallbackTestHarness) signIDToken(t *testing.T, subject, email string) string {
	t.Helper()
	claims := IDTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    h.issuer,
			Subject:   subject,
			Audience:  jwt.ClaimStrings{h.clientID},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email:         email,
		EmailVerified: true,
		Name:          "OIDC Callback Test User",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = oidcCallbackTestKid
	signed, err := token.SignedString(h.privateKey)
	if err != nil {
		t.Fatalf("sign ID token: %v", err)
	}
	return signed
}

// doCallback drives OIDCCallback end-to-end: generates real state and uses
// the pre-signed ID token AS the authorization code — exchangeCodeForToken
// posts it straight through to the fake token endpoint, which echoes it back
// as id_token (see the /oauth/v2/token handler above).
func (h *oidcCallbackTestHarness) doCallback(t *testing.T, idToken string) *httptest.ResponseRecorder {
	t.Helper()
	state, _, err := generateOAuthState(h.tenantSlug, "/dashboard")
	if err != nil {
		t.Fatalf("generateOAuthState: %v", err)
	}
	q := url.Values{}
	q.Set("code", idToken)
	q.Set("state", state)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/oauth/callback?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return w
}

func TestOIDCCallback_LinksPlatformAccountOntoUser(t *testing.T) {
	h := setupOIDCCallbackHarness(t, 4242)
	defer h.cleanup()

	idToken := h.signIDToken(t, "oidc-link-subject-1", "oidc-link-1@example.com")
	w := h.doCallback(t, idToken)

	if w.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want 302 (redirect on success); body=%s", w.Code, w.Body.String())
	}

	user, err := repo.GetUserByLurusAccountID(4242)
	if err != nil || user == nil {
		t.Fatalf("repo.GetUserByLurusAccountID(4242) = %v, %v — link was never persisted", user, err)
	}
	if user.Email != "oidc-link-1@example.com" {
		t.Errorf("linked user email = %q, want the callback user", user.Email)
	}

	key, err := app.GenerateTokenKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	clean := app.BuildCleanToken(user.Id, user.TenantId, &repo.Token{RemainQuota: 1}, key)
	if clean.IdentityAccountID != 4242 {
		t.Errorf("app.BuildCleanToken(callback user).IdentityAccountID = %d, want 4242 — a token minted for this user would never engage the wallet-debit gate", clean.IdentityAccountID)
	}
}

func TestOIDCCallback_AccountAlreadyBoundElsewhere_SkipsRebindLoginSucceeds(t *testing.T) {
	h := setupOIDCCallbackHarness(t, 4242)
	defer h.cleanup()

	// Pre-bind 4242 to an unrelated existing user, simulating a prior login.
	priorUser := &repo.User{
		Username:    "prior-owner",
		DisplayName: "Prior Owner",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "prior-owner@example.com",
		TenantId:    h.tenantID,
		Quota:       1_000_000,
	}
	if err := repo.DB.Create(priorUser).Error; err != nil {
		t.Fatalf("seed prior owner: %v", err)
	}
	if err := repo.LinkUserPlatformAccount(priorUser.Id, 4242, true); err != nil {
		t.Fatalf("pre-bind: %v", err)
	}

	idToken := h.signIDToken(t, "oidc-link-subject-2", "oidc-link-2@example.com")
	w := h.doCallback(t, idToken)

	if w.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want 302 (a link collision must never fail login); body=%s", w.Code, w.Body.String())
	}

	// The original binding must be untouched.
	stillPrior, err := repo.GetUserByLurusAccountID(4242)
	if err != nil || stillPrior == nil || stillPrior.Id != priorUser.Id {
		t.Fatalf("account 4242's original binding was disturbed: %v, %v", stillPrior, err)
	}

	// The new callback user must exist but remain UNLINKED (never re-bound).
	newUser, _, err := repo.GetUserByIDPSubject("oidc-link-subject-2", h.tenantID)
	if err != nil || newUser == nil {
		t.Fatalf("callback user was not created: %v, %v", newUser, err)
	}
	if newUser.LurusAccountID != nil {
		t.Errorf("new callback user got bound to account %d, which already belongs to another user", *newUser.LurusAccountID)
	}
}

// TestOIDCCallback_AccountAlreadyBoundElsewhere_AnotherTenant_SkipsRebindLoginSucceeds
// is the cross-tenant variant: the prior owner of accountID 4242 lives in a
// DIFFERENT tenant than the callback user. GetUserByLurusAccountID resolves
// GLOBALLY (bypasses tenant isolation — see provisionRaceWinner's comment in
// internal_api_ext.go), so the never-re-bind guard must still fire here: a
// same-account collision across tenants is exactly the case the guard exists
// to stop two local users (in any tenant) sharing one wallet.
func TestOIDCCallback_AccountAlreadyBoundElsewhere_AnotherTenant_SkipsRebindLoginSucceeds(t *testing.T) {
	h := setupOIDCCallbackHarness(t, 4242)
	defer h.cleanup()

	otherTenant := &repo.Tenant{
		Id:        "oidc-link-other-tenant",
		Name:      "Other Tenant",
		Slug:      "oidc-link-other-tenant",
		Status:    repo.TenantStatusEnabled,
		IDPOrgID:  "org_oidc_link_other",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := repo.DB.Create(otherTenant).Error; err != nil {
		t.Fatalf("seed other tenant: %v", err)
	}

	// Pre-bind 4242 to an existing user in the OTHER tenant.
	priorUser := &repo.User{
		Username:    "prior-owner-other-tenant",
		DisplayName: "Prior Owner Other Tenant",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "prior-owner-other-tenant@example.com",
		TenantId:    otherTenant.Id,
		Quota:       1_000_000,
	}
	if err := repo.DB.Create(priorUser).Error; err != nil {
		t.Fatalf("seed prior owner: %v", err)
	}
	if err := repo.LinkUserPlatformAccount(priorUser.Id, 4242, true); err != nil {
		t.Fatalf("pre-bind: %v", err)
	}

	idToken := h.signIDToken(t, "oidc-link-subject-3", "oidc-link-3@example.com")
	w := h.doCallback(t, idToken)

	if w.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want 302 (a cross-tenant link collision must never fail login); body=%s", w.Code, w.Body.String())
	}

	// The original binding (in the other tenant) must be untouched.
	stillPrior, err := repo.GetUserByLurusAccountID(4242)
	if err != nil || stillPrior == nil || stillPrior.Id != priorUser.Id {
		t.Fatalf("account 4242's original binding was disturbed: %v, %v", stillPrior, err)
	}

	// The new callback user must exist in ITS OWN tenant but remain UNLINKED.
	newUser, _, err := repo.GetUserByIDPSubject("oidc-link-subject-3", h.tenantID)
	if err != nil || newUser == nil {
		t.Fatalf("callback user was not created: %v, %v", newUser, err)
	}
	if newUser.LurusAccountID != nil {
		t.Errorf("new callback user got bound to account %d, which already belongs to a user in another tenant", *newUser.LurusAccountID)
	}
}
