package handler

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// setupPlanGrantTest reuses setupProvisionTest (shared V2 sqlite DB + local
// JWKS stub verifier), adds the plan_quota_grants table and mounts both
// provision and plan-grant.
func setupPlanGrantTest(t *testing.T) (*gin.Engine, *V2TestContext, *rsa.PrivateKey) {
	t.Helper()
	r, ctx, key := setupProvisionTest(t)
	if err := repo.DB.AutoMigrate(&entity.PlanQuotaGrant{}); err != nil {
		t.Fatalf("migrate plan_quota_grants: %v", err)
	}
	r.POST("/api/v2/:tenant_slug/plan-grant", PlanGrantV2)
	return r, ctx, key
}

func planGrantReq(t *testing.T, r *gin.Engine, slug, token string) (int, map[string]any) {
	t.Helper()
	w := V2Request(r, http.MethodPost, "/api/v2/"+slug+"/plan-grant", map[string]any{"entitlement_token": token}, nil)
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v raw=%s", err, w.Body.String())
	}
	return w.Code, resp
}

func planGrantEnt(overrides map[string]string) map[string]string {
	ent := map[string]string{
		"plan_code":               "cc_pro",
		"newhub_pool_amount":      "500000",
		"subscription_id":         "sub_42",
		"subscription_expires_at": "2026-10-23T00:00:00Z",
	}
	for k, v := range overrides {
		if v == "<delete>" {
			delete(ent, k)
			continue
		}
		ent[k] = v
	}
	return ent
}

// planGrantProvisionUser runs the real provision flow so the bridged user exists in
// ctx's tenant, and returns its hub user id + starting quota.
func planGrantProvisionUser(t *testing.T, r *gin.Engine, ctx *V2TestContext, key *rsa.PrivateKey, sub string) (int, int) {
	t.Helper()
	tok := provSignRS256(t, key, "test-kid", provClaims(sub, map[string]string{"plan_code": "cc_pro"}))
	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusOK {
		t.Fatalf("provision: expected 200, got %d body=%v", code, resp)
	}
	data := resp["data"].(map[string]any)
	uid := int(data["user_id"].(float64))
	q, err := repo.GetUserQuota(uid, true)
	if err != nil {
		t.Fatalf("GetUserQuota: %v", err)
	}
	return uid, q
}

func planGrantErrCode(resp map[string]any) string {
	s, _ := resp["error_code"].(string)
	return s
}

func planGrantCount(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := repo.DB.Model(&entity.PlanQuotaGrant{}).Count(&n).Error; err != nil {
		t.Fatalf("count grants: %v", err)
	}
	return n
}

