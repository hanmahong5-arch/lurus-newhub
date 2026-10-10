package repo

import (
	"errors"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrRoutingPolicyNotFound is returned when no policy exists for the
// (tenant, public model) pair.
var ErrRoutingPolicyNotFound = errors.New("routing policy not found")

// GetRoutingPolicy loads one tenant's policy for a public model. The tenant is
// always part of the predicate: a policy is never addressable across tenants.
func GetRoutingPolicy(tenantID, publicModel string) (*entity.RoutingPolicy, error) {
	var p entity.RoutingPolicy
	err := DB.Where("tenant_id = ? AND public_model = ?", tenantID, publicModel).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRoutingPolicyNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// UpsertRoutingPolicy creates or replaces the policy for (tenant, model). The
// unique index makes this a single statement, so two concurrent PUTs cannot
// leave two rows.
func UpsertRoutingPolicy(p *entity.RoutingPolicy) error {
	now := common.GetTimestamp()
	p.UpdatedAt = now
	if p.CreatedAt == 0 {
		p.CreatedAt = now
	}
	// Written as a column map, not the struct: GORM substitutes the `default:`
	// tag value for ANY zero struct field (selected or not), and an explicit
	// min_confidence of 0 ("accept any pick") must stay 0. 0.65 is the value
	// of an OMITTED field, applied by the handler, not of a stored zero.
	return DB.Model(&entity.RoutingPolicy{}).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "public_model"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"strategy", "enabled", "evaluator_model", "instructions", "min_confidence",
			"default_candidate", "candidates", "updated_at",
		}),
	}).Create(map[string]any{
		"tenant_id":         p.TenantID,
		"public_model":      p.PublicModel,
		"strategy":          p.Strategy,
		"enabled":           p.Enabled,
		"evaluator_model":   p.EvaluatorModel,
		"instructions":      p.Instructions,
		"min_confidence":    p.MinConfidence,
		"default_candidate": p.DefaultCandidate,
		"candidates":        p.Candidates,
		"created_at":        p.CreatedAt,
		"updated_at":        p.UpdatedAt,
	}).Error
}

// DeleteRoutingPolicy removes one policy; ErrRoutingPolicyNotFound when it did
// not exist for this tenant.
func DeleteRoutingPolicy(tenantID, publicModel string) error {
	res := DB.Where("tenant_id = ? AND public_model = ?", tenantID, publicModel).Delete(&entity.RoutingPolicy{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrRoutingPolicyNotFound
	}
	return nil
}

// ListEnabledRoutingPolicies returns every enabled policy of every tenant. It
// feeds the request path's in-memory index (routingdecision), which refreshes
// it at most once per TTL; the table is tiny (one row per tenant model) so a
// full scan is cheaper than per-request lookups.
func ListEnabledRoutingPolicies() ([]entity.RoutingPolicy, error) {
	var out []entity.RoutingPolicy
	err := DB.Where("enabled = ?", true).Find(&out).Error
	return out, err
}
