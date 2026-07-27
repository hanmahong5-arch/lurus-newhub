package handler

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/privateendpoint"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// V2 Private-Routing status — "where does this tenant's model traffic go?"
//
// The console needs to answer one buyer question at a glance: is this tenant's
// inference staying inside our own network? This endpoint answers it from the
// live channel table, and it derives the verdict by calling the SAME
// classifier the dispatch guard uses (privateendpoint.ClassifyBaseURL). That
// shared call is the point: a console that re-implemented "is this address
// internal?" would eventually disagree with the code that actually blocks
// egress, and a reassuring green badge that contradicts runtime behaviour is
// worse than no badge at all.
// ============================================================================

// privateRoutingChannel is the field-whitelisted per-channel view. It carries
// no key/secret material — same posture as channelView in v2_channel.go.
type privateRoutingChannel struct {
	Id       int    `json:"id"`
	Name     string `json:"name"`
	Type     int    `json:"type"`
	TypeName string `json:"type_name"`
	BaseUrl  string `json:"base_url"`
	Models   string `json:"models"`
	Status   int    `json:"status"`
	// Intranet is the classifier verdict for this channel's base URL.
	Intranet bool `json:"intranet"`
	// Reason explains the verdict in words the console can print verbatim.
	Reason string `json:"reason"`
	// WillBeBlocked is true for a private-endpoint-typed channel whose base URL
	// is NOT intranet: the dispatch guard refuses it, so it can never serve
	// traffic. Surfacing it (rather than hiding it) is deliberate — an operator
	// needs to see a misconfigured row, not wonder why a model 500s.
	WillBeBlocked bool `json:"will_be_blocked"`
}

// Verdict values for privateRoutingResponse.Verdict.
const (
	// PrivateRoutingVerdictAllOnPrem — every enabled channel for this tenant is
	// a private endpoint pointing at an intranet address.
	PrivateRoutingVerdictAllOnPrem = "all_traffic_stays_on_prem"
	// PrivateRoutingVerdictMixed — the tenant has private endpoints AND channels
	// that egress to external providers.
	PrivateRoutingVerdictMixed = "mixed_private_and_external"
	// PrivateRoutingVerdictNoPrivate — no private-endpoint channel configured.
	PrivateRoutingVerdictNoPrivate = "no_private_endpoint_configured"
)

type privateRoutingResponse struct {
	Tenant string `json:"tenant"`
	// Verdict is the single headline the console renders as a badge.
	Verdict string `json:"verdict"`
	// EnforcedByCode states that the verdict is a mechanism, not a convention —
	// the console shows this so a buyer knows the badge is not cosmetic.
	EnforcedByCode bool `json:"enforced_by_code"`
	// PrivateEndpointChannels are channels typed ChannelTypePrivateEndpoint.
	PrivateEndpointChannels []privateRoutingChannel `json:"private_endpoint_channels"`
	// ExternalChannels are every other channel type — each one is a potential
	// egress path, listed so "stays on prem" can be audited rather than trusted.
	ExternalChannels []privateRoutingChannel `json:"external_channels"`
	// BlockedChannels is the subset of PrivateEndpointChannels the dispatch
	// guard will refuse (misconfigured to a public address).
	BlockedChannels []privateRoutingChannel `json:"blocked_channels"`
}

// GetPrivateRoutingStatus reports how a tenant's model traffic is routed.
//
// GET /api/v2/:tenant_slug/private-routing
//
// Admin-gated exactly like the channel list it summarises (a tenant's routing
// topology is operator information, not end-user information).
func GetPrivateRoutingStatus(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "Admin role required",
		})
		return
	}

	// Tenant-scoped read: never sees another tenant's channels. `num = 0` is
	// avoided by asking for a generous page — a tenant's channel count is small
	// and this view is a summary, not a paginated list.
	channels, err := repo.GetChannelsByTenant(tenantCtx.TenantID, 0, 500, true)
	if err != nil {
		common.SysError("Failed to load channels for private-routing status: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to load channels",
		})
		return
	}

	resp := privateRoutingResponse{
		Tenant:                  tenantCtx.TenantID,
		EnforcedByCode:          true,
		PrivateEndpointChannels: []privateRoutingChannel{},
		ExternalChannels:        []privateRoutingChannel{},
		BlockedChannels:         []privateRoutingChannel{},
	}

	for _, ch := range channels {
		if ch == nil {
			continue
		}
		view := privateRoutingChannel{
			Id:       ch.Id,
			Name:     ch.Name,
			Type:     ch.Type,
			TypeName: constant.GetChannelTypeName(ch.Type),
			BaseUrl:  ch.GetBaseURL(),
			Models:   ch.Models,
			Status:   ch.Status,
		}
		// Classify every channel, not just private-endpoint ones: a tenant that
		// believes it is on-prem needs to see that (say) an OpenAI channel is
		// still wired up and would egress.
		if verdict, classifyErr := privateendpoint.ClassifyBaseURL(view.BaseUrl); classifyErr == nil {
			view.Intranet = verdict.Intranet
			view.Reason = verdict.Reason
		} else {
			view.Intranet = false
			view.Reason = classifyErr.Error()
		}

		if ch.Type == constant.ChannelTypePrivateEndpoint {
			view.WillBeBlocked = !view.Intranet
			resp.PrivateEndpointChannels = append(resp.PrivateEndpointChannels, view)
			if view.WillBeBlocked {
				resp.BlockedChannels = append(resp.BlockedChannels, view)
			}
			continue
		}
		resp.ExternalChannels = append(resp.ExternalChannels, view)
	}

	resp.Verdict = derivePrivateRoutingVerdict(resp)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    resp,
	})
}

// derivePrivateRoutingVerdict collapses the channel lists into one headline.
//
// A channel that the dispatch guard will refuse does NOT count as an egress
// path (it cannot serve traffic), but it also cannot be the thing that makes a
// tenant "on prem" — so a tenant whose only private endpoint is misconfigured
// reports `no_private_endpoint_configured`, not a green badge.
func derivePrivateRoutingVerdict(resp privateRoutingResponse) string {
	usablePrivate := 0
	for _, ch := range resp.PrivateEndpointChannels {
		if !ch.WillBeBlocked {
			usablePrivate++
		}
	}
	if usablePrivate == 0 {
		return PrivateRoutingVerdictNoPrivate
	}
	if len(resp.ExternalChannels) > 0 {
		return PrivateRoutingVerdictMixed
	}
	return PrivateRoutingVerdictAllOnPrem
}
