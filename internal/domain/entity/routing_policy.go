package entity

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
)

// RoutingStrategyDecision is the only strategy today; the column is a varchar
// so a second strategy would be a data change, not a migration.
const RoutingStrategyDecision = "decision"

// DefaultRoutingMinConfidence is the code-side twin of the SQL default.
const DefaultRoutingMinConfidence = 0.65

// RoutingCandidate is one target the evaluator may choose: a stable id the
// policy refers to (default_candidate), the public model the request is
// rewritten to, and the natural-language criteria the evaluator is shown.
type RoutingCandidate struct {
	ID       string `json:"id"`
	Model    string `json:"model"`
	Criteria string `json:"criteria"`
}

// RoutingCandidates is the jsonb candidates column. It implements
// driver.Valuer / sql.Scanner explicitly so the same struct works on
// PostgreSQL (jsonb) and on the hermetic SQLite tier (text) without a
// dialect-specific tag.
type RoutingCandidates []RoutingCandidate

func (c RoutingCandidates) Value() (driver.Value, error) {
	if c == nil {
		return "[]", nil
	}
	b, err := json.Marshal([]RoutingCandidate(c))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (c *RoutingCandidates) Scan(src any) error {
	var raw []byte
	switch v := src.(type) {
	case nil:
		*c = RoutingCandidates{}
		return nil
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("routing candidates: unsupported scan type %T", src)
	}
	if len(raw) == 0 {
		*c = RoutingCandidates{}
		return nil
	}
	var out []RoutingCandidate
	if err := json.Unmarshal(raw, &out); err != nil {
		return errors.New("routing candidates: stored value is not a JSON array")
	}
	*c = out
	return nil
}

// RoutingPolicy is a tenant's decision-routing policy for one public model
// (migration 054, table routing_policies). Tags, defaults and the unique index
// mirror the SQL exactly; a policy is born disabled because auto-routing is
// permanent opt-in (ADR 2026-05-09).
type RoutingPolicy struct {
	ID               int64             `json:"id" gorm:"primaryKey"`
	TenantID         string            `json:"tenant_id" gorm:"column:tenant_id;type:varchar(36);not null;default:'';uniqueIndex:uk_routing_policies_tenant_model,priority:1"`
	PublicModel      string            `json:"public_model" gorm:"type:varchar(128);not null;uniqueIndex:uk_routing_policies_tenant_model,priority:2"`
	Strategy         string            `json:"strategy" gorm:"type:varchar(16);not null;default:'decision'"`
	Enabled          bool              `json:"enabled" gorm:"not null;default:false"`
	EvaluatorModel   string            `json:"evaluator_model" gorm:"type:varchar(128);not null;default:''"`
	Instructions     string            `json:"instructions" gorm:"type:text;not null;default:''"`
	MinConfidence    float64           `json:"min_confidence" gorm:"type:double precision;not null;default:0.65"`
	DefaultCandidate string            `json:"default_candidate" gorm:"type:varchar(64);not null;default:''"`
	Candidates       RoutingCandidates `json:"candidates" gorm:"type:jsonb;not null;default:'[]'"`
	CreatedAt        int64             `json:"created_at" gorm:"not null;default:0"`
	UpdatedAt        int64             `json:"updated_at" gorm:"not null;default:0"`
}

func (RoutingPolicy) TableName() string { return "routing_policies" }
