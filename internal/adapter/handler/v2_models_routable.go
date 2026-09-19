package handler

// v2_models_routable.go — GET /api/v2/:tenant_slug/models/routable (L1,
// cycle-11). The console's own "what can this tenant route" question,
// answered from the exact tenant-scoped core /v1/models uses
// (tenantRoutableModels + narrowByTenantAllowlist in model.go), so the two
// endpoints answer the same set of ids for the same user — the previous
// source, GET /api/v2/:slug/models (v2_models.go, ListModelsV2), reads the
// manual catalogue table `models` and structurally cannot answer "what can
// this tenant route" (that table has no tenant_id and no channel/ability
// join at all).
//
// Unlike /v1/models this route carries no sk-token: there is no token to
// read a model_limit or group override from, so those two visibleModels
// branches (model.go:152-180, the modelLimitEnable branch) simply do not
// apply here — this handler calls tenantRoutableModels directly instead of
// going through visibleModels. tokenGroup resolution inside
// tenantRoutableModels reads ContextKeyTokenGroup, which a session-only
// caller never sets, so it falls through to the caller's own user group,
// same as /v1/models without a token-group override would.
//
// Intended mount: the tenantModels group in router/api-v2-router.go, whose
// UserAuth + TenantSlugGuard are what make tenant_context present on c
// before this handler runs — see TenantSlugGuard's doc comment. This
// handler does not depend on that placement for its own safety: it is
// fail-closed (401 UNAUTHENTICATED) whenever tenant_context or the user id
// is absent, proved by TestListRoutableModelsV2_Unauthenticated401
// regardless of where — or whether — the route ends up mounted.

import (
	"net/http"
	"sort"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"

	"github.com/gin-gonic/gin"
)

// routableModelView is the console-facing projection of dto.OpenAIModels —
// only the fields the picker/snippet builders on the four console pages
// need (id to send, owned_by for display, supported_endpoint_types to pick
// an OpenAI- vs Anthropic-wire example).
type routableModelView struct {
	Id                     string   `json:"id"`
	OwnedBy                string   `json:"owned_by"`
	SupportedEndpointTypes []string `json:"supported_endpoint_types"`
}

// ListRoutableModelsV2 handles GET /api/v2/:tenant_slug/models/routable.
func ListRoutableModelsV2(c *gin.Context) {
	userID := c.GetInt("id")
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil || tenantCtx == nil || userID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":    false,
			"message":    "Not authenticated",
			"error_code": "UNAUTHENTICATED",
		})
		return
	}

	// A cold process has never populated repo's process-wide
	// supported-endpoint-types map (pricing.go GetModelSupportEndpointTypes
	// only reads it, never builds it) until something calls GetPricing().
	// /v1/models discovery gets this for free through whichever request
	// happens to hit GetPricingV2/GetPricing first in the process's
	// lifetime; this endpoint has no such guarantee, so it forces the build
	// itself before reading the map.
	repo.GetPricing()

	acceptUnsetRatio := acceptUnsetRatioModels(c)
	models, err := tenantRoutableModels(c, userID, tenantCtx.TenantID, acceptUnsetRatio)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to resolve routable models",
		})
		return
	}
	models = narrowByTenantAllowlist(tenantCtx.TenantID, models)

	items := make([]routableModelView, 0, len(models))
	for _, m := range models {
		endpointTypes := make([]string, len(m.SupportedEndpointTypes))
		for i, et := range m.SupportedEndpointTypes {
			endpointTypes[i] = string(et)
		}
		items = append(items, routableModelView{
			Id:                     m.Id,
			OwnedBy:                m.OwnedBy,
			SupportedEndpointTypes: endpointTypes,
		})
	}
	// Deterministic order: the underlying query has no ORDER BY
	// (repo/ability.go GetGroupEnabledModelsForTenant), so without this the
	// console's "first routable model" (Chat's default, the Dashboard curl,
	// Token's snippets) could name a different model on every load and
	// differ between replicas. Sort by id so "first" is stable.
	sortRoutableItems(items)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": items,
		},
	})
}

// sortRoutableItems orders the projection by id. It is a separate function
// so a test can hand it a deliberately unsorted slice: on the sqlite test
// tier DISTINCT already yields alphabetical order, so a handler-level
// fixture cannot tell "sorted here" from "the DB happened to agree".
func sortRoutableItems(items []routableModelView) {
	sort.Slice(items, func(i, j int) bool { return items[i].Id < items[j].Id })
}
