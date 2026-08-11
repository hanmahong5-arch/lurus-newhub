package middleware

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting"

	"github.com/gin-gonic/gin"
)

// PoolBalanceCheck is the relay-side gate that enforces tenant credit pool
// limits before any upstream provider call. It runs after TokenAuth (so the
// tenant is identified) and before CostSpikeLimit / Distribute (so an
// exhausted pool short-circuits the rest of the relay chain).
//
// Behaviour (ADR 2026-05-18 (tenant-credit-pool) §5 enforcement order):
//
//	no pool row        → gated by CREDIT_POOL_REQUIRED (setting.GetCreditPoolRequired):
//	                       off (default) → bypass, byte-identical to the
//	                         original back-compat default (treated as unlimited)
//	                       log           → bypass + counter + structured log,
//	                         so ops can size the blast radius pre-rollout
//	                       enforce       → HTTP 402 pool_not_configured, abort chain
//	unlimited pool     → bypass (MaxBalance == -1 sentinel)
//	exhausted pool     → HTTP 402 with structured body, abort chain
//	any DB error       → log and bypass (fail-open; don't break traffic on
//	                     transient repo issues — schema dedup at debit time
//	                     remains the safety net for over-consumption).
//	                     Deliberate residual: this stays fail-open even when
//	                     CREDIT_POOL_REQUIRED=enforce — a DB blip must not 402
//	                     the entire tenant base just because the pool-required
//	                     rollout is on; only a genuinely-absent pool row is
//	                     enforced.
//
// Position in chain: AFTER TokenAuth, BEFORE CostSpikeLimit. Inserted on
// every relay group that can spend tenant credit: /v1 (chat), /mj
// (+ /:mode/mj), /suno, /v1/audio (music), /v1beta (Gemini), /v1/video +
// /v1/videos (OpenAI video), /kling/v1, /jimeng. Video groups were added
// 2026-05-19 after the Phase 2 self-audit found the original ADR §3.1
// list of "5 relay groups" omitted video — a cash-path gap that let a
// pool-exhausted Reseller continue spending via the video relay.
func PoolBalanceCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantCtx, err := GetTenantContext(c)
		if err != nil || tenantCtx == nil || tenantCtx.TenantID == "" {
			// No tenant identified yet (e.g. anonymous /v1/models call slipping
			// through). Pool gate has no opinion — let downstream decide.
			c.Next()
			return
		}

		tenantID := tenantCtx.TenantID
		pool, err := repo.GetTenantCreditPool(tenantID)
		if err != nil {
			if errors.Is(err, repo.ErrPoolNotFound) {
				// ADR §5 default: absence of a row = unlimited, bypass. The
				// CREDIT_POOL_REQUIRED flag lets ops gradually turn this into a
				// hard block (Phase 0 of the pool-required rollout). Unknown flag
				// values already degrade to "off" inside GetCreditPoolRequired —
				// this switch never fail-opens to enforce on its own.
				switch setting.GetCreditPoolRequired() {
				case setting.CreditPoolRequiredEnforce:
					app.RecordPoolNotConfigured(tenantID, "enforce")
					c.JSON(http.StatusPaymentRequired, gin.H{
						"error": gin.H{
							"code":      "pool_not_configured",
							"message":   "Tenant credit pool is not configured",
							"tenant_id": tenantID,
						},
					})
					c.Abort()
					return
				case setting.CreditPoolRequiredLog:
					group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
					app.RecordPoolNotConfigured(tenantID, "log")
					common.SysLog(fmt.Sprintf(
						`{"event":"pool_required_miss","who":"tenant:%s","group":"%s","what":"relay admitted request with no credit pool row","result":"bypass (CREDIT_POOL_REQUIRED=log)"}`,
						tenantID, group))
					c.Next()
					return
				default:
					// "off" — byte-identical to pre-flag behaviour.
					c.Next()
					return
				}
			}
			// Transient DB issue → fail open, but log so ops see it. This stays
			// fail-open even under CREDIT_POOL_REQUIRED=enforce — see the
			// "Deliberate residual" note in the godoc above.
			common.SysError("pool_balance_check: tenant=" + tenantID + " err=" + err.Error())
			c.Next()
			return
		}

		if pool.IsUnlimited() {
			c.Next()
			return
		}

		if pool.IsExhausted() {
			app.RecordPoolExhausted(tenantID, "relay")
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error": gin.H{
					"code":      "pool_exhausted",
					"message":   "Tenant credit pool exhausted",
					"tenant_id": tenantID,
				},
			})
			c.Abort()
			return
		}

		// Healthy pool — expose the live balance to dashboards even on the
		// read path so a Reseller sees a fresh value without waiting for the
		// next debit. Cheap: one gauge Set per request.
		metrics.CreditPoolBalance.WithLabelValues(tenantID).Set(float64(pool.CurrentBalance))
		c.Next()
	}
}
