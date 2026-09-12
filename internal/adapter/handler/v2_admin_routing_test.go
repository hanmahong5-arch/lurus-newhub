package handler

// v2_admin_routing_test.go — hermetic coverage for v2_admin_routing.go's
// three L5 (routing-resilience-limits-11) routes: GET .../routing/affinity
// (stats), DELETE .../routing/affinity/:key (single purge), DELETE
// .../routing/affinity?all=true (purge-all). Reuses the pinnedAuditWriter /
// pollAuditRow / sqlite-setup pattern from v2_pricing_write_test.go (same
// package, no need to redeclare) so a purge is proven audited by a real
// polled row, not just a 2xx status code. The Redis-backed cases use a real
// miniredis server so PurgeAffinityBindingV2/PurgeAllAffinityBindingsV2 are
// driven through the actual app.PurgeAffinityKey/app.PurgeAllAffinity code
// path, not a hand-built stand-in.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var routingTestDBCounter atomic.Int64

// routingTestCtx wires a router carrying only the three affinity routes plus
// a mock actor-id middleware (mirrors RootJWTAuth's context-key contract:
// "id" is the actor int governance.NewAuditEvent reads), backed by its own
// sqlite db (for audit rows) and, when useRedis is requested by the caller,
// a real miniredis instance wired as common.RDB.
type routingTestCtx struct {
	router *gin.Engine
	db     *gorm.DB
	mr     *miniredis.Miniredis
	rdb    *redis.Client
}

func setupRoutingTestRouter(t *testing.T, useRedis bool) *routingTestCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:routingadmin%d?mode=memory&cache=shared", routingTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&entity.AuditEvent{}, &entity.AuditChainHead{}} {
		if err := db.AutoMigrate(tbl); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("auto migrate %T: %v", tbl, err)
		}
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedisEnabled := common.RedisEnabled
	prevRDB := common.RDB

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	governance.SetAuditWriter(&pinnedAuditWriter{db: db})

	ctx := &routingTestCtx{db: db}
	if useRedis {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("start miniredis: %v", err)
		}
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		common.RDB = rdb
		common.RedisEnabled = true
		ctx.mr = mr
		ctx.rdb = rdb
	} else {
		common.RedisEnabled = false
	}

	router := gin.New()
	// Mirrors RootJWTAuth's context-key contract without pulling in JWT
	// validation: "id" is the only field these handlers/RecordAuditEvent read
	// off the actor.
	mockRoot := func(c *gin.Context) {
		c.Set("id", 999)
		c.Next()
	}
	router.GET("/api/v2/admin/routing/affinity", mockRoot, GetAffinityStatsV2)
	router.DELETE("/api/v2/admin/routing/affinity", mockRoot, PurgeAllAffinityBindingsV2)
	router.DELETE("/api/v2/admin/routing/affinity/:key", mockRoot, PurgeAffinityBindingV2)
	ctx.router = router

	t.Cleanup(func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedisEnabled
		common.RDB = prevRDB
		if ctx.mr != nil {
			ctx.mr.Close()
		}
		if ctx.rdb != nil {
			_ = ctx.rdb.Close()
		}
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return ctx
}

func doRouting(ctx *routingTestCtx, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func parseRoutingBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	if w.Body.Len() == 0 {
		return nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}
	return out
}

// TestGetAffinityStatsV2_ReturnsLiveSnapshot proves the handler serves
// app.AffinityStatsSnapshot()'s real fields, not a hand-built stand-in: the
// backend field must read "memory" with RedisEnabled=false.
func TestGetAffinityStatsV2_ReturnsLiveSnapshot(t *testing.T) {
	ctx := setupRoutingTestRouter(t, false)

	w := doRouting(ctx, http.MethodGet, "/api/v2/admin/routing/affinity")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	body := parseRoutingBody(t, w)
	if body["success"] != true {
		t.Fatalf("success = %v, want true", body["success"])
	}
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not a map: %T", body["data"])
	}
	if data["backend"] != "memory" {
		t.Errorf("backend = %v, want memory (RedisEnabled=false in this test)", data["backend"])
	}
	for _, field := range []string{"hit", "miss", "stale", "mem_entries"} {
		if _, present := data[field]; !present {
			t.Errorf("missing field %q in response: %v", field, data)
		}
	}
}

// TestPurgeAffinityBindingV2_UnknownKey404 is the oracle
// TestAffinityPurge_Unknown404 at the HTTP layer: purging a key nobody ever
// stored must 404, not silently succeed.
func TestPurgeAffinityBindingV2_UnknownKey404(t *testing.T) {
	ctx := setupRoutingTestRouter(t, false)

	w := doRouting(ctx, http.MethodDelete, "/api/v2/admin/routing/affinity/never-existed-key")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", w.Code, w.Body.String())
	}
}

