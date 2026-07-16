package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/LurusTech/lurus-hub/internal/pkg/entverify"
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
// 72h offline grace still verifies — bounded staleness is the Track B
// contract), gate plan_code on the cc_ prefix, find-or-create the hub user by
// sub (platform account id, same path as zita-bootstrap), then mint — or, on
// replay, return — the user's "switch-provision-<plan_code>" relay token.
// Quota: ent.quota (fallback ent.amount) > 0 → bounded token; absent →
// unlimited_quota=true, i.e. spend is bounded by the USER balance instead
// (platform funds it separately).
//
// Response 200:
//
//	{ "success": true, "data": { "user_id", "token": "sk-...", "base_url",
//	  "plan_code", "replayed" } }
//
// Errors: 400 MISSING_ENTITLEMENT_TOKEN · 401 TOKEN_INVALID (bad signature /
// malformed / past exp+grace / keys unavailable / non-numeric sub) ·
// 403 AUD_MISMATCH · 403 PLAN_NOT_ELIGIBLE · 403 USER_DISABLED ·
// 404 TENANT_NOT_FOUND.
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
		user, err = autoCreateBridgedUser(accountID)
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
		ExpiredTime:    -1, // plan lifecycle is platform-side; the next provision refresh reconciles
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
