package handler

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/entverify"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Column bounds of plan_quota_grants (migration 040).
const (
	planGrantKeyMaxLen      = 128
	planGrantPlanCodeMaxLen = 64
)

// planGrantIncreaseQuota is the quota-credit step. A package var only so a
// test can force the failure path; production always uses
// repo.IncreaseUserQuota (which also refreshes the Redis user-quota cache).
var planGrantIncreaseQuota = repo.IncreaseUserQuota

// planGrantError writes the standard v2 error envelope.
func planGrantError(c *gin.Context, status int, code, msg string) {
	c.JSON(status, gin.H{"success": false, "message": msg, "error_code": code})
}

// PlanGrantV2 credits a Claude-Code-class plan's quota onto the buyer's own
// hub user balance.
//
// Route: POST /api/v2/:tenant_slug/plan-grant   (public — the platform
// entitlement token is the credential, same as ProvisionV2; registered with
// BootstrapRateLimit)
//
// WHY: there are two ledgers on the relay hot path. Buying a cc_* plan funds
// the TENANT credit pool, but relay pre-consume
// (internal/app/pre_consume_quota.go) also gates on the buyer's OWN
// users.quota. ProvisionV2 only mints a relay token, so that balance stayed
// at the ~10000-unit welcome grant (zita_bootstrap.go) and a paying buyer got
// 402 after a few requests. This endpoint closes the user-side ledger by
// adding ent.newhub_pool_amount to users.quota.
//
// Idempotency: grant_key = ent.subscription_id + ":" +
// ent.subscription_expires_at — ONE grant per subscription period. A renewal
// or extension changes expires_at, so the next call grants again; replaying
// the same period is answered 200 replayed=true without crediting. The
// UNIQUE(tenant_id, grant_key) index on plan_quota_grants (migration 040) is
// the race-safe guard: the row is inserted BEFORE crediting, and removed
// again if the credit fails so a retry can succeed. A missing
// subscription_id is refused (422 GRANT_KEY_MISSING) — there is deliberately
// no heuristic fallback key, because a wrong key either double-grants or
// never grants.
//
// Request: { "entitlement_token": "<RS256 compact JWS from platform>" }
//
// Response 200: { "success": true, "data": { "granted", "replayed",
// "amount", "grant_key", "new_quota" (only when granted) } }
//
// Errors: 400 INVALID_REQUEST / MISSING_ENTITLEMENT_TOKEN · 401 TOKEN_INVALID
// · 403 AUD_MISMATCH / ENTITLEMENT_STALE / PLAN_NOT_ELIGIBLE /
// TENANT_DISABLED / TENANT_MISMATCH / USER_DISABLED · 404 TENANT_NOT_FOUND ·
// 409 NOT_PROVISIONED (call provision first) · 422 NO_GRANT_AMOUNT /
// GRANT_KEY_MISSING / GRANT_KEY_INVALID · 500 GRANT_FAILED.
func PlanGrantV2(c *gin.Context) {
	tenant, err := repo.GetTenantBySlug(c.Param("tenant_slug"))
	if err != nil || tenant == nil {
		planGrantError(c, http.StatusNotFound, "TENANT_NOT_FOUND", "tenant not found: verify the slug in the URL path")
		return
	}
	if ok, reason := repo.TenantGate(tenant.Id); !ok {
		common.SysLog("PlanGrantV2: refused, tenant gate closed tenant=" + tenant.Id + " reason=" + reason)
		planGrantError(c, http.StatusForbidden, "TENANT_DISABLED", "tenant is disabled or suspended")
		return
	}

	var req struct {
		EntitlementToken string `json:"entitlement_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		planGrantError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body: "+err.Error())
		return
	}
	if req.EntitlementToken == "" {
		planGrantError(c, http.StatusBadRequest, "MISSING_ENTITLEMENT_TOKEN",
			"entitlement_token is required: obtain it from platform GET /api/v1/entitlements/"+entitlementExpectedAud)
		return
	}

	claims, verifyErr := getProvisionVerifier().Verify(c.Request.Context(), req.EntitlementToken, entitlementExpectedAud)
	if verifyErr != nil {
		if errors.Is(verifyErr, entverify.ErrWrongAudience) {
			planGrantError(c, http.StatusForbidden, "AUD_MISMATCH", "entitlement token audience mismatch: "+verifyErr.Error())
			return
		}
		planGrantError(c, http.StatusUnauthorized, "TOKEN_INVALID", "entitlement token rejected: "+verifyErr.Error())
		return
	}
	// Crediting money is not a degrade: a Grace token's entitlement may
	// already be gone platform-side, so only Fresh tokens grant.
	if claims.Freshness == entverify.Grace {
		planGrantError(c, http.StatusForbidden, "ENTITLEMENT_STALE",
			"entitlement token is stale (past its freshness window); obtain a fresh token from platform GET /api/v1/entitlements/"+entitlementExpectedAud)
		return
	}

	planCode := claims.Ent["plan_code"]
	if !strings.HasPrefix(planCode, eligiblePlanPrefix) || len(planCode) > planGrantPlanCodeMaxLen {
		planGrantError(c, http.StatusForbidden, "PLAN_NOT_ELIGIBLE",
			"plan is not eligible for a plan quota grant (requires a "+eligiblePlanPrefix+"* plan); token plan_code="+strconv.Quote(planCode))
		return
	}

	accountID, parseErr := strconv.ParseInt(claims.Sub, 10, 64)
	if parseErr != nil || accountID <= 0 {
		planGrantError(c, http.StatusUnauthorized, "TOKEN_INVALID", "entitlement token sub is not a platform account id")
		return
	}

	user, err := repo.GetUserByLurusAccountID(accountID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		planGrantError(c, http.StatusConflict, "NOT_PROVISIONED",
			"no hub account for this platform account yet; call POST /api/v2/"+tenant.Slug+"/provision first")
		return
	}
	if err != nil {
		common.SysError("PlanGrantV2: user lookup failed account_id=" + strconv.FormatInt(accountID, 10) + " err=" + err.Error())
		planGrantError(c, http.StatusInternalServerError, "GRANT_FAILED", "failed to resolve hub account")
		return
	}
	// Exactly ProvisionV2's rule: a legacy account on the bootstrap tenant
	// ("default"/"") may use any slug. A stricter rule here would let such a
	// buyer provision a key and then be refused the quota that makes it
	// usable — the very 402 this endpoint exists to prevent.
	if provisionTenantIsPinned(user.TenantId) && provisionTenantIsPinned(tenant.Id) && user.TenantId != tenant.Id {
		common.SysLog("PlanGrantV2: refused, account belongs to another tenant" +
			" url_tenant=" + tenant.Id + " user_tenant=" + user.TenantId +
			" account_id=" + strconv.FormatInt(accountID, 10))
		planGrantError(c, http.StatusForbidden, "TENANT_MISMATCH",
			"this platform account belongs to another tenant; grant through that tenant's slug")
		return
	}
	if user.Status != common.UserStatusEnabled {
		planGrantError(c, http.StatusForbidden, "USER_DISABLED", "hub account is disabled")
		return
	}

	amount, amtErr := strconv.ParseInt(strings.TrimSpace(claims.Ent["newhub_pool_amount"]), 10, 64)
	if amtErr != nil || amount <= 0 || amount > int64(math.MaxInt) {
		planGrantError(c, http.StatusUnprocessableEntity, "NO_GRANT_AMOUNT",
			"entitlement carries no positive newhub_pool_amount; nothing to grant")
		return
	}

	subscriptionID := strings.TrimSpace(claims.Ent["subscription_id"])
	if subscriptionID == "" {
		planGrantError(c, http.StatusUnprocessableEntity, "GRANT_KEY_MISSING",
			"entitlement carries no subscription_id; cannot derive an idempotent grant key")
		return
	}
	grantKey := subscriptionID + ":" + strings.TrimSpace(claims.Ent["subscription_expires_at"])

	// Seat plans (cc_team: "seats":5) reach every member through the org's
	// ONE subscription, so the key above is shared. Keyed only on it, the
	// first member to call would take the whole team amount and every other
	// seat would be answered "replayed" with nothing — still 402. Each member
	// gets an equal share under a per-account key instead; the team total
	// never exceeds the plan amount.
	if seats, sErr := strconv.ParseInt(strings.TrimSpace(claims.Ent["seats"]), 10, 64); sErr == nil && seats > 1 {
		amount /= seats
		grantKey += ":" + strconv.FormatInt(accountID, 10)
		if amount <= 0 {
			planGrantError(c, http.StatusUnprocessableEntity, "NO_GRANT_AMOUNT",
				"plan amount is too small to share across its seats")
			return
		}
	}
	if len(grantKey) > planGrantKeyMaxLen {
		planGrantError(c, http.StatusUnprocessableEntity, "GRANT_KEY_INVALID",
			"subscription_id/subscription_expires_at too long for a grant key")
		return
	}

	row := &entity.PlanQuotaGrant{
		TenantId:  tenant.Id,
		AccountId: accountID,
		UserId:    user.Id,
		GrantKey:  grantKey,
		PlanCode:  planCode,
		Amount:    amount,
		CreatedAt: common.GetTimestamp(),
	}
	if insErr := repo.InsertPlanQuotaGrant(row); insErr != nil {
		if errors.Is(insErr, repo.ErrPlanQuotaGrantExists) {
			common.SysLog("PlanGrantV2: replayed tenant=" + tenant.Id +
				" account_id=" + strconv.FormatInt(accountID, 10) +
				" user_id=" + strconv.Itoa(user.Id) +
				" key=" + grantKey + " amount=" + strconv.FormatInt(amount, 10))
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"data": gin.H{
					"granted":   false,
					"replayed":  true,
					"amount":    amount,
					"grant_key": grantKey,
				},
			})
			return
		}
		common.SysError("PlanGrantV2: grant row insert failed user_id=" + strconv.Itoa(user.Id) + " err=" + insErr.Error())
		planGrantError(c, http.StatusInternalServerError, "GRANT_FAILED", "failed to record plan grant")
		return
	}

	if incErr := planGrantIncreaseQuota(user.Id, int(amount), true); incErr != nil {
		common.SysError("PlanGrantV2: quota credit failed user_id=" + strconv.Itoa(user.Id) +
			" key=" + grantKey + " err=" + incErr.Error())
		if delErr := repo.DeletePlanQuotaGrant(row.Id); delErr != nil {
			// The row stays, so a retry would be reported as a replay without
			// the credit ever landing — must be reconciled by hand.
			common.SysError("PlanGrantV2: CRITICAL grant row rollback failed, manual reconcile needed" +
				" grant_id=" + strconv.FormatInt(row.Id, 10) + " user_id=" + strconv.Itoa(user.Id) +
				" key=" + grantKey + " err=" + delErr.Error())
		}
		planGrantError(c, http.StatusInternalServerError, "GRANT_FAILED", "failed to credit plan quota; retry is safe")
		return
	}

	newQuota, qErr := repo.GetUserQuota(user.Id, true)
	if qErr != nil {
		common.SysError("PlanGrantV2: post-grant quota read failed user_id=" + strconv.Itoa(user.Id) + " err=" + qErr.Error())
	}
	common.SysLog("PlanGrantV2: granted tenant=" + tenant.Id +
		" account_id=" + strconv.FormatInt(accountID, 10) +
		" user_id=" + strconv.Itoa(user.Id) +
		" plan=" + planCode +
		" key=" + grantKey + " amount=" + strconv.FormatInt(amount, 10))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"granted":   true,
			"replayed":  false,
			"amount":    amount,
			"grant_key": grantKey,
			"new_quota": newQuota,
		},
	})
}
