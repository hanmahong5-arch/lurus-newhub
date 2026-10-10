package repo

import (
	"errors"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func routingPolicyDB(t *testing.T, name string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&entity.RoutingPolicy{}); err != nil {
		t.Fatal(err)
	}
	prev := DB
	DB = db
	t.Cleanup(func() { DB = prev })
}

// An explicit zero must survive the column default: min_confidence 0 means
// "accept any pick" and 0.65 is only the value for an OMITTED field.
func TestUpsertRoutingPolicy_ExplicitZeroSurvivesColumnDefault(t *testing.T) {
	routingPolicyDB(t, "rp_zero")
	p := &entity.RoutingPolicy{
		TenantID: "t1", PublicModel: "smart", Strategy: entity.RoutingStrategyDecision, MinConfidence: 0,
		Candidates: entity.RoutingCandidates{{ID: "a", Model: "model-a", Criteria: "c"}},
	}
	if err := UpsertRoutingPolicy(p); err != nil {
		t.Fatal(err)
	}
	got, err := GetRoutingPolicy("t1", "smart")
	if err != nil {
		t.Fatal(err)
	}
	if got.MinConfidence != 0 || got.Enabled {
		t.Fatalf("stored min_confidence=%v enabled=%v, want 0/false", got.MinConfidence, got.Enabled)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].Model != "model-a" {
		t.Fatalf("candidates round trip: %+v", got.Candidates)
	}
}

func TestRoutingPolicy_UpsertReplacesAndIsTenantScoped(t *testing.T) {
	routingPolicyDB(t, "rp_scope")
	mk := func(tenant string, enabled bool, conf float64) *entity.RoutingPolicy {
		return &entity.RoutingPolicy{
			TenantID: tenant, PublicModel: "smart", Strategy: entity.RoutingStrategyDecision, Enabled: enabled,
			EvaluatorModel: "evaluator-a", MinConfidence: conf,
			Candidates: entity.RoutingCandidates{{ID: "a", Model: "model-a", Criteria: "c"}},
		}
	}
	if err := UpsertRoutingPolicy(mk("t1", false, 0.5)); err != nil {
		t.Fatal(err)
	}
	if err := UpsertRoutingPolicy(mk("t1", true, 0.9)); err != nil { // same key: replace, not a second row
		t.Fatal(err)
	}
	if err := UpsertRoutingPolicy(mk("t2", true, 0.1)); err != nil {
		t.Fatal(err)
	}
	var n int64
	DB.Model(&entity.RoutingPolicy{}).Where("tenant_id = ?", "t1").Count(&n)
	if n != 1 {
		t.Fatalf("t1 rows = %d, want 1 (unique index)", n)
	}
	got, _ := GetRoutingPolicy("t1", "smart")
	if !got.Enabled || got.MinConfidence != 0.9 {
		t.Fatalf("not replaced: %+v", got)
	}
	enabled, err := ListEnabledRoutingPolicies()
	if err != nil || len(enabled) != 2 {
		t.Fatalf("enabled = %d err=%v", len(enabled), err)
	}
	if _, err := GetRoutingPolicy("t3", "smart"); !errors.Is(err, ErrRoutingPolicyNotFound) {
		t.Fatalf("other tenant sees a policy: %v", err)
	}
	if err := DeleteRoutingPolicy("t3", "smart"); !errors.Is(err, ErrRoutingPolicyNotFound) {
		t.Fatalf("delete of another tenant's policy: %v", err)
	}
	if err := DeleteRoutingPolicy("t1", "smart"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetRoutingPolicy("t2", "smart"); err != nil {
		t.Fatalf("t2 policy removed by t1's delete: %v", err)
	}
}
