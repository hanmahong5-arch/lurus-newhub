package router

// model_discovery_probe_test.go proves the /v1/models GET route survives a
// caller with zero routable models through the REAL wiring — SetRelayRouter,
// the same function cmd/server/main.go reaches via SetRouter — not a
// hand-built engine that could diverge from production routing. Same
// rationale as relay_wire_stamp_test.go: driving the actual mount catches a
// route that silently stopped being registered, where a package-local test
// of handler.ListModels alone would not.
//
// The caller here is an enabled user + token with NO abilities row for their
// group — exactly a day-one tenant, or a token whose model allow-list is
// empty. Before the fix, handler.ListModels' Anthropic branch indexed
// element 0 of an empty slice and panicked, and gin's Recovery() middleware
// (mounted in production, see cmd/server/main.go) turned that into a 500
// carrying the panic text. This probe fails fast if the route disappears
// (asserting the exact 200/empty-list contract) so it cannot pass vacuously
// on a 404.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var modelDiscoveryProbeDBCounter atomic.Int64

// modelDiscoveryProbeDB wires an isolated, empty in-memory SQLite DB into
// repo.DB, migrates the tables TokenAuth + ListModels' group path touch, and
// seeds one enabled user + one enabled, unlimited-quota token with NO
// abilities row for the user's group — the empty-catalogue case this test
// exists to probe. Mirrors relayWireStampEmptyDB's plumbing (same file's
// counterpart for the "no token at all" case).
func modelDiscoveryProbeDB(t *testing.T) (key string, cleanup func()) {
	t.Helper()
	seq := modelDiscoveryProbeDBCounter.Add(1)
	dbName := fmt.Sprintf("file:model_discovery_probe_%d?mode=memory&cache=shared", seq)
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.User{}, &repo.Token{}, &entity.Ability{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	prevDB := repo.DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled
	repo.DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	user := &repo.User{
		Username: "model-discovery-probe", DisplayName: "Model Discovery Probe",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "model-discovery-probe@local", TenantId: "default", Quota: 1_000_000,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	key = common.GetRandomString(48)
	tok := &repo.Token{
		UserId: user.Id, TenantId: "default", Key: key, Status: common.TokenStatusEnabled,
		Name: "model-discovery-probe-token", CreatedTime: common.GetTimestamp(),
		AccessedTime: common.GetTimestamp(), ExpiredTime: -1, UnlimitedQuota: true,
	}
	if err := db.Create(tok).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	// Deliberately no entity.Ability rows: this user's group has zero
	// enabled models, reproducing the day-one-tenant / empty-allow-list case.

	cleanup = func() {
		repo.DB = prevDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, dbErr := db.DB(); dbErr == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	}
	return key, cleanup
}

// TestRelayRouter_ModelsGet_EmptyCatalogueDoesNotPanic drives GET /v1/models
// through the real SetRelayRouter wiring with the Anthropic-selecting
// headers (x-api-key + anthropic-version, see relay-router.go's models
// route) for a caller with no routable models. It must not come back as a
// 500 carrying gin's/Go's panic envelope.
func TestRelayRouter_ModelsGet_EmptyCatalogueDoesNotPanic(t *testing.T) {
	key, cleanup := modelDiscoveryProbeDB(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	// Recovery mirrors cmd/server/main.go's production engine: without it, a
	// panic in the handler would crash this test process instead of
	// surfacing as the 500 a real deployment would actually serve.
	engine.Use(gin.Recovery())
	SetRelayRouter(engine)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("x-api-key", "sk-"+key)
	req.Header.Set("anthropic-version", "2023-06-01")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code == http.StatusInternalServerError {
		t.Fatalf("GET /v1/models with zero routable models: status = 500 (panic), body=%s", w.Body.String())
	}
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/models with zero routable models: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"data":[]`) {
		t.Errorf("body = %s, want an empty data array for a caller with zero routable models", body)
	}
	if strings.Contains(body, "index out of range") {
		t.Errorf("body = %s, leaked the Go runtime panic text", body)
	}
}