func TestPlanGrantV2_GrantReplayRenew(t *testing.T) {
	r, ctx, key := setupPlanGrantTest(t)
	uid, q0 := planGrantProvisionUser(t, r, ctx, key, "880001")

	tok := provSignRS256(t, key, "test-kid", provClaims("880001", planGrantEnt(nil)))

	// First grant credits the user by amount.
	code, resp := planGrantReq(t, r, ctx.TenantID, tok)
	if code != http.StatusOK {
		t.Fatalf("first grant: expected 200, got %d body=%v", code, resp)
	}
	data := resp["data"].(map[string]any)
	if data["granted"] != true || data["replayed"] != false {
		t.Fatalf("first grant: want granted=true replayed=false, got %v", data)
	}
	if data["grant_key"] != "sub_42:2026-10-23T00:00:00Z" {
		t.Errorf("grant_key = %v", data["grant_key"])
	}
	q1, _ := repo.GetUserQuota(uid, true)
	if q1 != q0+500000 {
		t.Fatalf("quota after grant = %d, want %d", q1, q0+500000)
	}
	if int(data["new_quota"].(float64)) != q1 {
		t.Errorf("new_quota = %v, want %d", data["new_quota"], q1)
	}

	// Replay of the same period: no credit.
	code, resp = planGrantReq(t, r, ctx.TenantID, tok)
	if code != http.StatusOK {
		t.Fatalf("replay: expected 200, got %d body=%v", code, resp)
	}
	data = resp["data"].(map[string]any)
	if data["granted"] != false || data["replayed"] != true {
		t.Fatalf("replay: want granted=false replayed=true, got %v", data)
	}
	if q, _ := repo.GetUserQuota(uid, true); q != q1 {
		t.Fatalf("quota after replay = %d, want unchanged %d", q, q1)
	}

	// Renewal: new expires_at -> new key -> grants again.
	tok2 := provSignRS256(t, key, "test-kid", provClaims("880001",
		planGrantEnt(map[string]string{"subscription_expires_at": "2026-11-23T00:00:00Z"})))
	code, resp = planGrantReq(t, r, ctx.TenantID, tok2)
	if code != http.StatusOK {
		t.Fatalf("renewal: expected 200, got %d body=%v", code, resp)
	}
	if resp["data"].(map[string]any)["granted"] != true {
		t.Fatalf("renewal: want granted=true, got %v", resp)
	}
	if q, _ := repo.GetUserQuota(uid, true); q != q1+500000 {
		t.Fatalf("quota after renewal = %d, want %d", q, q1+500000)
	}
	if n := planGrantCount(t); n != 2 {
		t.Errorf("grant rows = %d, want 2", n)
	}
}

func TestPlanGrantV2_Refusals(t *testing.T) {
	r, ctx, key := setupPlanGrantTest(t)
	uid, q0 := planGrantProvisionUser(t, r, ctx, key, "880002")

	cases := []struct {
		name   string
		sub    string
		ent    map[string]string
		mutate func(map[string]any)
		status int
		code   string
	}{
		{"missing subscription_id", "880002", planGrantEnt(map[string]string{"subscription_id": "<delete>"}), nil,
			http.StatusUnprocessableEntity, "GRANT_KEY_MISSING"},
		{"blank subscription_id", "880002", planGrantEnt(map[string]string{"subscription_id": "  "}), nil,
			http.StatusUnprocessableEntity, "GRANT_KEY_MISSING"},
		{"not provisioned", "880999", planGrantEnt(nil), nil,
			http.StatusConflict, "NOT_PROVISIONED"},
		{"non-cc plan", "880002", planGrantEnt(map[string]string{"plan_code": "pro_monthly"}), nil,
			http.StatusForbidden, "PLAN_NOT_ELIGIBLE"},
		{"missing amount", "880002", planGrantEnt(map[string]string{"newhub_pool_amount": "<delete>"}), nil,
			http.StatusUnprocessableEntity, "NO_GRANT_AMOUNT"},
		{"zero amount", "880002", planGrantEnt(map[string]string{"newhub_pool_amount": "0"}), nil,
			http.StatusUnprocessableEntity, "NO_GRANT_AMOUNT"},
		{"negative amount", "880002", planGrantEnt(map[string]string{"newhub_pool_amount": "-5"}), nil,
			http.StatusUnprocessableEntity, "NO_GRANT_AMOUNT"},
		{"non-numeric amount", "880002", planGrantEnt(map[string]string{"newhub_pool_amount": "12.5"}), nil,
			http.StatusUnprocessableEntity, "NO_GRANT_AMOUNT"},
		{"overflowing amount", "880002", planGrantEnt(map[string]string{"newhub_pool_amount": "99999999999999999999"}), nil,
			http.StatusUnprocessableEntity, "NO_GRANT_AMOUNT"},
		{"grace (stale) token", "880002", planGrantEnt(nil), func(c map[string]any) {
			// Past hard exp but inside the 72h offline grace -> Freshness==Grace.
			c["exp"] = time.Now().Add(-2 * time.Hour).Unix()
			c["iat"] = time.Now().Add(-26 * time.Hour).Unix()
			c["nbf"] = time.Now().Add(-26 * time.Hour).Unix()
		}, http.StatusForbidden, "ENTITLEMENT_STALE"},
		{"wrong audience", "880002", planGrantEnt(nil), func(c map[string]any) { c["aud"] = "other-product" },
			http.StatusForbidden, "AUD_MISMATCH"},
		{"non-numeric sub", "abc", planGrantEnt(nil), nil,
			http.StatusUnauthorized, "TOKEN_INVALID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims := provClaims(tc.sub, tc.ent)
			if tc.mutate != nil {
				tc.mutate(claims)
			}
			tok := provSignRS256(t, key, "test-kid", claims)
			code, resp := planGrantReq(t, r, ctx.TenantID, tok)
			if code != tc.status || planGrantErrCode(resp) != tc.code {
				t.Fatalf("want %d %s, got %d %s body=%v", tc.status, tc.code, code, planGrantErrCode(resp), resp)
			}
		})
	}

	if q, _ := repo.GetUserQuota(uid, true); q != q0 {
		t.Errorf("quota changed by refused calls: %d -> %d", q0, q)
	}
	if n := planGrantCount(t); n != 0 {
		t.Errorf("refused calls wrote %d grant rows", n)
	}
}