// TestPurgeAffinityBindingV2_ExistingRedisKey_RemovedAndAudited seeds a real
// binding in a real miniredis (using the same wire encoding
// app.PurgeAffinityKey/session_affinity.go's affinityStore write:
// "<channel_id>|<group>" under the "session_affinity:" prefix), purges it
// through the real handler, and asserts BOTH the 204 AND that the key is
// actually gone from Redis AND that the purge produced a real, polled
// routing.affinity_purged audit row — not merely a 2xx status.
func TestPurgeAffinityBindingV2_ExistingRedisKey_RemovedAndAudited(t *testing.T) {
	ctx := setupRoutingTestRouter(t, true)

	const key = "handler-purge-key"
	if err := ctx.rdb.Set(t.Context(), "session_affinity:"+key, "5|default", time.Hour).Err(); err != nil {
		t.Fatalf("seed redis key: %v", err)
	}

	w := doRouting(ctx, http.MethodDelete, "/api/v2/admin/routing/affinity/"+key)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}

	if v, err := ctx.rdb.Exists(t.Context(), "session_affinity:"+key).Result(); err != nil || v != 0 {
		t.Errorf("key must be gone from redis after purge: exists=%d err=%v", v, err)
	}

	row := pollAuditRow(t, governance.ActionRoutingAffinityPurged, 2*time.Second)
	if row == nil {
		t.Fatal("expected a routing.affinity_purged audit row, found none")
	}
	if row.ActorID != 999 {
		t.Errorf("audit ActorID = %d, want 999 (the mock actor id)", row.ActorID)
	}
	if !strings.Contains(row.Details, key) {
		t.Errorf("audit Details = %q, want it to mention the purged key %q", row.Details, key)
	}

	// A second delete of the same (now-gone) key must 404, proving the first
	// call actually removed it rather than being a no-op success.
	w2 := doRouting(ctx, http.MethodDelete, "/api/v2/admin/routing/affinity/"+key)
	if w2.Code != http.StatusNotFound {
		t.Errorf("second delete status = %d, want 404 (key must already be gone)", w2.Code)
	}
}

// TestPurgeAllAffinityBindingsV2_RequiresAllTrueQuery proves a bare DELETE
// against the collection path (no ?all=true) is rejected rather than
// silently wiping every binding.
func TestPurgeAllAffinityBindingsV2_RequiresAllTrueQuery(t *testing.T) {
	ctx := setupRoutingTestRouter(t, false)

	w := doRouting(ctx, http.MethodDelete, "/api/v2/admin/routing/affinity")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 without ?all=true, body=%s", w.Code, w.Body.String())
	}
}

// TestPurgeAllAffinityBindingsV2_Redis_RemovesEveryBindingAndAudits seeds
// several bindings plus one unrelated Redis key, purges all via
// ?all=true, and asserts every affinity key is gone, the unrelated key
// survives (bounded SCAN, not FLUSHALL), and a routing.affinity_purged
// audit row lands with scope "all".
func TestPurgeAllAffinityBindingsV2_Redis_RemovesEveryBindingAndAudits(t *testing.T) {
	ctx := setupRoutingTestRouter(t, true)

	for _, key := range []string{"all-purge-1", "all-purge-2", "all-purge-3"} {
		if err := ctx.rdb.Set(t.Context(), "session_affinity:"+key, "1|default", time.Hour).Err(); err != nil {
			t.Fatalf("seed redis key %s: %v", key, err)
		}
	}
	if err := ctx.rdb.Set(t.Context(), "unrelated:keep-me", "v", 0).Err(); err != nil {
		t.Fatalf("seed unrelated key: %v", err)
	}

	w := doRouting(ctx, http.MethodDelete, "/api/v2/admin/routing/affinity?all=true")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", w.Code, w.Body.String())
	}

	for _, key := range []string{"all-purge-1", "all-purge-2", "all-purge-3"} {
		if v, err := ctx.rdb.Exists(t.Context(), "session_affinity:"+key).Result(); err != nil || v != 0 {
			t.Errorf("key %s must be gone: exists=%d err=%v", key, v, err)
		}
	}
	if v, err := ctx.rdb.Get(t.Context(), "unrelated:keep-me").Result(); err != nil || v != "v" {
		t.Errorf("unrelated key must survive: got %q err=%v", v, err)
	}

	row := pollAuditRow(t, governance.ActionRoutingAffinityPurged, 2*time.Second)
	if row == nil {
		t.Fatal("expected a routing.affinity_purged audit row, found none")
	}
	if !strings.Contains(row.Details, `"scope":"all"`) {
		t.Errorf("audit Details = %q, want scope:all", row.Details)
	}
}

// appStatsSanity is a compile-time reminder that GetAffinityStatsV2 must stay
// wired to the real snapshot type, not a copy — if app.AffinityStats's shape
// changes, this line (and the handler) must be updated together.
var _ = app.AffinityStats{}
