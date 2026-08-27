package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// The Redis-dependent CostSpikeLimit paths (window read + breach) are only
// covered by existing tests that use withRedis, which SKIPS without a real
// Redis. These miniredis variants run those branches unconditionally, seeding
// the exact ZSET (app.CostSpikeKeyPrefix = "cost_spike:user:<id>") the
// middleware reads via app.QueryCostSpikeWindow.
func costSpikeKey(userID int) string { return "cost_spike:user:" + strconv.Itoa(userID) }

func runCostSpike(userID int) *httptest.ResponseRecorder {
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("id", userID); c.Next() })
	r.GET("/x", CostSpikeLimit(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	return w
}

// Breach in ENFORCE mode (common.CostSpikeEnforce=true): a window already
// over the hard limit must auto-disable the user and return 429 — the
// legacy cost-spike safety behavior, exercised end-to-end. This is the only
// end-to-end proof that enforcement still works once an operator flips
// COST_SPIKE_ENFORCE back on; observe mode (the D-A6 default) is covered
// separately in r4_cost_spike_enforce_test.go.
func TestCostSpikeLimit_Breach_MiniRedis(t *testing.T) {
	_, dbCleanup := setupCoverDB(t)
	defer dbCleanup()

	user := &repo.User{Username: "spike-mr", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "spikemr@local", TenantId: "default"}
	if err := repo.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	_, rdb, cleanup := withMiniRedis(t)
	defer cleanup()

	prevEnabled := common.CostSpikeProtectionEnabled
	prevLimit := common.CostSpikeHardLimitPer5Min
	prevEnforce := common.CostSpikeEnforce
	common.CostSpikeProtectionEnabled = true
	common.CostSpikeHardLimitPer5Min = 50000
	common.CostSpikeEnforce = true
	defer func() {
		common.CostSpikeProtectionEnabled = prevEnabled
		common.CostSpikeHardLimitPer5Min = prevLimit
		common.CostSpikeEnforce = prevEnforce
	}()

	// Seed a fresh window entry worth 60000 quota units (> 50000 limit).
	nowMs := time.Now().UnixMilli()
	if err := rdb.ZAdd(context.Background(), costSpikeKey(user.Id), redis.Z{
		Score:  float64(nowMs),
		Member: fmt.Sprintf("%d:%d", nowMs, 60000),
	}).Err(); err != nil {
		t.Fatalf("seed zset: %v", err)
	}

	w := runCostSpike(user.Id)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 on breach; body=%s", w.Code, w.Body.String())
	}
	var got repo.User
	if err := repo.DB.First(&got, user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if got.Status != common.UserStatusDisabled {
		t.Errorf("user status = %d, want disabled (%d) after breach", got.Status, common.UserStatusDisabled)
	}
}

// Under the limit: the window read runs but the request passes through.
func TestCostSpikeLimit_UnderLimit_MiniRedis(t *testing.T) {
	_, rdb, cleanup := withMiniRedis(t)
	defer cleanup()

	prevEnabled := common.CostSpikeProtectionEnabled
	prevLimit := common.CostSpikeHardLimitPer5Min
	common.CostSpikeProtectionEnabled = true
	common.CostSpikeHardLimitPer5Min = 50000
	defer func() {
		common.CostSpikeProtectionEnabled = prevEnabled
		common.CostSpikeHardLimitPer5Min = prevLimit
	}()

	const uid = 771010
	nowMs := time.Now().UnixMilli()
	if err := rdb.ZAdd(context.Background(), costSpikeKey(uid), redis.Z{
		Score:  float64(nowMs),
		Member: fmt.Sprintf("%d:%d", nowMs, 100), // well under limit
	}).Err(); err != nil {
		t.Fatalf("seed zset: %v", err)
	}

	if w := runCostSpike(uid); w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 under limit", w.Code)
	}
}
