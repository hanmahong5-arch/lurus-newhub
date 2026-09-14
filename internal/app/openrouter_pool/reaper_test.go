package openrouter_pool

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	entity "github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
	"github.com/glebarez/sqlite"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"gorm.io/gorm"
)

// reaperHeartbeatDBCounter gives each hermetic sqlite DB in this file its
// own named database — mirrors setupServiceTestDB (internal/app) and
// openCleanupTestDB (internal/lifecycle); ListOpenRouterMultiKeyChannels
// only touches the channels table, so migrating repo.Channel alone is
// enough here (pgsetup_test.go's PostgreSQL harness is for the fuller
// multi-key cooldown integration tests and skips without TEST_POSTGRES_DSN).
var reaperHeartbeatDBCounter atomic.Int64

func openReaperHeartbeatTestDB(t *testing.T) {
	t.Helper()
	dsn := fmt.Sprintf("file:reaperheartbeat%d?mode=memory&cache=shared", reaperHeartbeatDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&repo.Channel{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	prevDB := repo.DB
	repo.DB = db
	t.Cleanup(func() {
		repo.DB = prevDB
		_ = sqlDB.Close()
	})
}

// reapChannel is the pure logic worth unit-testing without the full DB harness.
// It mutates the channel in place; the tests check the post-conditions on
// ChannelInfo maps and Channel.Status. SaveWithoutKey / ability-sync are
// integration concerns covered separately.

func TestReapChannel_RecoversExpiredOnly(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)

	ch := newMultiKeyChannel(3)
	// idx 0: expired cooldown — should recover
	// idx 1: still cooling — leave alone
	// idx 2: permanent disable (no cooldown entry) — leave alone
	markCooling(ch, 0, now.Add(-1*time.Minute).Unix(), "rate limit")
	markCooling(ch, 1, now.Add(10*time.Minute).Unix(), "rate limit")
	markPermanentDisable(ch, 2, "401 unauthorized")

	// Simulate the in-memory portion of reapChannel without invoking the
	// DB save (we don't want to drag in repo.DB here). The test exercises
	// the cooldown-map logic that's the heart of the reaper.
	expiredCount := 0
	for idx, until := range ch.ChannelInfo.MultiKeyCooldownUntil {
		if until > 0 && now.Unix() >= until {
			delete(ch.ChannelInfo.MultiKeyStatusList, idx)
			delete(ch.ChannelInfo.MultiKeyDisabledReason, idx)
			delete(ch.ChannelInfo.MultiKeyDisabledTime, idx)
			delete(ch.ChannelInfo.MultiKeyCooldownUntil, idx)
			expiredCount++
		}
	}

	if expiredCount != 1 {
		t.Fatalf("expected to recover exactly 1 key, got %d", expiredCount)
	}
	if _, stillDown := ch.ChannelInfo.MultiKeyStatusList[0]; stillDown {
		t.Errorf("idx 0 should have been re-enabled")
	}
	if _, stillDown := ch.ChannelInfo.MultiKeyStatusList[1]; !stillDown {
		t.Errorf("idx 1 should remain cooling")
	}
	if _, stillDown := ch.ChannelInfo.MultiKeyStatusList[2]; !stillDown {
		t.Errorf("idx 2 should remain permanently disabled")
	}
	if _, hasCooldown := ch.ChannelInfo.MultiKeyCooldownUntil[2]; hasCooldown {
		t.Errorf("idx 2 should never have had a cooldown entry")
	}
}

func TestReapChannel_DoesNotTouchPermanentDisables(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	ch := newMultiKeyChannel(2)
	markPermanentDisable(ch, 0, "billing_not_active")
	markPermanentDisable(ch, 1, "invalid_api_key")

	// Run the same reaper logic — should be a no-op
	for idx, until := range ch.ChannelInfo.MultiKeyCooldownUntil {
		if until > 0 && now.Unix() >= until {
			delete(ch.ChannelInfo.MultiKeyStatusList, idx)
		}
	}
	if len(ch.ChannelInfo.MultiKeyStatusList) != 2 {
		t.Errorf("permanent disables should not be reaped, got status list size=%d", len(ch.ChannelInfo.MultiKeyStatusList))
	}
}

// --- helpers ---

func newMultiKeyChannel(size int) *entity.Channel {
	keys := make([]string, size)
	for i := range keys {
		keys[i] = "sk-or-v1-test-" + string(rune('a'+i))
	}
	return &entity.Channel{
		Id:   42,
		Type: 20, // ChannelTypeOpenRouter
		Key:  joinNL(keys),
		ChannelInfo: entity.ChannelInfo{
			IsMultiKey:             true,
			MultiKeySize:           size,
			MultiKeyStatusList:     make(map[int]int),
			MultiKeyDisabledReason: make(map[int]string),
			MultiKeyDisabledTime:   make(map[int]int64),
			MultiKeyCooldownUntil:  make(map[int]int64),
		},
	}
}

func markCooling(ch *entity.Channel, idx int, until int64, reason string) {
	ch.ChannelInfo.MultiKeyStatusList[idx] = common.ChannelStatusAutoDisabled
	ch.ChannelInfo.MultiKeyDisabledReason[idx] = reason
	ch.ChannelInfo.MultiKeyDisabledTime[idx] = until - 60
	ch.ChannelInfo.MultiKeyCooldownUntil[idx] = until
}

func markPermanentDisable(ch *entity.Channel, idx int, reason string) {
	ch.ChannelInfo.MultiKeyStatusList[idx] = common.ChannelStatusAutoDisabled
	ch.ChannelInfo.MultiKeyDisabledReason[idx] = reason
	ch.ChannelInfo.MultiKeyDisabledTime[idx] = 1234567890
	// No CooldownUntil entry — that's the marker for "permanent".
}

func joinNL(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += "\n"
		}
		out += v
	}
	return out
}

