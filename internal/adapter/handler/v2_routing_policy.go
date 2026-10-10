package handler

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/app/routingdecision"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// Tenant-admin decision-routing policy (migration 054):
//
//	GET    /api/v2/:tenant_slug/routing-policies/:model
//	PUT    /api/v2/:tenant_slug/routing-policies/:model
//	DELETE /api/v2/:tenant_slug/routing-policies/:model
//
// Every route sits behind UserAuth + TenantSlugGuard and the tenant admin gate
// (tenantSelectionScope); the tenant is always the caller's own, never a path
// or body value, so another tenant's policy is simply "not found". A policy
// is validated as a whole before anything is written. :model is a single path
// segment, so a model name containing "/" cannot be addressed here.

type routingPolicyRequest struct {
	Enabled          bool                      `json:"enabled"`
	EvaluatorModel   string                    `json:"evaluator_model"`
	Instructions     string                    `json:"instructions"`
	MinConfidence    *float64                  `json:"min_confidence"`
	DefaultCandidate string                    `json:"default_candidate"`
	Candidates       []entity.RoutingCandidate `json:"candidates"`
}

func routingPolicyView(p *entity.RoutingPolicy) gin.H {
	cands := p.Candidates
	if cands == nil {
		cands = entity.RoutingCandidates{}
	}
	return gin.H{
		"public_model":      p.PublicModel,
		"strategy":          p.Strategy,
		"enabled":           p.Enabled,
		"evaluator_model":   p.EvaluatorModel,
		"instructions":      p.Instructions,
		"min_confidence":    p.MinConfidence,
		"default_candidate": p.DefaultCandidate,
		"candidates":        cands,
		"created_at":        p.CreatedAt,
		"updated_at":        p.UpdatedAt,
		"limits": gin.H{
			"max_candidates":         routingdecision.MaxCandidates,
			"max_criteria_bytes":     routingdecision.MaxCriteriaBytes,
			"max_instructions_bytes": routingdecision.MaxInstructionsBytes,
		},
	}
}

// GetRoutingPolicyV2 handles GET /:tenant_slug/routing-policies/:model.
func GetRoutingPolicyV2(c *gin.Context) {
	tenantID, ok := tenantSelectionScope(c)
	if !ok {
		return
	}
	p, err := repo.GetRoutingPolicy(tenantID, c.Param("model"))
	if err != nil {
		routingPolicyError(c, err, "read routing policy")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": routingPolicyView(p)})
}

// PutRoutingPolicyV2 handles PUT /:tenant_slug/routing-policies/:model: create
// or replace. The policy is born as the admin sends it; an omitted
// min_confidence takes the documented default (0.65).
func PutRoutingPolicyV2(c *gin.Context) {
	tenantID, ok := tenantSelectionScope(c)
	if !ok {
		return
	}
	var req routingPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dpFail(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid routing policy body")
		return
	}
	p := &entity.RoutingPolicy{
		TenantID:         tenantID,
		PublicModel:      c.Param("model"),
		Strategy:         entity.RoutingStrategyDecision,
		Enabled:          req.Enabled,
		EvaluatorModel:   req.EvaluatorModel,
		Instructions:     req.Instructions,
		MinConfidence:    entity.DefaultRoutingMinConfidence,
		DefaultCandidate: req.DefaultCandidate,
		Candidates:       req.Candidates,
	}
	if req.MinConfidence != nil {
		p.MinConfidence = *req.MinConfidence
	}
	routable := map[string]struct{}{}
	for _, m := range repo.GetEnabledModelsForTenant(tenantID) {
		routable[m] = struct{}{}
	}
	if verr := routingdecision.ValidatePolicy(p, func(m string) bool { _, ok := routable[m]; return ok }); verr != nil {
		dpFail(c, http.StatusBadRequest, verr.Code, verr.Message)
		return
	}
	if err := repo.UpsertRoutingPolicy(p); err != nil {
		routingPolicyError(c, err, "save routing policy")
		return
	}
	routingdecision.Invalidate()
	saved, err := repo.GetRoutingPolicy(tenantID, p.PublicModel)
	if err != nil {
		routingPolicyError(c, err, "read routing policy")
		return
	}
	// Instructions are admin-authored prose, still not copied into the chain:
	// the audit row records the shape of the change, not free text.
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, dpActor(c),
		governance.ActionRoutingPolicySet, governance.ResourceRoutingPolicy, int(saved.ID),
		fmt.Sprintf(`{"tenant_id":%q,"public_model":%q,"enabled":%t,"evaluator_model":%q,"candidates":%d,"min_confidence":%g,"default_candidate":%q}`,
			tenantID, saved.PublicModel, saved.Enabled, saved.EvaluatorModel, len(saved.Candidates), saved.MinConfidence, saved.DefaultCandidate)))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": routingPolicyView(saved)})
}

// DeleteRoutingPolicyV2 handles DELETE /:tenant_slug/routing-policies/:model.
func DeleteRoutingPolicyV2(c *gin.Context) {
	tenantID, ok := tenantSelectionScope(c)
	if !ok {
		return
	}
	model := c.Param("model")
	existing, err := repo.GetRoutingPolicy(tenantID, model)
	if err != nil {
		routingPolicyError(c, err, "read routing policy")
		return
	}
	if err := repo.DeleteRoutingPolicy(tenantID, model); err != nil {
		routingPolicyError(c, err, "delete routing policy")
		return
	}
	routingdecision.Invalidate()
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, dpActor(c),
		governance.ActionRoutingPolicyDeleted, governance.ResourceRoutingPolicy, int(existing.ID),
		fmt.Sprintf(`{"tenant_id":%q,"public_model":%q}`, tenantID, model)))
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func routingPolicyError(c *gin.Context, err error, what string) {
	if errors.Is(err, repo.ErrRoutingPolicyNotFound) {
		dpFail(c, http.StatusNotFound, "POLICY_NOT_FOUND", "Routing policy not found")
		return
	}
	common.SysError(what + ": " + err.Error())
	dpFail(c, http.StatusInternalServerError, "INTERNAL", "Failed to "+what)
}
