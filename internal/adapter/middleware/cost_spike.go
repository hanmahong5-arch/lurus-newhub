package middleware

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

// CostSpikeLimit is a Gin middleware that watches LLM relay requests against
// the configured 5-minute quota window. On breach it ALWAYS counts the event
// (metrics.CostSpikeBreachTotal); whether it also auto-disables the user and
// rejects with 429 depends on common.CostSpikeEnforce (default false —
// observe mode):
//
//   - observe (CostSpikeEnforce=false, the default): the breach is counted
//     via metrics.CostSpikeBreachTotal{action="observed"} and logged (see
//     costSpikeShouldLog below for the per-user log throttle), but the
//     account is left enabled and the request proceeds. Reasons — see the
//     CostSpikeEnforce doc comment in internal/pkg/common/constants.go for
//     the full rationale (untested threshold, self-inflicted-outage failure
//     mode, observe-first rollout) — are not repeated here.
//   - enforce (CostSpikeEnforce=true): the breach passes through the same
//     per-user log throttle first (costSpikeShouldLog at :83 runs before
//     this branch — both paths share it), then the user is disabled
//     via repo.DisableUserById and the request is rejected 429. Disable is
//     best-effort: if it errors (e.g. the users table is unreachable) the
//     account stays enabled even though this request still got 429, so a
//     later request in the same window can breach again and needs the same
//     throttle observe mode relies on — see
//     TestCostSpikeLimit_Breach_DisableError_Still429.
//
// Place AFTER TokenAuth (which sets the "id" context key). When Redis is
// unavailable or the feature is disabled, the middleware fails open — better
// to allow legitimate traffic than block on infrastructure issues.
//
// Ported from 2b-svc-newapi/middleware/cost_spike.go (2026-05-07).
func CostSpikeLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !common.CostSpikeProtectionEnabled {
			c.Next()
			return
		}
		userID := c.GetInt("id")
		if userID == 0 {
			c.Next()
			return
		}
		if !common.RedisEnabled || common.RDB == nil {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		windowUsed, err := app.QueryCostSpikeWindow(ctx, common.RDB, userID)
		if err != nil {
			// Fail open on Redis errors — don't block legit traffic.
			common.SysLog(fmt.Sprintf("cost_spike check error user %d: %s", userID, err.Error()))
			c.Next()
			return
		}

		limit := int64(common.CostSpikeHardLimitPer5Min)
		if windowUsed < limit {
			c.Next()
			return
		}

		// Breach: always observe (log + count), regardless of enforce mode.
		enforce := common.CostSpikeEnforce
		action := "observed"
		if enforce {
			action = "enforced"
		}
		metrics.CostSpikeBreachTotal.WithLabelValues(action).Inc()
		if costSpikeShouldLog(userID) {
			costSpikeLogf(
				`{"event":"cost_spike_triggered","user_id":%d,"window_used":%d,"limit":%d,"enforce":%t,"action":"%s"}`,
				userID, windowUsed, limit, enforce, action,
			)
		}

		if !enforce {
			// Observe mode (default): don't touch the account, let the
			// request through. See common.CostSpikeEnforce for why.
			c.Next()
			return
		}

		// Enforce mode: auto-disable and 429.
		if disableErr := repo.DisableUserById(userID); disableErr != nil {
			common.SysLog(fmt.Sprintf("cost_spike disable user %d failed: %s", userID, disableErr.Error()))
		}
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": gin.H{
				"message": "cost spike limit exceeded; account temporarily disabled for safety",
				"type":    "new_api_error",
				"code":    "cost_spike_limit_exceeded",
			},
		})
	}
}

// costSpikeLogLast tracks, per user, the last time the "cost_spike_triggered"
// SysLogf line fired — a 1/minute-per-user log throttle applied identically
// on the observe and enforce paths (costSpikeShouldLog is called once, before
// the enforce/observe branch). Observe mode needs it because the account
// stays enabled, so every subsequent request within the 5-minute window would
// otherwise breach and log again — potentially one line per request for the
// duration of the window. Enforce mode needs it too: repo.DisableUserById is
// best-effort and can fail (see TestCostSpikeLimit_Breach_DisableError_Still429),
// leaving the account enabled for a later request in the same window to
// breach and log again. metrics.CostSpikeBreachTotal stays UNCONDITIONAL (same rationale
// as quota.go's zeroWalletWarnLast/D-A5): throttling the counter too would
// hide *how often* the window was actually breached, only the log noise is
// gated. Per-user, not global, for the same reason D-A5 keeps its throttle
// per-account: a global throttle would mask a second, different user
// breaching in the same minute.
var costSpikeLogLast sync.Map // userID(int) -> time.Time

// costSpikeLogf is the log sink CostSpikeLimit's throttled breach line writes
// through — a var, not a direct common.SysLogf call, so tests can substitute
// a counting stub and assert on emission counts (the same seam pattern as
// internal/app/quota.go's zeroWalletWarnLogf).
var costSpikeLogf = common.SysLogf

// costSpikeShouldLog reports whether the current breach should emit the
// "cost_spike_triggered" log line for userID, applying the 1/minute-per-user
// throttle described on costSpikeLogLast.
func costSpikeShouldLog(userID int) bool {
	now := time.Now()
	if last, loaded := costSpikeLogLast.LoadOrStore(userID, now); loaded {
		if now.Sub(last.(time.Time)) < time.Minute {
			return false
		}
		costSpikeLogLast.Store(userID, now)
	}
	return true
}

// resetCostSpikeLogThrottle clears the per-user log throttle state. Test-only
// seam: without it, costSpikeShouldLog's suppression window makes tests that
// drive the same userID through multiple breaches order-dependent on wall
// clock, the same reason quota.go's resetZeroWalletWarnThrottle exists.
func resetCostSpikeLogThrottle() {
	costSpikeLogLast.Range(func(key, _ any) bool {
		costSpikeLogLast.Delete(key)
		return true
	})
}
