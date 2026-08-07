package repo

// cov_repo_model_rate_limit_test.go — admin CRUD for per-tenant, per-model
// RPM/TPM caps (entity.ModelRateLimit). The business-critical properties are:
// (1) tenant isolation on list/upsert/delete, (2) the upsert's ON CONFLICT
// path updates the existing row in place rather than duplicating it (would
// otherwise violate uk_model_rate_limits_tenant_model AND silently create two
// competing limits for the same model), and (3) delete of a nonexistent row
// is reported via the bool, not an error.

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

func repoSetupModelRateLimitPG(t *testing.T) {
	t.Helper()
	SetupTestDB(t)
	if err := DB.AutoMigrate(&entity.ModelRateLimit{}); err != nil {
		t.Fatalf("migrate model_rate_limits: %v", err)
	}
}

func TestUpsertModelRateLimit_CreateThenUpdateInPlace(t *testing.T) {
	repoSetupModelRateLimitPG(t)

	created, err := UpsertModelRateLimit("tenant-a", "gpt-4o", 60, 100000)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Id == 0 {
		t.Fatal("want a nonzero primary key on create")
	}
	if created.RateLimitRPM != 60 || created.RateLimitTPM != 100000 {
		t.Fatalf("created row mismatch: rpm=%d tpm=%d", created.RateLimitRPM, created.RateLimitTPM)
	}

	updated, err := UpsertModelRateLimit("tenant-a", "gpt-4o", 30, 50000)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Id != created.Id {
		t.Fatalf("upsert on conflict must update the SAME row, got new id %d want %d", updated.Id, created.Id)
	}
	if updated.RateLimitRPM != 30 || updated.RateLimitTPM != 50000 {
		t.Fatalf("updated row mismatch: rpm=%d tpm=%d", updated.RateLimitRPM, updated.RateLimitTPM)
	}

	// Exactly one row must exist for (tenant-a, gpt-4o) — no duplicate from the
	// conflict path.
	var cnt int64
	DB.Model(&entity.ModelRateLimit{}).Where("tenant_id = ? AND model = ?", "tenant-a", "gpt-4o").Count(&cnt)
	if cnt != 1 {
		t.Fatalf("want exactly 1 row after upsert-update, got %d", cnt)
	}
}

func TestUpsertModelRateLimit_SameModelDifferentTenantsAreIndependent(t *testing.T) {
	repoSetupModelRateLimitPG(t)

	a, err := UpsertModelRateLimit("tenant-a", "claude-3", 10, 1000)
	if err != nil {
		t.Fatalf("tenant-a upsert: %v", err)
	}
	b, err := UpsertModelRateLimit("tenant-b", "claude-3", 99, 9999)
	if err != nil {
		t.Fatalf("tenant-b upsert: %v", err)
	}
	if a.Id == b.Id {
		t.Fatal("same model name under different tenants must be distinct rows")
	}
	if a.RateLimitRPM != 10 || b.RateLimitRPM != 99 {
		t.Fatalf("cross-tenant leak: a.rpm=%d b.rpm=%d", a.RateLimitRPM, b.RateLimitRPM)
	}
}

func TestListModelRateLimits_ScopedToTenantAndOrdered(t *testing.T) {
	repoSetupModelRateLimitPG(t)

	if _, err := UpsertModelRateLimit("tenant-a", "z-model", 1, 1); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := UpsertModelRateLimit("tenant-a", "a-model", 2, 2); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := UpsertModelRateLimit("tenant-b", "m-model", 3, 3); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rows, err := ListModelRateLimits("tenant-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows for tenant-a, got %d", len(rows))
	}
	if rows[0].Model != "a-model" || rows[1].Model != "z-model" {
		t.Fatalf("want alphabetical order by model, got %q then %q", rows[0].Model, rows[1].Model)
	}
	for _, r := range rows {
		if r.TenantId != "tenant-a" {
			t.Fatalf("tenant-b row leaked into tenant-a list: %+v", r)
		}
	}
}

func TestListModelRateLimits_UnknownTenantReturnsEmptyNotError(t *testing.T) {
	repoSetupModelRateLimitPG(t)
	if _, err := UpsertModelRateLimit("tenant-real", "m1", 1, 1); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rows, err := ListModelRateLimits("tenant-nonexistent")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("want 0 rows for a tenant with no limits, got %d", len(rows))
	}
}

func TestDeleteModelRateLimit_ExistingReturnsTrueThenGone(t *testing.T) {
	repoSetupModelRateLimitPG(t)
	if _, err := UpsertModelRateLimit("tenant-a", "to-delete", 1, 1); err != nil {
		t.Fatalf("seed: %v", err)
	}

	deleted, err := DeleteModelRateLimit("tenant-a", "to-delete")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !deleted {
		t.Fatal("want true for deleting an existing row")
	}

	rows, err := ListModelRateLimits("tenant-a")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("want 0 rows after delete, got %d", len(rows))
	}
}

func TestDeleteModelRateLimit_NonexistentReturnsFalseNotError(t *testing.T) {
	repoSetupModelRateLimitPG(t)

	deleted, err := DeleteModelRateLimit("tenant-a", "never-existed")
	if err != nil {
		t.Fatalf("want nil error for deleting a nonexistent row, got %v", err)
	}
	if deleted {
		t.Fatal("want false for deleting a nonexistent row")
	}
}

// TestDeleteModelRateLimit_TenantIsolation proves a delete scoped to tenant-a
// cannot remove tenant-b's identically-named model limit.
func TestDeleteModelRateLimit_TenantIsolation(t *testing.T) {
	repoSetupModelRateLimitPG(t)
	if _, err := UpsertModelRateLimit("tenant-b", "shared-model", 5, 5); err != nil {
		t.Fatalf("seed tenant-b: %v", err)
	}

	deleted, err := DeleteModelRateLimit("tenant-a", "shared-model")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted {
		t.Fatal("want false: tenant-a has no such row even though tenant-b does")
	}

	rows, err := ListModelRateLimits("tenant-b")
	if err != nil || len(rows) != 1 {
		t.Fatalf("tenant-b row must survive tenant-a's delete attempt: rows=%v err=%v", rows, err)
	}
}
