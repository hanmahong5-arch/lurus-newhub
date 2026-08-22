package middleware

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

const (
	entitlementCacheTTL = 5 * time.Minute
	// entitlementProduct is the product this gate asks the platform about. It
	// has to be the same id the relay's wallet debit is filed under, or the
	// quota checked here is not the quota being spent — hence the shared
	// constant instead of a second literal.
	entitlementProduct = ratio_setting.DefaultSourceProduct
)

type entitlementEntry struct {
	allowed   bool
	checkedAt time.Time
}

var (
	entitlementCache sync.Map // map[int64]entitlementEntry
)

// EntitlementCheck validates that the caller has remaining quota on the
// platform entitlement system before forwarding to upstream LLM providers.
// Skips gracefully for legacy users without a platform account ID.
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

		// Check local cache first.
		if val, hit := entitlementCache.Load(accountID); hit {
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
		ent, err := common.GetEntitlements(c.Request.Context(), accountID, entitlementProduct)
		if err != nil {
			slog.Warn("entitlement check failed, allowing request",
				"account_id", accountID, "err", err)
			c.Next()
			return
		}

		// quota_remaining: -1 = unlimited (paid plan), 0 = exhausted, >0 = remaining.
		// plan_code: "free" indicates free tier with limited quota.
		quotaRemaining := ent.GetInt("quota_remaining", -1)
		allowed := quotaRemaining != 0

		entitlementCache.Store(accountID, entitlementEntry{
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

func abortQuotaExceeded(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
		"error":       "quota_exceeded",
		"message":     "Your API quota has been exhausted. Please upgrade your plan or top up credits.",
		"upgrade_url": common.IdentityPublicURL + "/pricing",
	})
}
