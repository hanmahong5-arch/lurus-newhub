package handler

// v2_token_rotate_idor_test.go — RotateTokenV2 (POST
// /api/v2/:tenant_slug/tokens/:id/rotate) already carries the same
// `token.TenantId != tenantCtx.TenantID` ownership check as UpdateTokenV2 /
// DeleteTokenV2, but — unlike those two — had no test proving it. The v2
// completeness guard (router/v2_completeness_test.go) requires a named test
// before a by-id mutation route can be marked swept; this is that test.
// Mirrors TestUpdateTokenV2_TenantMismatch (v2_token_test.go).

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

func TestRotateTokenV2_TenantMismatch(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Token belongs to a different tenant than the caller.
	otherTenantID := "other-tenant-rotate-456"
	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "Victim Token")
	token.TenantId = otherTenantID
	ctx.DB.Save(token)
	originalKey := token.Key

	path := fmt.Sprintf("/api/v2/test-tenant/tokens/%d/rotate", token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, path, nil, nil)

	AssertV2Status(t, w, http.StatusForbidden)

	var reloaded repo.Token
	if err := ctx.DB.First(&reloaded, token.Id).Error; err != nil {
		t.Fatalf("failed to reload victim token: %v", err)
	}
	if reloaded.Key != originalKey {
		t.Errorf("victim token key was rotated by a cross-tenant caller: got %q, want unchanged %q", reloaded.Key, originalKey)
	}
}

// TestRotateTokenV2_SameTenantControl guards against over-tightening: the
// token owner rotating their own token must still succeed (200), not
// regress to 403 alongside the cross-tenant fix above.
func TestRotateTokenV2_SameTenantControl(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	token := SeedV2Token(t, ctx, ctx.NormalUser.Id, "Mine")
	originalKey := token.Key

	path := fmt.Sprintf("/api/v2/test-tenant/tokens/%d/rotate", token.Id)
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, path, nil, nil)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	newKey, _ := data["key"].(string)
	if newKey == "" || newKey == "sk-"+originalKey {
		t.Errorf("expected a freshly rotated key, got %q", newKey)
	}
}
