package middleware

// oidc_auth_link_guard_test.go — L4: OIDCAuth's platform-account link guard
// (oidc_auth.go, "if lurusUser.LurusAccountID == nil") must make the
// repo.LinkUserPlatformAccount write a ONE-TIME cost per user, not a
// per-request one. lurusUser is loaded fresh from the DB on every request
// (line ~797), so without the guard every authenticated request for an
// already-linked user would re-run the `UPDATE users SET lurus_account_id
// = ...` statement — a harmless no-op given the `lurus_account_id IS NULL`
// clause in the repo layer, but a wasted write against the primary DB on
// every single JWT-authenticated request.
//
// Drives the real OIDCAuth middleware (via setupIntegrationTest's harness)
// twice with the same bearer token and counts `users` table UPDATE
// statements with a GORM callback probe, the way a query counter would.

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

func TestOIDCAuth_AccountLinkGuard_SecondRequestSkipsWrite(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	// Fake platform identity service: UpsertAccountGRPC falls back to this
	// HTTP endpoint when the gRPC client can't reach a real platform-core.
	identitySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9101,"idp_subject":"zitadel_integration_user"}`))
	}))
	defer identitySrv.Close()
	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = identitySrv.URL
	defer func() { common.IdentityServiceURL = prevURL }()

	var userUpdateCount int32
	if err := ctx.DB.Callback().Update().Before("gorm:update").Register("link_guard_probe_count", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			atomic.AddInt32(&userUpdateCount, 1)
		}
	}); err != nil {
		t.Fatalf("register probe callback: %v", err)
	}
	defer func() { _ = ctx.DB.Callback().Update().Remove("link_guard_probe_count") }()

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

	doReq := func() int {
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		ctx.Router.ServeHTTP(w, req)
		return w.Code
	}

	atomic.StoreInt32(&userUpdateCount, 0)
	if code := doReq(); code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", code)
	}
	if got := atomic.LoadInt32(&userUpdateCount); got != 1 {
		t.Fatalf("first request: users UPDATE fired %d times, want 1 (the initial link)", got)
	}

	var linked repo.User
	if err := ctx.DB.First(&linked, "id = ?", ctx.User.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if linked.LurusAccountID == nil || *linked.LurusAccountID != 9101 {
		t.Fatalf("user.lurus_account_id = %v, want 9101", linked.LurusAccountID)
	}

	atomic.StoreInt32(&userUpdateCount, 0)
	if code := doReq(); code != http.StatusOK {
		t.Fatalf("second request status = %d, want 200", code)
	}
	if got := atomic.LoadInt32(&userUpdateCount); got != 0 {
		t.Errorf("second request (already-linked user): users UPDATE fired %d times, want 0 — the LurusAccountID==nil guard should have skipped the write entirely", got)
	}
}