func TestPlanGrantV2_TenantMismatch(t *testing.T) {
	r, ctx, key := setupPlanGrantTest(t)
	uid, q0 := planGrantProvisionUser(t, r, ctx, key, "880003")

	other := &repo.Tenant{Id: "tenant-other-plangrant", Slug: "other-plangrant", Name: "Other", IDPOrgID: "org-other-plangrant", Status: 1}
	if err := repo.DB.Create(other).Error; err != nil {
		t.Fatalf("create other tenant: %v", err)
	}

	tok := provSignRS256(t, key, "test-kid", provClaims("880003", planGrantEnt(nil)))
	code, resp := planGrantReq(t, r, other.Slug, tok)
	if code != http.StatusForbidden || planGrantErrCode(resp) != "TENANT_MISMATCH" {
		t.Fatalf("want 403 TENANT_MISMATCH, got %d %s body=%v", code, planGrantErrCode(resp), resp)
	}
	if q, _ := repo.GetUserQuota(uid, true); q != q0 {
		t.Errorf("quota changed: %d -> %d", q0, q)
	}
}

func TestPlanGrantV2_UserDisabled(t *testing.T) {
	r, ctx, key := setupPlanGrantTest(t)
	uid, _ := planGrantProvisionUser(t, r, ctx, key, "880004")
	if err := repo.DB.Model(&repo.User{}).Where("id = ?", uid).
		Update("status", common.UserStatusDisabled).Error; err != nil {
		t.Fatalf("disable user: %v", err)
	}
	tok := provSignRS256(t, key, "test-kid", provClaims("880004", planGrantEnt(nil)))
	code, resp := planGrantReq(t, r, ctx.TenantID, tok)
	if code != http.StatusForbidden || planGrantErrCode(resp) != "USER_DISABLED" {
		t.Fatalf("want 403 USER_DISABLED, got %d %s body=%v", code, planGrantErrCode(resp), resp)
	}
}

