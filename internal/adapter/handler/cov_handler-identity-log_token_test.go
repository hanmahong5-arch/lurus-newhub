package handler

// ============================================================================
// token.go edge cases beyond token_v1_extra_test.go / cover_r2_auth_test.go:
//   - re-enabling an EXPIRED or QUOTA-EXHAUSTED token must be refused
//     (app.CanEnableToken), not silently flip the status bit.
//   - deleting another user's token id (cross-user IDOR on the numeric
//     token id) must fail closed, not delete someone else's credential.
//   - GetTokenStatus on a non-existent token_id fails closed.
//   - UpdateToken's malformed-body / quota / expired-time validation branches.
// Reuses registerV1TokenRoutes (token_v1_extra_test.go, same package).
// ============================================================================

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// TestHandlerIdentityLog_UpdateToken_CannotEnableExpiredToken proves an
// expired token cannot be flipped back to enabled through the status_only
// path — the business rule requires the caller to first fix the expiry.
func TestHandlerIdentityLog_UpdateToken_CannotEnableExpiredToken(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	uid := ctx.NormalUser.Id
	registerV1TokenRoutes(ctx, uid)

	tok := SeedV2Token(t, ctx, uid, "expired-tok")
	tok.Status = common.TokenStatusExpired
	tok.ExpiredTime = common.GetTimestamp() - 3600 // one hour in the past
	tok.UnlimitedQuota = true
	if err := ctx.DB.Save(tok).Error; err != nil {
		t.Fatalf("seed expired token: %v", err)
	}

	body := map[string]interface{}{
		"id": tok.Id, "name": tok.Name, "status": common.TokenStatusEnabled,
		"unlimited_quota": true, "expired_time": tok.ExpiredTime,
	}
	w := V2Request(ctx.Router, http.MethodPut, "/api/token/?status_only=true", body, nil)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Fatalf("enabling an expired token must be refused, body=%s", w.Body.String())
	}

	var reloaded repo.Token
	if err := ctx.DB.First(&reloaded, tok.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status == common.TokenStatusEnabled {
		t.Error("expired token must not have been flipped to enabled in the DB")
	}
}

// TestHandlerIdentityLog_UpdateToken_CannotEnableExhaustedToken mirrors the
// expired case for the quota-exhausted branch: RemainQuota<=0 and not
// unlimited blocks re-enabling.
func TestHandlerIdentityLog_UpdateToken_CannotEnableExhaustedToken(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	uid := ctx.NormalUser.Id
	registerV1TokenRoutes(ctx, uid)

	tok := SeedV2Token(t, ctx, uid, "exhausted-tok")
	tok.Status = common.TokenStatusExhausted
	tok.UnlimitedQuota = false
	tok.RemainQuota = 0
	tok.ExpiredTime = -1
	if err := ctx.DB.Save(tok).Error; err != nil {
		t.Fatalf("seed exhausted token: %v", err)
	}

	body := map[string]interface{}{
		"id": tok.Id, "name": tok.Name, "status": common.TokenStatusEnabled,
		"unlimited_quota": false, "remain_quota": 0, "expired_time": -1,
	}
	w := V2Request(ctx.Router, http.MethodPut, "/api/token/?status_only=true", body, nil)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Fatalf("enabling a quota-exhausted token must be refused, body=%s", w.Body.String())
	}

	var reloaded repo.Token
	if err := ctx.DB.First(&reloaded, tok.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status == common.TokenStatusEnabled {
		t.Error("exhausted token must not have been flipped to enabled in the DB")
	}
}

