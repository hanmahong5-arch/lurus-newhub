package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/LurusTech/lurus-hub/internal/pkg/entverify"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// P4 unified provisioning: newhub is the FIRST consumer of platform
// entitlement tokens. A desktop client (switch) presents the RS256 token it
// obtained from platform GET /api/v1/entitlements/llm-api; newhub verifies it
// OFFLINE against the platform JWKS (zero platform round-trip per request,
// only a cached JWKS fetch), finds-or-creates the bridged hub user by the
// token subject (platform account id) and hands back a bounded relay token.
const (
	// defaultPlatformJWKSURL is overridden by env PLATFORM_JWKS_URL.
	defaultPlatformJWKSURL = "https://identity.lurus.cn/api/v1/entitlements/jwks"
	// entitlementExpectedAud: platform mints aud = CANONICAL product_id
	// (entitlement_token_handler.go), and newhub's product id is "llm-api" —
	// NOT the tenant slug.
	entitlementExpectedAud = "llm-api"
	// eligiblePlanPrefix gates provisioning to Claude-Code-class plans.
	eligiblePlanPrefix = "cc_"
	// defaultRelayBaseURL is overridden by env RELAY_PUBLIC_BASE_URL.
	defaultRelayBaseURL = "https://hub.lurus.cn"
	// provisionTokenNamePrefix + plan_code is the relay token name and the
	// per-user idempotency key of this endpoint.
	provisionTokenNamePrefix = "switch-provision-"
)

var (
	provisionVerifierMu sync.Mutex
	provisionVerifier   *entverify.Verifier // lazily built; tests inject via setProvisionVerifier
)

// getProvisionVerifier returns the shared entitlement verifier (JWKS cached
// in-process for its TTL; *entverify.Verifier is safe for concurrent use).
func getProvisionVerifier() *entverify.Verifier {
	provisionVerifierMu.Lock()
	defer provisionVerifierMu.Unlock()
	if provisionVerifier == nil {
		jwksURL := os.Getenv("PLATFORM_JWKS_URL")
		if jwksURL == "" {
			jwksURL = defaultPlatformJWKSURL
		}
		provisionVerifier = entverify.New(jwksURL)
	}
	return provisionVerifier
}

// setProvisionVerifier swaps the package verifier. Tests only — lets a test
// point verification at a local JWKS stub (entverify.WithInsecureJWKS).
func setProvisionVerifier(v *entverify.Verifier) {
	provisionVerifierMu.Lock()
	defer provisionVerifierMu.Unlock()
	provisionVerifier = v
}