// TestPlanGrantV2_CreditFailureRollsBackRow: when the quota credit fails the
// grant row is removed, so a retry with the SAME key grants (not "replayed").
func TestPlanGrantV2_CreditFailureRollsBackRow(t *testing.T) {
	r, ctx, key := setupPlanGrantTest(t)
	uid, q0 := planGrantProvisionUser(t, r, ctx, key, "880005")

	prev := planGrantIncreaseQuota
	planGrantIncreaseQuota = func(int, int, bool) error { return errors.New("injected credit failure") }
	t.Cleanup(func() { planGrantIncreaseQuota = prev })

	tok := provSignRS256(t, key, "test-kid", provClaims("880005", planGrantEnt(nil)))
	code, resp := planGrantReq(t, r, ctx.TenantID, tok)
	if code != http.StatusInternalServerError || planGrantErrCode(resp) != "GRANT_FAILED" {
		t.Fatalf("want 500 GRANT_FAILED, got %d %s body=%v", code, planGrantErrCode(resp), resp)
	}
	if n := planGrantCount(t); n != 0 {
		t.Fatalf("grant row not rolled back: %d rows", n)
	}
	if q, _ := repo.GetUserQuota(uid, true); q != q0 {
		t.Fatalf("quota changed on failed credit: %d -> %d", q0, q)
	}

	planGrantIncreaseQuota = prev
	code, resp = planGrantReq(t, r, ctx.TenantID, tok)
	if code != http.StatusOK || resp["data"].(map[string]any)["granted"] != true {
		t.Fatalf("retry: want 200 granted=true, got %d body=%v", code, resp)
	}
	if q, _ := repo.GetUserQuota(uid, true); q != q0+500000 {
		t.Fatalf("quota after retry = %d, want %d", q, q0+500000)
	}
}

// A legacy account on the bootstrap tenant may provision through any slug
// (ProvisionV2's rule), so it must be able to claim through it too —
// otherwise it gets a key it cannot use.
func TestPlanGrantV2_LegacyDefaultTenantAccountMayClaim(t *testing.T) {
	r, ctx, key := setupPlanGrantTest(t)
	uid, q0 := planGrantProvisionUser(t, r, ctx, key, "880010")
	if err := repo.DB.Model(&repo.User{}).Where("id = ?", uid).Update("tenant_id", "default").Error; err != nil {
		t.Fatalf("move user to default: %v", err)
	}

	tok := provSignRS256(t, key, "test-kid", provClaims("880010", planGrantEnt(nil)))
	code, resp := planGrantReq(t, r, ctx.TenantID, tok)
	if code != http.StatusOK || resp["data"].(map[string]any)["granted"] != true {
		t.Fatalf("want 200 granted, got %d body=%v", code, resp)
	}
	if q, _ := repo.GetUserQuota(uid, true); q != q0+500000 {
		t.Errorf("quota %d -> %d, want +500000", q0, q)
	}
}

// cc_team: every seat reaches the plan through the org's single subscription.
// Each member gets an equal share under its own key; replays stay replays;
// the team total never exceeds the plan amount.
func TestPlanGrantV2_SeatPlanSharesPerMember(t *testing.T) {
	r, ctx, key := setupPlanGrantTest(t)
	team := planGrantEnt(map[string]string{"plan_code": "cc_team", "seats": "5", "newhub_pool_amount": "1000000", "subscription_id": "sub_team"})

	var total int64
	for _, sub := range []string{"880020", "880021"} {
		uid, q0 := planGrantProvisionUser(t, r, ctx, key, sub)
		tok := provSignRS256(t, key, "test-kid", provClaims(sub, team))
		code, resp := planGrantReq(t, r, ctx.TenantID, tok)
		data, _ := resp["data"].(map[string]any)
		if code != http.StatusOK || data["granted"] != true || data["amount"].(float64) != 200000 {
			t.Fatalf("member %s: want 200 granted 200000, got %d body=%v", sub, code, resp)
		}
		q, _ := repo.GetUserQuota(uid, true)
		if q != q0+200000 {
			t.Errorf("member %s quota %d -> %d, want +200000", sub, q0, q)
		}
		total += int64(q - q0)

		// Same member again: a replay, not a second share.
		code, resp = planGrantReq(t, r, ctx.TenantID, tok)
		if code != http.StatusOK || resp["data"].(map[string]any)["replayed"] != true {
			t.Fatalf("member %s replay: got %d body=%v", sub, code, resp)
		}
	}
	if total > 1000000 {
		t.Fatalf("team total %d exceeds the plan amount", total)
	}
}
