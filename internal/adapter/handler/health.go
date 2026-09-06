package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

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

	// Schema-migration drift. Reported, never fatal: during a rolling update an
	// old pod legitimately sees pending migrations for the duration of the roll,
	// and failing readiness there would stall the very deploy that applies them.
	// The value is the snapshot published at boot, so this costs no DB round trip
	// on a probe that runs every few seconds.
	if pending, known := metrics.PendingSchemaMigrations(); !known {
		checks["schema_migrations"] = "unknown"
	} else if pending > 0 {
		checks["schema_migrations"] = fmt.Sprintf("pending:%d", pending)
	} else {
		checks["schema_migrations"] = "ok"
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
		"status": healthBodyStatus(healthy, checks),
		"checks": checks,
	})
}

// healthIntentionalOffStates lists the exact check values that represent a
// deliberate opt-out rather than a fault — these must never be reported as
// "degraded" even though they are not literally "ok".
var healthIntentionalOffStates = map[string]map[string]bool{
	"redis":             {"disabled": true},
	"billing":           {"legacy_mode": true},
	"database":          {"not_configured": true},
	"schema_migrations": {"unknown": true},
}

// healthBodyStatus derives the human-facing "status" word from the full set
// of checks, independent of the HTTP status code. The code only ever moves
// on a failed database check (readiness contract, untouched above); the body
// word must additionally go "degraded" for soft-dependency failures — Redis
// down, the billing breaker open, pending migrations — so an operator (or an
// alert reading this JSON) can't mistake a replica whose rate limiters are
// failing open for a genuinely healthy one.
func healthBodyStatus(healthy bool, checks map[string]string) string {
	if !healthy {
		return "degraded"
	}
	for name, value := range checks {
		if value == "ok" {
			continue
		}
		if healthIntentionalOffStates[name][value] {
			continue
		}
		return "degraded"
	}
	return "healthy"
}