// ProvisionV2 exchanges a platform entitlement token for a hub relay token.
//
// Route: POST /api/v2/:tenant_slug/provision   (public — the entitlement
// token itself is the credential; registered with BootstrapRateLimit, the
// same bucket as the structurally identical zita-bootstrap exchange)
//
// Request body (JSON):
//
//	{
//	  "entitlement_token": "<RS256 compact JWS from platform>",  // required
//	  "fingerprint":       "<device fingerprint>"                 // optional, log/audit only
//	}
//
// Flow: offline-verify signature+aud+freshness (entverify; a token inside its
// 72h offline grace still verifies at the LIBRARY level, but this endpoint
// additionally requires Freshness==Fresh before acting on it — see
// ENTITLEMENT_STALE below), gate plan_code on the cc_ prefix, find-or-create
// the hub user by sub (platform account id, same path as zita-bootstrap),
// then mint, refresh, or (on replay) return the user's
// "switch-provision-<plan_code>" relay token. An existing, unexpired token is
// replayed as-is; an existing token that has merely aged past its own expiry
// is refreshed in place (remain_quota/used_quota carry over — a net
// reconcile, not a refill); a same-named token that is Disabled (an admin
// deliberately took it off Enabled) is never resurrected — see TOKEN_REVOKED
// below.
// Quota: ent.quota (fallback ent.amount) > 0 → bounded token; absent →
// unlimited_quota=true, i.e. spend is bounded by the USER balance instead
// (platform funds it separately). The minted/refreshed relay token's own
// expiry is forward-looking from the entitlement's exp (exp + the entverify
// offline-grace margin, never the raw claim verbatim — never immortal, never
// in the past), so an unrenewed plan stops working on its own.
//
// Response 200:
//
//	{ "success": true, "data": { "user_id", "token": "sk-...", "base_url",
//	  "plan_code", "replayed" } }
//
// Errors: 400 MISSING_ENTITLEMENT_TOKEN · 401 TOKEN_INVALID (bad signature /
// malformed / past exp+grace / keys unavailable / non-numeric sub) ·
// 403 AUD_MISMATCH · 403 ENTITLEMENT_STALE (token verified but
// Freshness==Grace, i.e. past its hard exp) · 403 PLAN_NOT_ELIGIBLE ·
// 403 USER_DISABLED · 403 TOKEN_REVOKED (same-named relay token exists but
// was administratively disabled) · 404 TENANT_NOT_FOUND.
func ProvisionV2(c *gin.Context) {
	slug := c.Param("tenant_slug")
	tenant, err := repo.GetTenantBySlug(slug)
	if err != nil || tenant == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "tenant not found: verify the slug in the URL path",
			"error_code": "TENANT_NOT_FOUND",
		})
		return
	}

	var req struct {
		EntitlementToken string `json:"entitlement_token"`
		Fingerprint      string `json:"fingerprint"` // log/audit only
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "invalid request body: " + err.Error(),
			"error_code": "INVALID_REQUEST",
		})
		return
	}
	if req.EntitlementToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "entitlement_token is required: obtain it from platform GET /api/v1/entitlements/" + entitlementExpectedAud,
			"error_code": "MISSING_ENTITLEMENT_TOKEN",
		})
		return
	}

	claims, verifyErr := getProvisionVerifier().Verify(c.Request.Context(), req.EntitlementToken, entitlementExpectedAud)
	if verifyErr != nil {
		if errors.Is(verifyErr, entverify.ErrWrongAudience) {
			c.JSON(http.StatusForbidden, gin.H{
				"success":    false,
				"message":    "entitlement token audience mismatch: " + verifyErr.Error(),
				"error_code": "AUD_MISMATCH",
			})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":    false,
			"message":    "entitlement token rejected: " + verifyErr.Error(),
			"error_code": "TOKEN_INVALID",
		})
		return
	}
	// A Grace-freshness token verified (signature/aud/exp+72h all check out —
	// entverify intentionally still returns it so a caller CAN degrade), but
	// minting or renewing a relay token is not a degrade: the platform-side
	// entitlement behind a stale claim may already be gone (plan cancelled,
	// seat revoked) and this endpoint has no way to tell from the token
	// alone. Bounded staleness means Grace is hard-rejected here — the caller
	// must fetch a Fresh token from platform before provisioning proceeds.
	if claims.Freshness == entverify.Grace {
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "entitlement token is stale (past its freshness window); obtain a fresh token from platform GET /api/v1/entitlements/" + entitlementExpectedAud,
			"error_code": "ENTITLEMENT_STALE",
		})
		return
	}

	planCode := claims.Ent["plan_code"]
	tokenName := provisionTokenNamePrefix + planCode
	// The name-length check doubles as the plan_code sanity bound (token name
	// is the idempotency key — it must round-trip through the tokens table).
	if !strings.HasPrefix(planCode, eligiblePlanPrefix) || app.ValidateTokenName(tokenName) != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "plan is not eligible for unified provisioning (requires a " + eligiblePlanPrefix + "* plan); token plan_code=" + strconv.Quote(planCode),
			"error_code": "PLAN_NOT_ELIGIBLE",
		})
		return
	}

	// sub carries the platform account id as a decimal string
	// (entitlement_token_handler.go mints strconv.FormatInt(accountID, 10)).
	accountID, parseErr := strconv.ParseInt(claims.Sub, 10, 64)
	if parseErr != nil || accountID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":    false,
			"message":    "entitlement token sub is not a platform account id",
			"error_code": "TOKEN_INVALID",
		})
		return
	}

	// Find-or-create the bridged hub user — the SAME path zita-bootstrap uses
	// (lurus_account_id is unique, so one platform account maps to one hub user
	// regardless of which tenant slug the client called).
	user, err := repo.GetUserByLurusAccountID(accountID)
	autoCreated := false
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// "default" preserves this endpoint's existing behavior exactly —
		// tenant invite consumption (N2) is wired into ZitaBootstrap's
		// auto-create branch only; ProvisionV2 already resolves `tenant`
		// from :tenant_slug above but that is a pre-existing, separately
		// tracked gap (recon N2 evidence #2), out of scope here.
		user, err = autoCreateBridgedUser(accountID, "default")
		if err != nil {
			common.SysError("ProvisionV2: auto-create user failed" +
				" account_id=" + strconv.FormatInt(accountID, 10) + " err=" + err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{
				"success":    false,
				"message":    "failed to provision hub account",
				"error_code": "PROVISION_FAILED",
			})
			return
		}
		autoCreated = true
	} else if err != nil {
		common.SysError("ProvisionV2: user lookup failed" +
			" account_id=" + strconv.FormatInt(accountID, 10) + " err=" + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "failed to resolve hub account",
			"error_code": "PROVISION_FAILED",
		})
		return
	}
	if user.Status != common.UserStatusEnabled {
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "hub account is disabled",
			"error_code": "USER_DISABLED",
		})
		return
	}

	baseURL := os.Getenv("RELAY_PUBLIC_BASE_URL")
	if baseURL == "" {
		baseURL = defaultRelayBaseURL
	}

	// Relay-token expiry is forward-looking from the entitlement's own exp,
	// never copied verbatim: entverify grades a token Fresh for the WHOLE
	// window exp <= now < exp+skew, so `exp` itself can already be in the past
	// at mint/refresh time — copying it verbatim would issue a
	// dead-on-arrival credential (rejected by the very first relay call).
	// Anchoring at exp + the offline grace the entitlement contract already
	// promises covers that window; the floor is a last-resort guard against a
	// non-positive TTL.
	expiredAt := claims.ExpiresAt.Add(entverify.DefaultGrace).Unix()
	if expiredAt <= common.GetTimestamp() {
		expiredAt = common.GetTimestamp() + int64(entverify.DefaultGrace/time.Second)
	}

	// Idempotency: one live relay token per (user, plan). An existing enabled,
	// unexpired token with this name is returned as-is — no duplicate mint.
	var existing repo.Token
	findErr := repo.DB.
		Where("user_id = ? AND name = ?", user.Id, tokenName).
		Order("id desc").First(&existing).Error
	if findErr == nil && existing.Status == common.TokenStatusEnabled &&
		(existing.ExpiredTime == -1 || existing.ExpiredTime > common.GetTimestamp()) {
		common.SysLog("ProvisionV2: replayed" +
			" tenant=" + tenant.Id +
			" user_id=" + strconv.Itoa(user.Id) +
			" plan=" + planCode +
			" freshness=" + claims.Freshness.String() +
			" fingerprint=" + req.Fingerprint)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"user_id":   user.Id,
				"token":     "sk-" + existing.Key,
				"base_url":  baseURL,
				"plan_code": planCode,
				"replayed":  true,
			},
		})
		return
	}
	// Expired-but-Enabled: the entitlement mints on a fixed 24h cadence, so a
	// row that merely aged past its own expired_time while Status stayed
	// Enabled (the Redis-on production shape — the auto-expire status write
	// in repo/token.go never fires) is a ROUTINE re-provision, not a rare
	// edge case. ent.quota is a static plan attribute that does not shrink
	// with spend, so minting a brand-new row here would refill
	// remain_quota/used_quota to the full plan quota every cycle — a monthly
	// cap becomes a daily one. Refresh the EXISTING row's expiry in place
	// instead: remain_quota/used_quota are left untouched, a net reconcile
	// rather than an increment.
	if findErr == nil && existing.Status == common.TokenStatusEnabled {
		if updErr := repo.DB.Model(&existing).Updates(map[string]any{
			"expired_time":  expiredAt,
			"accessed_time": common.GetTimestamp(),
		}).Error; updErr != nil {
			common.SysError("ProvisionV2: token refresh failed" +
				" user_id=" + strconv.Itoa(user.Id) + " err=" + updErr.Error())
			c.JSON(http.StatusInternalServerError, gin.H{
				"success":    false,
				"message":    "failed to refresh relay token",
				"error_code": "PROVISION_FAILED",
			})
			return
		}
		common.SysLog("ProvisionV2: refreshed expired token" +
			" tenant=" + tenant.Id +
			" user_id=" + strconv.Itoa(user.Id) +
			" plan=" + planCode +
			" freshness=" + claims.Freshness.String() +
			" fingerprint=" + req.Fingerprint)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"user_id":   user.Id,
				"token":     "sk-" + existing.Key,
				"base_url":  baseURL,
				"plan_code": planCode,
				"replayed":  true,
			},
		})
		return
	}
	// Revocation semantics: a same-named token that is Disabled was
	// administratively taken off Enabled by a human. Falling through to mint
	// a fresh Enabled row under the same idempotency name would silently undo
	// that revocation on the client's next routine provision call — so
	// re-provisioning is refused outright instead. Expired(3)/Exhausted(4)
	// are AUTOMATIC transitions repo/token.go itself makes with no human
	// involved and must NOT be treated as revocation — excluding them here
	// lets those rows fall through to a fresh mint below instead of
	// hard-locking the account forever.
	if findErr == nil && existing.Status == common.TokenStatusDisabled {
		common.SysLog("ProvisionV2: re-provision denied, token revoked" +
			" tenant=" + tenant.Id +
			" user_id=" + strconv.Itoa(user.Id) +
			" plan=" + planCode +
			" existing_token_id=" + strconv.Itoa(existing.Id))
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "relay token for this plan was revoked; contact the tenant admin to re-enable it",
			"error_code": "TOKEN_REVOKED",
		})
		return
	}

	// Quota mapping: a positive integer ent.quota (fallback ent.amount) bounds
	// the token; otherwise the token is unlimited_quota and spend is bounded by
	// the user's balance (the safe default — a fresh bridged user has 0 quota
	// until platform funds it).
	remainQuota := 0
	unlimited := true
	quotaStr := claims.Ent["quota"]
	if quotaStr == "" {
		quotaStr = claims.Ent["amount"]
	}
	if quotaStr != "" {
		if q, qErr := strconv.ParseInt(quotaStr, 10, 64); qErr == nil && q > 0 {
			remainQuota = int(q)
			unlimited = false
		}
	}

	key, keyErr := app.GenerateTokenKey()
	if keyErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "failed to generate relay token key",
			"error_code": "PROVISION_FAILED",
		})
		return
	}

	token := repo.Token{
		UserId:         user.Id,
		TenantId:       user.TenantId,
		Name:           tokenName,
		Key:            key,
		Status:         common.TokenStatusEnabled,
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    expiredAt, // forward-looking: entitlement exp + offline grace, floored at now+grace — never immortal, never <= now
		RemainQuota:    remainQuota,
		UnlimitedQuota: unlimited,
		Group:          "default",
	}
	if insertErr := token.Insert(); insertErr != nil {
		common.SysError("ProvisionV2: token insert failed" +
			" user_id=" + strconv.Itoa(user.Id) + " err=" + insertErr.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "failed to create relay token",
			"error_code": "PROVISION_FAILED",
		})
		return
	}

	details, _ := json.Marshal(map[string]any{
		"source":       "switch-provision",
		"plan_code":    planCode,
		"fingerprint":  req.Fingerprint,
		"auto_created": autoCreated,
	})
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, user.Id,
		governance.ActionTokenCreated, governance.ResourceToken, token.Id, string(details)))

	common.SysLog("ProvisionV2: provisioned" +
		" tenant=" + tenant.Id +
		" user_id=" + strconv.Itoa(user.Id) +
		" auto_created=" + strconv.FormatBool(autoCreated) +
		" plan=" + planCode +
		" unlimited=" + strconv.FormatBool(unlimited) +
		" quota=" + strconv.Itoa(remainQuota) +
		" freshness=" + claims.Freshness.String() +
		" fingerprint=" + req.Fingerprint)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"user_id":   user.Id,
			"token":     "sk-" + key,
			"base_url":  baseURL,
			"plan_code": planCode,
			"replayed":  false,
		},
	})
}
