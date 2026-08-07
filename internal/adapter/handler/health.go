package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// GetHealthDetailed returns a detailed health check including dependency connectivity.
// GET /api/health
func GetHealthDetailed(c *gin.Context) {
	checks := make(map[string]string)
	healthy := true

	// DB check
	if repo.DB != nil {
		sqlDB, err := repo.DB.DB()
		if err == nil {
			// Bound the ping with its own deadline (A1): a ping to a DOWN host can
			// otherwise outlive the readinessProbe timeoutSeconds:2 window and leave
			// a zombie pod draining traffic. Inline cancel — two more checks follow.
			ctx, cancel := context.WithTimeout(c.Request.Context(), common.HealthDBPingTimeout)
			pingErr := sqlDB.PingContext(ctx)
			cancel()
			if pingErr != nil {
				checks["database"] = "unreachable"
				healthy = false
			} else {
				checks["database"] = "ok"
			}
		} else {
			checks["database"] = "error"
			healthy = false
		}
	} else {
		checks["database"] = "not_configured"
	}

	// Redis check
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := common.RDB.Ping(ctx).Err(); err != nil {
			checks["redis"] = "unreachable"
		} else {
			checks["redis"] = "ok"
		}
	} else if common.RedisEnabled {
		// Enabled but no client: a broken wiring, not the intentional off
		// state — give the operator a distinct label instead of "disabled".
		checks["redis"] = "not_configured"
	} else {
		checks["redis"] = "disabled"
	}

	// Platform billing service check (via circuit breaker state)
	if common.BillingUnifiedEnabled() {
		if err := common.BillingBreakerAllow(); err != nil {
			checks["billing"] = "circuit_open"
		} else {
			checks["billing"] = "ok"
		}
	} else {
		checks["billing"] = "legacy_mode"
	}

	status := http.StatusOK
	if !healthy {
		status = http.StatusServiceUnavailable
	}

	c.JSON(status, gin.H{
		"status": map[bool]string{true: "healthy", false: "degraded"}[healthy],
		"checks": checks,
	})
}
