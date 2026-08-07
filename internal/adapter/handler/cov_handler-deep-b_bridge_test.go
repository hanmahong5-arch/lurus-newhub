package handler

// cov_handler-deep-b_bridge_test.go — closes the one remaining branch in
// v2_bridge.go: the session.Save() error path. gorilla/securecookie caps an
// encoded cookie at 4096 bytes by default; a user row with a pathologically
// long username (the kind that can arrive via an external IDP import or a
// direct DB seed that bypasses app-level length validation — not a
// synthetic nil/zero value) pushes the serialized session over that cap, so
// session.Save() genuinely fails and the handler must surface 500 rather
// than silently dropping the session.
//
// Reuses buildBridgeRouter from v2_bridge_test.go (same package).

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestBridgeExchange_SessionSaveOversizeCookie(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// gorilla/securecookie's default MaxLength is 4096 bytes for the whole
	// encoded cookie (base64 + HMAC + gob overhead); a 6000-byte username
	// alone is comfortably over that once the session is serialized.
	hugeUsername := strings.Repeat("u", 6000)
	oversized := &repo.User{
		Username:    hugeUsername,
		DisplayName: "Oversized Session User",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "oversize@test.local",
		TenantId:    ctx.TenantID,
	}
	if err := ctx.DB.Create(oversized).Error; err != nil {
		t.Fatalf("seed oversized-username user: %v", err)
	}

	t.Setenv("E2E_BRIDGE_TOKEN", "correct-token")
	r := buildBridgeRouter()

	url := "/api/v2/bridge/exchange?token=correct-token&user_id=" + strconv.Itoa(oversized.Id)
	req := httptest.NewRequest(http.MethodPost, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (session.Save() must fail on an oversized cookie), body: %s", w.Code, w.Body.String())
	}
	resp := ParseV2Response(t, w)
	if resp["message"] != "failed to persist session" {
		t.Errorf("message = %v, want 'failed to persist session'", resp["message"])
	}
	if len(w.Header().Values("Set-Cookie")) != 0 {
		t.Errorf("a Set-Cookie header must not be present when Save() failed")
	}
}
