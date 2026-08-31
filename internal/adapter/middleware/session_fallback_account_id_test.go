package middleware

// Pins the platform-account linkage propagation in handleSessionFallback.
// Live-probed 2026-08-31: a freshly bridged (zita-bootstrap) user got 503
// "platform account not linked" from every /api/v2/user/billing/* endpoint —
// the linkage sat in the gin session AND the users table, but the session
// fallback never copied it into the gin context that getIdentityAccountID
// reads. Only Bearer-JWT callers ever got the key, so every browser user was
// locked out of billing/top-up behind an "unavailable" banner.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// mountAccountIDProbe mirrors mountSessionFallbackProbe but reports the
// identity_account_id context key (as int64) alongside the handled flag.
func mountAccountIDProbe(preset map[string]any) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("sf-acct-secret"))
	r.Use(sessions.Sessions("session", store))
	r.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		for k, v := range preset {
			s.Set(k, v)
		}
		_ = s.Save()
		c.Next()
	})
	r.GET("/sf", func(c *gin.Context) {
		handled := handleSessionFallback(c)
		if c.IsAborted() {
			return
		}
		var acct int64
		if v, ok := c.Get("identity_account_id"); ok {
			acct, _ = v.(int64)
		}
		c.JSON(http.StatusOK, gin.H{"handled": handled, "account_id": acct})
	})
	return r
}

func probeAccountID(t *testing.T, preset map[string]any) (bool, int64) {
	t.Helper()
	w := httptest.NewRecorder()
	mountAccountIDProbe(preset).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sf", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("probe status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Handled   bool  `json:"handled"`
		AccountID int64 `json:"account_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse probe body: %v (%s)", err, w.Body.String())
	}
	return out.Handled, out.AccountID
}

// The zita-bootstrap shape: session carries identity_account_id (int64), no
// oauth token → expired/missing-token branch. The context must receive it.
func TestSessionFallback_BridgedSession_PropagatesAccountID(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	user := &repo.User{
		Username: "sfacct1", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "sfa1@local", TenantId: "default",
	}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	handled, acct := probeAccountID(t, map[string]any{
		"id":                  user.Id,
		"identity_account_id": int64(184),
	})
	if !handled {
		t.Fatalf("fallback should have handled a valid session")
	}
	if acct != 184 {
		t.Errorf("identity_account_id in context = %d, want 184 (from session)", acct)
	}
}

// Session lacks the key (pre-fix cookie, or legacy OAuth session) but the user
// row carries lurus_account_id → the column is the fallback source.
func TestSessionFallback_UserColumnFallback_PropagatesAccountID(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	lid := int64(555)
	user := &repo.User{
		Username: "sfacct2", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "sfa2@local", TenantId: "default", LurusAccountID: &lid,
	}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	// Non-expired-token branch this time, to cover the other injection site.
	handled, acct := probeAccountID(t, map[string]any{
		"id":                     user.Id,
		"oauth_access_token":     "live-token",
		"oauth_token_expires_at": time.Now().Add(time.Hour).Unix(),
	})
	if !handled {
		t.Fatalf("fallback should have handled a valid session")
	}
	if acct != 555 {
		t.Errorf("identity_account_id in context = %d, want 555 (from users.lurus_account_id)", acct)
	}
}

// No linkage anywhere → the key must stay absent (0), not a fabricated value.
func TestSessionFallback_NoLinkage_NoAccountID(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()
	user := &repo.User{
		Username: "sfacct3", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "sfa3@local", TenantId: "default",
	}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	handled, acct := probeAccountID(t, map[string]any{"id": user.Id})
	if !handled {
		t.Fatalf("fallback should have handled a valid session")
	}
	if acct != 0 {
		t.Errorf("identity_account_id = %d, want 0 for an unlinked user", acct)
	}
}
