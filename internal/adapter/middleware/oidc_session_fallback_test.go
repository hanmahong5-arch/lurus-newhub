package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// TestOIDCAuth_SessionFallback_TenantFromUserNotSlug is a regression test
// for a cross-tenant isolation bug in handleSessionFallback.
//
// Before the fix, the session-fallback branch of OIDCAuth derived the
// tenant from the URL :tenant_slug instead of the authenticated user's own
// record. Because downstream guards compare tenantCtx.TenantID against that
// same slug (e.g. GetCreditPoolForEndUser's TENANT_MISMATCH check), the slug
// was effectively compared to itself and always passed — so any
// session-authenticated user could read another tenant's data (e.g. credit
// pool balance) just by typing the victim's slug into the path.
//
// Invariant under test: the session fallback MUST resolve the tenant to the
// authenticated user's own tenant. Pre-fix this assertion fails (it resolved
// to the victim's tenant, or to "default"); post-fix it holds.
func TestOIDCAuth_SessionFallback_TenantFromUserNotSlug(t *testing.T) {
	ctx := setupIntegrationTest(t)
	defer ctx.Cleanup()

	// ctx.Tenant is tenant A (slug "integration-test"); ctx.User belongs to A.
	// Create a second, "victim" tenant B that ctx.User does NOT belong to.
	victim := &repo.Tenant{
		Id:           "victim-tenant-id",
		Name:         "Victim Tenant",
		Slug:         "victim-tenant",
		Status:       repo.TenantStatusEnabled,
		IDPOrgID: "org_victim",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := ctx.DB.Create(victim).Error; err != nil {
		t.Fatalf("create victim tenant: %v", err)
	}

	// buildRouter establishes a session for ctx.User with a non-expired OAuth
	// access token and NO Bearer header — exactly the condition that drives
	// handleSessionFallback — then runs OIDCAuth on a tenant-scoped route
	// and echoes the resolved tenant context.
	buildRouter := func() *gin.Engine {
		r := gin.New()
		r.Use(gin.Recovery())

		store := cookie.NewStore([]byte("session-fallback-test-secret"))
		r.Use(sessions.Sessions("session", store))

		// Session injector: simulates a user already logged in via OAuth.
		r.Use(func(c *gin.Context) {
			s := sessions.Default(c)
			s.Set("id", ctx.User.Id)
			s.Set("oauth_access_token", "fake-session-token")
			s.Set("oauth_token_expires_at", time.Now().Add(time.Hour).Unix())
			_ = s.Save()
			c.Next()
		})

		r.GET("/api/v2/:tenant_slug/probe", OIDCAuth(), func(c *gin.Context) {
			tc, err := GetTenantContext(c)
			if err != nil || tc == nil {
				c.JSON(http.StatusUnauthorized, gin.H{"success": false})
				return
			}
			c.JSON(http.StatusOK, gin.H{
				"success":   true,
				"tenant_id": tc.TenantID,
				"user_id":   tc.UserID,
			})
		})
		return r
	}

	probe := func(t *testing.T, slug string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v2/"+slug+"/probe", nil)
		w := httptest.NewRecorder()
		buildRouter().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("slug %q: status = %d, want 200 (body=%s)", slug, w.Code, w.Body.String())
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("slug %q: unmarshal: %v", slug, err)
		}
		got, _ := resp["tenant_id"].(string)
		return got
	}

	t.Run("victim_slug_resolves_to_attackers_own_tenant", func(t *testing.T) {
		// The attacker (a member of tenant A) types the victim's slug.
		got := probe(t, victim.Slug)
		if got == victim.Id {
			t.Fatalf("CROSS-TENANT LEAK: session user of tenant %q resolved to victim tenant %q via URL slug",
				ctx.Tenant.Id, victim.Id)
		}
		if got != ctx.Tenant.Id {
			t.Fatalf("tenant_id = %q, want attacker's own tenant %q", got, ctx.Tenant.Id)
		}
	})

	t.Run("own_slug_resolves_correctly", func(t *testing.T) {
		got := probe(t, ctx.Tenant.Slug)
		if got != ctx.Tenant.Id {
			t.Fatalf("tenant_id = %q, want own tenant %q", got, ctx.Tenant.Id)
		}
	})
}
