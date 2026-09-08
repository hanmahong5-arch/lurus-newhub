package handler

// GET /v1/key — introspection for a product that holds only a relay key: its
// own limits, remaining allowance, rate limits and tenant pool state from one
// call, without console access. Mirrors OpenRouter's /api/v1/key. Read-only,
// no flag — documented in doc/product-integration-guide.md §F (key-holder
// endpoints).
//
// Route: GET /v1/key (dashboard.go's TokenAuth group). Deliberately separate
// from switch_user_info.go's GetSwitchUserInfo: that endpoint authenticates
// via a raw Authorization key lookup for one specific product's own wire
// contract (kept byte-identical to lurus-switch's client) and must not be
// touched here; this one rides the standard TokenAuth context instead.

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

type keyRateLimitView struct {
	RPM       int `json:"rpm"`
	TPM       int `json:"tpm"`
	TenantRPM int `json:"tenant_rpm"`
	TenantTPM int `json:"tenant_tpm"`
}

type keyPoolView struct {
	Balance    int64  `json:"balance"`
	MaxBalance int64  `json:"max_balance"`
	Health     string `json:"health"`
}

// keyInfoView follows the OpenRouter /api/v1/key vocabulary: limit is the
// token's total cap (remaining + already used; null when unlimited),
// limit_remaining is what is left to spend (null when unlimited), usage is
// what has been spent so far. All three are quota integers (the DB unit),
// not USD — /v1/generation's total_cost is the only USD-denominated field
// this lane exposes.
type keyInfoView struct {
	Label          string           `json:"label"`
	TenantId       string           `json:"tenant_id"`
	ProjectId      int              `json:"project_id"`
	Group          string           `json:"group"`
	Scopes         []string         `json:"scopes"`
	ModelLimits    []string         `json:"model_limits"`
	Limit          *int             `json:"limit"`
	LimitRemaining *int             `json:"limit_remaining"`
	Usage          int              `json:"usage"`
	ExpiresAt      *int64           `json:"expires_at"`
	RateLimit      keyRateLimitView `json:"rate_limit"`
	Pool           *keyPoolView     `json:"pool"`
	DefaultProduct string           `json:"default_product"`
}

// GetKeyInfo handles GET /v1/key.
func GetKeyInfo(c *gin.Context) {
	tokenID := c.GetInt("token_id")
	token, err := repo.GetTokenById(tokenID)
	if err != nil || token == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "token not found"})
		return
	}

	// limit is the total cap (remaining + already used), not the naked
	// remaining number — OpenRouter's vocabulary: limit_remaining is what
	// is left, limit is what the key was granted in total.
	var limit *int
	var limitRemaining *int
	if !token.UnlimitedQuota {
		total := token.RemainQuota + token.UsedQuota
		limit = &total
		remaining := token.RemainQuota
		limitRemaining = &remaining
	}
	var expiresAt *int64
	if token.ExpiredTime != -1 {
		t := token.ExpiredTime
		expiresAt = &t
	}

	tenantID := token.TenantId
	if tenantID == "" {
		tenantID = "default"
	}
	rateLimit := keyRateLimitView{RPM: token.RateLimitRPM, TPM: token.RateLimitTPM}
	if tenant, tErr := repo.GetTenantByID(tenantID); tErr == nil && tenant != nil {
		rateLimit.TenantRPM = tenant.RateLimitRPM
		rateLimit.TenantTPM = tenant.RateLimitTPM
	}

	var pool *keyPoolView
	if p, pErr := repo.GetTenantCreditPool(tenantID); pErr == nil && p != nil {
		pool = &keyPoolView{
			Balance:    p.CurrentBalance,
			MaxBalance: p.MaxBalance,
			Health:     poolHealthForEndUser(p),
		}
	}
	// repo.ErrPoolNotFound (and any other lookup failure) leaves pool nil —
	// the relay gate's own convention: absence of a pool row means
	// unlimited/not-gated, not an error to surface to the caller.

	// GetScopes returns nil (not []) when the token has no scope
	// allowlist — that nil is meaningful to HasScope's "no restriction"
	// check but must not leak into the JSON response as `null` next to
	// model_limits' `[]`; normalise both to an empty array here.
	scopes := token.GetScopes()
	if scopes == nil {
		scopes = []string{}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": keyInfoView{
			Label:          token.Name,
			TenantId:       tenantID,
			ProjectId:      token.ProjectId,
			Group:          token.Group,
			Scopes:         scopes,
			ModelLimits:    token.GetModelLimits(),
			Limit:          limit,
			LimitRemaining: limitRemaining,
			Usage:          token.UsedQuota,
			ExpiresAt:      expiresAt,
			RateLimit:      rateLimit,
			Pool:           pool,
			DefaultProduct: ratio_setting.DefaultSourceProduct,
		},
	})
}