// TestHandlerIdentityLog_UpdateToken_TopUpQuotaThenEnableSucceeds proves the
// converse of the two guard tests above: once the caller actually raises
// RemainQuota above zero, the SAME status_only=enable request that was
// refused before now succeeds — the guard is state-dependent, not a
// permanent lock.
func TestHandlerIdentityLog_UpdateToken_TopUpQuotaThenEnableSucceeds(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	uid := ctx.NormalUser.Id
	registerV1TokenRoutes(ctx, uid)

	tok := SeedV2Token(t, ctx, uid, "topped-up-tok")
	tok.Status = common.TokenStatusExhausted
	tok.UnlimitedQuota = false
	tok.RemainQuota = 5000
	tok.ExpiredTime = -1
	if err := ctx.DB.Save(tok).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	body := map[string]interface{}{
		"id": tok.Id, "name": tok.Name, "status": common.TokenStatusEnabled,
		"unlimited_quota": false, "remain_quota": 5000, "expired_time": -1,
	}
	w := V2Request(ctx.Router, http.MethodPut, "/api/token/?status_only=true", body, nil)
	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	var reloaded repo.Token
	if err := ctx.DB.First(&reloaded, tok.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != common.TokenStatusEnabled {
		t.Errorf("status = %d, want enabled(%d) once quota is available", reloaded.Status, common.TokenStatusEnabled)
	}
}

// TestHandlerIdentityLog_DeleteToken_CrossUserIDOR proves deleting a token
// id you don't own fails (repo scopes the delete by id AND user_id) and the
// victim's token survives untouched.
func TestHandlerIdentityLog_DeleteToken_CrossUserIDOR(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	attackerID := ctx.NormalUser.Id
	registerV1TokenRoutes(ctx, attackerID)

	victimTok := SeedV2Token(t, ctx, ctx.AdminUser.Id, "victim-token")

	w := V2Request(ctx.Router, http.MethodDelete, fmt.Sprintf("/api/token/%d", victimTok.Id), nil, nil)
	resp := ParseV2Response(t, w)
	if success, _ := resp["success"].(bool); success {
		t.Fatalf("cross-user token delete must fail, body=%s", w.Body.String())
	}

	var count int64
	ctx.DB.Model(&repo.Token{}).Where("id = ?", victimTok.Id).Count(&count)
	if count != 1 {
		t.Errorf("victim's token was deleted by a non-owner caller, count=%d want 1", count)
	}
}

// TestHandlerIdentityLog_GetTokenStatus_NotFound proves an unknown token_id
// fails closed via ApiError rather than returning a fabricated zero-value
// credit summary.
func TestHandlerIdentityLog_GetTokenStatus_NotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := r2authReq(http.MethodGet, "/api/token/status", nil)
	c.Set("id", ctx.NormalUser.Id)
	c.Set("token_id", 987654) // never seeded
	GetTokenStatus(c)

	body := r2authBody(t, w)
	if body["object"] == "credit_summary" {
		t.Fatalf("non-existent token must not return a synthesized credit_summary, body=%s", w.Body.String())
	}
}

// TestHandlerIdentityLog_UpdateToken_ValidationGaps covers the malformed
// body, negative-quota, and far-future-expiry rejections that
// TestUpdateToken_V1_StatusOnly (token_v1_extra_test.go) didn't reach.
func TestHandlerIdentityLog_UpdateToken_ValidationGaps(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	uid := ctx.NormalUser.Id
	registerV1TokenRoutes(ctx, uid)
	tok := SeedV2Token(t, ctx, uid, "validation-tok")

	t.Run("malformed_json_body", func(t *testing.T) {
		c, w := r2authReq(http.MethodPut, "/api/token/", "{not-json")
		c.Set("id", uid)
		UpdateToken(c)
		body := r2authBody(t, w)
		if success, _ := body["success"].(bool); success {
			t.Fatalf("malformed body must be rejected, body=%s", w.Body.String())
		}
	})

	t.Run("negative_quota_rejected", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"id": tok.Id, "name": tok.Name, "remain_quota": -5, "unlimited_quota": false, "expired_time": -1,
		}
		w := V2Request(ctx.Router, http.MethodPut, "/api/token/", reqBody, nil)
		resp := ParseV2Response(t, w)
		if success, _ := resp["success"].(bool); success {
			t.Fatalf("negative remain_quota must be rejected, body=%s", w.Body.String())
		}
		if resp["message"] != "额度值不能为负数" {
			t.Errorf("message = %v", resp["message"])
		}
	})

	t.Run("far_future_expiry_rejected", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"id": tok.Id, "name": tok.Name, "remain_quota": 1, "unlimited_quota": false,
			"expired_time": int64(99999999999999), // absurdly far future — likely a unit mistake
		}
		w := V2Request(ctx.Router, http.MethodPut, "/api/token/", reqBody, nil)
		resp := ParseV2Response(t, w)
		if success, _ := resp["success"].(bool); success {
			t.Fatalf("implausibly far-future expiry must be rejected, body=%s", w.Body.String())
		}
	})
}