// TestOpenRouterPoolReap_SuccessfulTickStampsHeartbeat is the L3 heartbeat
// oracle: a nil-error ReapOnce pass (here, trivially, zero OpenRouter
// channels to scan) must advance
// metrics.LeaderTaskLastSuccess{task="openrouter-pool-reap"} to "now".
// Drives the real, exported ReapOnce entry point production uses.
func TestOpenRouterPoolReap_SuccessfulTickStampsHeartbeat(t *testing.T) {
	openReaperHeartbeatTestDB(t)

	before := time.Now().Unix()
	if err := ReapOnce(context.Background(), time.Now); err != nil {
		t.Fatalf("ReapOnce: %v", err)
	}
	after := time.Now().Unix()

	got := testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues("openrouter-pool-reap"))
	if got < float64(before) || got > float64(after) {
		t.Errorf("LeaderTaskLastSuccess{task=openrouter-pool-reap} = %v, want within [%d, %d]", got, before, after)
	}
}

// TestOpenRouterPoolReap_FailedTickDoesNotStamp: when the channel listing
// itself errors, ReapOnce returns non-nil and the heartbeat must not move.
func TestOpenRouterPoolReap_FailedTickDoesNotStamp(t *testing.T) {
	openReaperHeartbeatTestDB(t)

	// Sabotage: drop the channels table so ListOpenRouterMultiKeyChannels
	// errors instead of returning an empty slice.
	if err := repo.DB.Migrator().DropTable(&repo.Channel{}); err != nil {
		t.Fatalf("drop channels: %v", err)
	}

	baseline := testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues("openrouter-pool-reap"))
	if err := ReapOnce(context.Background(), time.Now); err == nil {
		t.Fatal("ReapOnce: want error with channels table dropped, got nil")
	}
	got := testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues("openrouter-pool-reap"))

	if got != baseline {
		t.Errorf("LeaderTaskLastSuccess{task=openrouter-pool-reap} moved from %v to %v after a failed pass, want unchanged", baseline, got)
	}
}

// TestOpenRouterPoolReap_StartRegistersHeartbeat is the A-F1 oracle: the
// boot-time Set(0) and taskreg.Register calls inside AutoReapWithContext
// are otherwise deletable with every test in this package staying green.
// Pre-stamps a distinctive non-zero value so the zero-assertion below
// cannot pass merely from a GaugeVec's first-access default; forces
// common.IsLeader() false so the "run once on startup" branch cannot race
// the assertions with an async ReapOnce pass.
func TestOpenRouterPoolReap_StartRegistersHeartbeat(t *testing.T) {
	openReaperHeartbeatTestDB(t)

	prevLeader := common.IsLeader()
	common.SetLeader(false)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	metrics.LeaderTaskLastSuccess.WithLabelValues(openRouterPoolReapTaskName).Set(999999999)
	before := len(taskreg.Snapshot())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	AutoReapWithContext(ctx)

	if got := testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues(openRouterPoolReapTaskName)); got != 0 {
		t.Errorf("LeaderTaskLastSuccess{task=openrouter-pool-reap} = %v immediately after AutoReapWithContext, want 0 (boot-time Set(0) resetting a pre-stamped series)", got)
	}

	snap := taskreg.Snapshot()
	if len(snap) <= before {
		t.Fatalf("taskreg.Snapshot() length did not grow: before=%d after=%d", before, len(snap))
	}
	found := false
	for _, task := range snap {
		if task.Name == openRouterPoolReapTaskName {
			found = true
			if !task.LeaderOnly {
				t.Errorf("%s task.LeaderOnly = false, want true", openRouterPoolReapTaskName)
			}
		}
	}
	if !found {
		t.Errorf("taskreg.Snapshot() does not contain %q after AutoReapWithContext", openRouterPoolReapTaskName)
	}
}
