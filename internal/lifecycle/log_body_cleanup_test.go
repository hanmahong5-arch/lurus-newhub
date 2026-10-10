package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
)

func TestLogBodyCleanupPass_DeletesOnlyExpired(t *testing.T) {
	db := openLogRetentionTestDB(t)
	if err := db.AutoMigrate(&entity.LogBody{}, &entity.Tenant{}); err != nil {
		t.Fatal(err)
	}
	// The pass has two promises: expiry, and "a tenant without consent keeps
	// nothing". Tenant t consents, so only its expired rows go; tenant gone
	// has withdrawn, so its live row goes too.
	if err := db.Create(&entity.Tenant{Id: "t", IDPOrgID: "org-t", Slug: "t", Name: "t", SedimentationConsent: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&entity.Tenant{Id: "gone", IDPOrgID: "org-gone", Slug: "gone", Name: "gone"}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, exp := range []int64{now.Unix() - 10, now.Unix(), now.Unix() + 3600} {
		if err := db.Create(&entity.LogBody{RequestId: "r", TenantId: "t", ExpiresAt: exp}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&entity.LogBody{RequestId: "w", TenantId: "gone", ExpiresAt: now.Unix() + 3600}).Error; err != nil {
		t.Fatal(err)
	}

	n, err := RunLogBodyCleanupPass(context.Background(), LoadLogBodyCleanupConfig(), now)
	if err != nil || n != 3 {
		t.Fatalf("pass = %d,%v want 3 (expired, exactly-due, and the withdrawn tenant's live row)", n, err)
	}
	var left int64
	db.Model(&entity.LogBody{}).Count(&left)
	if left != 1 {
		t.Fatalf("left %d, want the consenting tenant's one live row", left)
	}
}

func TestLogBodyCleanupPass_ErrorIsReported(t *testing.T) {
	db := openLogRetentionTestDB(t) // log_bodies table deliberately absent
	_ = db
	if _, err := RunLogBodyCleanupPass(context.Background(), LoadLogBodyCleanupConfig(), time.Now()); err == nil {
		t.Fatal("a failed delete must surface so the leader task does not stamp success")
	}
}

func TestLogBodyCleanupConfig_DefaultsAndOverrides(t *testing.T) {
	cfg := LoadLogBodyCleanupConfig()
	if cfg.Batch != 500 || cfg.MaxBatches != 200 || cfg.Interval != time.Hour {
		t.Fatalf("defaults wrong: %+v", cfg)
	}
	t.Setenv("LOG_BODY_CLEANUP_BATCH", "10")
	t.Setenv("LOG_BODY_CLEANUP_MAX_BATCHES_PER_PASS", "-1")
	t.Setenv("LOG_BODY_CLEANUP_INTERVAL_SECONDS", "60")
	cfg = LoadLogBodyCleanupConfig()
	if cfg.Batch != 10 || cfg.MaxBatches != 200 || cfg.Interval != time.Minute {
		t.Fatalf("overrides wrong: %+v", cfg)
	}
}

func TestLogBodyCleanup_StartRegistersLeaderOnlyHeartbeat(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	StartLogBodyCleanupWithContext(ctx)
	for _, task := range taskreg.Snapshot() {
		if task.Name == "log-body-cleanup" {
			if !task.LeaderOnly || task.Interval == nil {
				t.Fatalf("task not registered as leader-only with an interval: %+v", task)
			}
			return
		}
	}
	t.Fatal("log-body-cleanup missing from taskreg after Start")
}
