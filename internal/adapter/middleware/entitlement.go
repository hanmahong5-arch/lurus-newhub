package middleware

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"github.com/gin-gonic/gin"
)

const entitlementCacheTTL = 5 * time.Minute

type entitlementEntry struct {
	allowed   bool
	checkedAt time.Time
}

// entitlementCacheKey is (account, product): the entitlement decision is
// per-product (a "kova" quota and a "llm-api" quota on the same account are
// unrelated), so a single account-keyed cache would let a deny decision for
// one product silently gate an unrelated one, or vice versa.
type entitlementCacheKey struct {
	accountID int64
	product   string
}

var entitlementCache sync.Map // map[entitlementCacheKey]entitlementEntry

// EntitlementCheck validates that the caller has remaining quota on the
// platform entitlement system before forwarding to upstream LLM providers.
// Skips gracefully for legacy users without a platform account ID.
//
// The product it asks the platform about is resolved PER REQUEST from
// X-Lurus-Product (same allow-list and fallback as the relay's wallet debit,
// ratio_setting.ResolveSourceProduct) — it has to be the same id the debit is
// filed under, or the quota checked here is not the quota being spent.
func EntitlementCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, exists := c.Get("identity_account_id")
		if !exists {
			// Legacy auth (session cookie / API key) — no platform account.
			// Quota enforced by lurus-api's own pre_consume_quota system.
			c.Next()
			return
		}
		accountID, ok := raw.(int64)
		if !ok || accountID <= 0 {
			c.Next()
			return
		}

		product := ratio_setting.ResolveSourceProduct(c.GetHeader(ratio_setting.SourceProductHeader))
		key := entitlementCacheKey{accountID: accountID, product: product}

		// Check local cache first.
		if val, hit := entitlementCache.Load(key); hit {
			entry := val.(entitlementEntry)
			if time.Since(entry.checkedAt) < entitlementCacheTTL {
				if !entry.allowed {
					abortQuotaExceeded(c)
					return
				}
				c.Next()
				return
			}
		}

		// Call platform entitlement API.
		ent, err := common.GetEntitlements(c.Request.Context(), accountID, product)
		if err != nil {
			slog.Warn("entitlement check failed, allowing request",
				"account_id", accountID, "product", product, "err", err)
			c.Next()
			return
		}

		// quota_remaining: -1 = unlimited (paid plan), 0 = exhausted, >0 = remaining.
		// plan_code: "free" indicates free tier with limited quota.
		quotaRemaining := ent.GetInt("quota_remaining", -1)
		allowed := quotaRemaining != 0

		entitlementCache.Store(key, entitlementEntry{
			allowed:   allowed,
			checkedAt: time.Now(),
		})

		if !allowed {
			abortQuotaExceeded(c)
			return
		}
		c.Next()
	}
}

// abortQuotaExceeded rejects in the caller's own wire shape (renderRejection)
// instead of always answering OpenAI's — a Claude/Gemini SDK hitting this
// gate must see its own error envelope, same as every other relay-stage
// rejection: pool_balance_check.go calls renderRejection directly;
// tenant_quota.go (internal/app) never calls it itself — it returns a
// *types.NewAPIError that bubbles up through preConsumeQuota and is rendered
// by the per-wire switch in handler.Relay's deferred error handler, the same
// switch renderRejection mirrors. The upgrade_url rides error.metadata
// (ErrOptionWithUpgradeURL) rather than a bare top-level field, so it
// survives MaskSensitiveInfo and matches the shape clients already parse for
// other 402/429 upgrade prompts.
func abortQuotaExceeded(c *gin.Context) {
	apiErr := types.WithOpenAIError(types.OpenAIError{
		// "quota_exceeded" appears in the message text (not just Code) because
		// the Gemini/Claude wire envelopes (types.ToGeminiError/ToClaudeError)
		// carry Message but drop Code — a caller on either of those wires must
		// still be able to match on the reason string.
		Message: "Your API quota has been exhausted (quota_exceeded). Please upgrade your plan or top up credits.",
		Type:    "new_api_error",
		Code:    "quota_exceeded",
	}, http.StatusTooManyRequests, types.ErrOptionWithUpgradeURL())
	renderRejection(c, apiErr)
	c.Abort()
}
