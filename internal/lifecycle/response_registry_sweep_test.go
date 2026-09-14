package lifecycle

// response_registry_sweep_test.go — oracle tests for the response_registry
// retention sweep (cycle-8 L7, tasks-plugins-12). Mirrors
// openrouter_pool/reaper_test.go's heartbeat-oracle pattern and
// leader_election_test.go's isLeader-injection technique for driving a real
// LeaderTask.Run tick deterministically.

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"

	"github.com/glebarez/sqlite"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"gorm.io/gorm"
)

var responseRegistrySweepDBCounter atomic.Int64

func openResponseRegistrySweepTestDB(t *testing.T) func() {
	t.Helper()
	dsn := fmt.Sprintf("file:respregistrysweep%d?mode=memory&cache=shared", responseRegistrySweepDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.ResponseRegistry{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := repo.DB
	repo.DB = db
	return func() {
		repo.DB = prev
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	}
}

func seedResponseRegistryRow(t *testing.T, id string, expiresAt int64) {
	t.Helper()
	now := time.Now().Unix()
	row := &entity.ResponseRegistry{
		ResponseId: id, TenantId: "default", UserId: 1, TokenId: 1, ChannelId: 1,
		UpstreamModel: "gpt-4o-mini", CreatedAt: now, ExpiresAt: expiresAt,
	}
	if err := repo.UpsertResponseRegistry(row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// TestResponseRegistrySweep_ExpiredOnly drives the real
// runResponseRegistrySweep entry point (the exact fn StartResponseRegistrySweepWithContext
// wraps in a LeaderTask) against a seeded expired + live row pair.
func TestResponseRegistrySweep_ExpiredOnly(t *testing.T) {
	cleanup := openResponseRegistrySweepTestDB(t)
	defer cleanup()

	now := time.Now().Unix()
	seedResponseRegistryRow(t, "resp_sweep_expired", now-3600)
	seedResponseRegistryRow(t, "resp_sweep_live", now+86400)

	if err := runResponseRegistrySweep(context.Background()); err != nil {
		t.Fatalf("runResponseRegistrySweep: %v", err)
	}

	if _, err := repo.GetResponseRegistry("resp_sweep_expired"); !errors.Is(err, repo.ErrResponseRegistryNotFound) {
		t.Errorf("expired row survived: err=%v", err)
	}
	if _, err := repo.GetResponseRegistry("resp_sweep_live"); err != nil {
		t.Errorf("live row was swept: %v", err)
	}
}

// TestResponseRegistrySweep_StampsHeartbeat is the L3 heartbeat oracle: a
// real LeaderTask.Run tick (isLeader forced true, tiny interval/poll) that
// completes a nil-error pass must advance
// metrics.LeaderTaskLastSuccess{task="response-registry-sweep"} to "now" —
// the same contract every other NewLeaderTask-wrapped job in this package
// carries. Unlike TestResponseRegistrySweep_ExpiredOnly (which calls
// runResponseRegistrySweep directly), this drives the actual LeaderTask
// wrapper StartResponseRegistrySweepWithContext constructs, so a regression
// that stops passing runResponseRegistrySweep as the task's fn (e.g.
// swapping it for a no-op) would go undetected by the direct-call test
// above but red here.
func TestResponseRegistrySweep_StampsHeartbeat(t *testing.T) {
	cleanup := openResponseRegistrySweepTestDB(t)
	defer cleanup()

	task := NewLeaderTask(responseRegistrySweepTaskName, 5*time.Millisecond, runResponseRegistrySweep)
	var alwaysLeader atomic.Bool
	alwaysLeader.Store(true)
	task.isLeader = alwaysLeader.Load

	before := testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues(responseRegistrySweepTaskName))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = task.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	var got float64
	for time.Now().Before(deadline) {
		got = testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues(responseRegistrySweepTaskName))
		if got > before {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	if got <= before {
		t.Errorf("LeaderTaskLastSuccess{task=%s} did not advance within timeout (before=%v, got=%v)", responseRegistrySweepTaskName, before, got)
	}
}

// TestResponseRegistrySweep_StartRegistersHeartbeat is the oracle for
// cycle-8 L7 repair round finding A-F3: StartResponseRegistrySweepWithContext's
// taskreg.Register call is deletable with the package green unless asserted
// directly — nothing else in the repo reads "response-registry-sweep" by
// name. Mirrors session_sweep_test.go's
// TestSessionSweep_StartRegistersHeartbeat: a cancelled context means the
// background goroutine never actually ticks, isolating this assertion to
// the registration call alone.
func TestResponseRegistrySweep_StartRegistersHeartbeat(t *testing.T) {
	before := len(taskreg.Snapshot())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	StartResponseRegistrySweepWithContext(ctx)

	snap := taskreg.Snapshot()
	if len(snap) <= before {
		t.Fatalf("taskreg.Snapshot() length did not grow: before=%d after=%d", before, len(snap))
	}
	found := false
	for _, task := range snap {
		if task.Name == responseRegistrySweepTaskName {
			found = true
			if !task.LeaderOnly {
				t.Errorf("%s task.LeaderOnly = false, want true", responseRegistrySweepTaskName)
			}
			if d := task.Interval(); d != responseRegistrySweepInterval {
				t.Errorf("%s task.Interval() = %s, want %s", responseRegistrySweepTaskName, d, responseRegistrySweepInterval)
			}
		}
	}
	if !found {
		t.Errorf("taskreg.Snapshot() does not contain %q after StartResponseRegistrySweepWithContext", responseRegistrySweepTaskName)
	}
}
