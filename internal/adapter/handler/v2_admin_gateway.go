package handler

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/relay/bifrosteng"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/resilience"

	"github.com/gin-gonic/gin"
)

// gatewayRouteHealth is one channel's live routing health as the relay sees it
// right now — not a historical aggregate.
type gatewayRouteHealth struct {
	resilience.BreakerSnapshot
	ChannelName string `json:"channel_name,omitempty"`
	Provider    string `json:"provider,omitempty"`
}

// GetGatewayHealthV2 reports the circuit-breaker state of every channel the
// relay has actually touched since this replica started.
//
// WHY: breaker state is the single most operationally urgent fact in the
// gateway — an Open breaker means traffic is being refused before it ever
// leaves the process — and until now it existed only as a Prometheus counter
// and a debug log line. Neither answers "which upstream is down right now",
// which is the first question during an incident.
//
// Two honesty caveats, both surfaced in the payload rather than hidden:
//   - Breakers are created lazily on first use, so a channel that has served no
//     traffic since boot is absent. Absent ≠ healthy; it means "unknown".
//   - State is the LAST RECORDED state. An Open breaker whose probe deadline
//     has passed still reads "open" until the next request flips it, because
//     advancing the state machine from a status read would burn the half-open
//     probe slot. probe_eligible_unix lets the reader tell the two apart.
//
// This replica only: breaker state is per-process by design (it reflects what
// THIS pod observed), so a multi-replica deployment shows one pod's view.
//
// GET /api/v2/admin/gateway/health — root only (RootJWTAuth on the group).
func GetGatewayHealthV2(c *gin.Context) {
	snapshots := channelBreakers.Snapshot()

	routes := make([]gatewayRouteHealth, 0, len(snapshots))
	openCount, halfOpenCount := 0, 0
	for _, snap := range snapshots {
		route := gatewayRouteHealth{BreakerSnapshot: snap}
		// Names/providers are best-effort decoration: a breaker can outlive the
		// channel row it was created for, and a missing name must not drop the
		// health entry that operators are looking for.
		if channel, err := repo.CacheGetChannel(snap.ChannelID); err == nil && channel != nil {
			route.ChannelName = channel.Name
			route.Provider = constant.GetChannelTypeName(channel.Type)
		}
		switch snap.State {
		case "open":
			openCount++
		case "half_open":
			halfOpenCount++
		}
		routes = append(routes, route)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"routes":          routes,
			"tracked":         len(routes),
			"open":            openCount,
			"half_open":       halfOpenCount,
			"replica_scoped":  true,
			"lazy_registered": true,
		},
	})
}

// GetRelayEngineV2 reports this replica's Bifrost/legacy relay-engine flag and
// in-process dispatch counters for the admin console.
//
// Honesty caveats (mirrored on ops surfaces that consume this):
//   - Config + counters are per-replica; a multi-replica NodePort shows one pod.
//   - totals count since process start, not a durable warehouse — Prometheus
//     /metrics remains the long-window source.
//   - engine=legacy with ready=false is the default healthy posture, not an error.
//
// GET /api/v2/admin/gateway/relay-engine — root only (RootJWTAuth on the group).
func GetRelayEngineV2(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    bifrosteng.Snapshot(),
	})
}
