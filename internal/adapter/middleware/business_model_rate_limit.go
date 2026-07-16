package middleware

import (
	"errors"
	"fmt"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Business model rate limiting — the (tenant, model) dimension of the
// business rate limiter (business_rate_limit.go holds the token and tenant
// dimensions and the shared window/reject plumbing this file reuses).
//
// Limits come from model_rate_limits rows (migration 026), matched by EXACT
// client-requested model name; a missing row means no per-model limit and
// 0 = unlimited per dimension, exactly like the 023 columns.
//
// Position in chain: AFTER Distribute — unlike the token/tenant dimensions
// (whose keys TokenAuth already established), the requested model name only
// enters the gin context when Distribute parses the request body
// (constant.ContextKeyOriginalModel). Rejecting after channel selection
// wastes a little work on the rare rejected request; parsing the body a
// second time before Distribute would tax every admitted one.
//
// Same fail-open contract as the other dimensions: lookup or window-backend
// errors log and admit, never block legitimate traffic on infra hiccups.

const (
	bizModelKeyPrefix = "rl:biz:model:"
	// bizTenantIDContextKey carries the token's tenant id from
	// BusinessRateLimit (which resolves it before Distribute) to this
	// middleware, saving a repeat token→tenant SELECT per request.
	bizTenantIDContextKey = "biz_rl_tenant_id"
)

// fetchModelBizLimits is a seam for hermetic tests, like fetchTokenBizLimits.
var fetchModelBizLimits = fetchModelBizLimitsFromDB

// fetchModelBizLimitsFromDB reads the (tenant, model) limit row via the
// uk_model_rate_limits_tenant_model unique index. A missing row means "no
// per-model limit" — not an error.
func fetchModelBizLimitsFromDB(tenantID, model string) (bizRateLimits, error) {
	if repo.DB == nil {
		return bizRateLimits{}, errors.New("db not initialized")
	}
	var row struct {
		RpmLimit int
		TpmLimit int
	}
	err := repo.DB.Model(&entity.ModelRateLimit{}).
		Select("rpm_limit", "tpm_limit").
		Where("tenant_id = ? AND model = ?", tenantID, model).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return bizRateLimits{}, nil
	}
	if err != nil {
		return bizRateLimits{}, err
	}
	return bizRateLimits{RPM: row.RpmLimit, TPM: row.TpmLimit}, nil
}

// bizModelTenantID resolves the request's tenant: the stash BusinessRateLimit
// left earlier in the chain when it ran, else the token row (routes that wire
// this middleware without BusinessRateLimit, or a stash lost to its fail-open
// path). "" means unattributable — the caller skips the model dimension.
func bizModelTenantID(c *gin.Context, tokenID int) string {
	if v, ok := c.Get(bizTenantIDContextKey); ok {
		s, _ := v.(string)
		return s
	}
	_, tenantID, err := fetchTokenBizLimits(tokenID)
	if err != nil {
		common.SysError(fmt.Sprintf("business model rate limit: token %d tenant lookup failed, failing open: %s", tokenID, err.Error()))
		return ""
	}
	return tenantID
}

// BusinessModelRateLimit enforces the tenant's per-model RPM and TPM sliding
// windows on the relay chain. Requests without an authenticated token or a
// parsed model name pass through untouched; limit-lookup failures fail open.
func BusinessModelRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenID := c.GetInt("token_id")
		model := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
		if tokenID == 0 || model == "" {
			c.Next()
			return
		}
		tenantID := bizModelTenantID(c, tokenID)
		if tenantID == "" {
			c.Next()
			return
		}

		limits, err := fetchModelBizLimits(tenantID, model)
		if err != nil {
			common.SysError(fmt.Sprintf("business model rate limit: (%s, %s) limit lookup failed, failing open: %s", tenantID, model, err.Error()))
			c.Next()
			return
		}

		// RPM is checked (and its admission recorded) BEFORE TPM, mirroring the
		// token/tenant dimensions in business_rate_limit.go. Consequence: a
		// request admitted past RPM but then rejected by TPM has still consumed
		// its RPM slot for the window — sustained TPM overage nibbles the RPM
		// budget too. This is deliberate and uniform across all three
		// dimensions (RPM = "requests that passed the RPM gate this minute");
		// decoupling the two would need a two-phase check-all-then-commit
		// restructure of the whole limiter, out of scope here.
		//
		// Key = prefix + tenantID + ":" + model. The model name is
		// client-controlled and unescaped, but tenant IDs are system-generated
		// ('default' or "tenant-"+timestamp — see repo.GenerateID) and never
		// contain ':', so the first ':' after the prefix unambiguously bounds
		// the tenant; no cross-(tenant,model) key collision is reachable.
		if limits.RPM > 0 {
			allowed, retryAfter := bizAllow(c, bizModelKeyPrefix+tenantID+":"+model, limits.RPM)
			if !allowed {
				bizReject(c, "model", "rpm", limits.RPM, retryAfter)
				return
			}
		}
		if limits.TPM > 0 {
			total, oldestMs, qerr := app.QueryBusinessTPMModelWindow(c.Request.Context(), tenantID, model)
			if !bizTPMAdmit(c, "model", limits.TPM, total, oldestMs, qerr) {
				return
			}
		}

		c.Next()
	}
}
