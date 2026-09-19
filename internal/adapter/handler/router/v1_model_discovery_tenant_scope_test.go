package router

// v1_model_discovery_tenant_scope_test.go — cycle-12 L5/W: drives the v1
// model-discovery endpoints through the real SetApiRouter route table (the
// actual AdminAuth/UserAuth chain that injects role and tenant_id), not a
// hand-built engine, and proves a tenant admin's answers stop at its own
// tenant's channels.
//
// Mutation: swap any of the *ForTenant repo calls in handler/user.go,
// handler/model.go or handler/channel.go back to their tenant-blind sibling
// and the matching assertion here goes red.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var v1DiscoveryScopeDBCounter atomic.Int64

const (
	v1DiscoveryOwnModel    = "l5-own-only-model"
	v1DiscoveryOtherModel  = "l5-other-only-model"
	v1DiscoveryOtherTag    = "l5-other-only-tag"
	v1DiscoveryOtherModels = "l5-other-secret-a,l5-other-secret-b"
)

func setupV1DiscoveryScopeChain(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:v1discoveryscope%d?mode=memory&cache=shared", v1DiscoveryScopeDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Tenant{}, &repo.Channel{}, &repo.Ability{}, &repo.Option{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}

	const ownTenantID = "l5-discovery-own"
	const otherTenantID = "l5-discovery-other"
	for _, tn := range []*repo.Tenant{
		{Id: ownTenantID, IDPOrgID: "l5-discovery-own-org", Slug: "l5-discovery-own-slug", Name: "L5 Own", Status: 1},
		{Id: otherTenantID, IDPOrgID: "l5-discovery-other-org", Slug: "l5-discovery-other-slug", Name: "L5 Other", Status: 1},
	} {
		if err := db.Create(tn).Error; err != nil {
			t.Fatalf("seed tenant %s: %v", tn.Id, err)
		}
	}

	admin := &repo.User{
		Username: "l5-discovery-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled,
		Email: "l5-discovery-admin@local", TenantId: ownTenantID, Group: "default",
	}
	if err := db.Create(admin).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	seedChannel := func(name, tenant, models, group, tag string) {
		ch := &repo.Channel{
			Name: name, TenantId: tenant, Key: "sk-" + name, Status: common.ChannelStatusEnabled,
			Type: 1, Models: models, Group: group, CreatedTime: common.GetTimestamp(),
		}
		if tag != "" {
			tg := tag
			ch.Tag = &tg
		}
		if err := db.Create(ch).Error; err != nil {
			t.Fatalf("seed channel %s: %v", name, err)
		}
		if err := db.Create(&repo.Ability{
			Group: group, Model: models, ChannelId: ch.Id, Enabled: true, Tag: ch.Tag,
		}).Error; err != nil {
			t.Fatalf("seed ability for %s: %v", name, err)
		}
	}
	seedChannel("l5-own-channel", ownTenantID, v1DiscoveryOwnModel, "default", "")
	seedChannel("l5-other-channel", otherTenantID, v1DiscoveryOtherModel, "default", "")

	otherTagged := &repo.Channel{
		Name: "l5-other-tagged", TenantId: otherTenantID, Key: "sk-l5-other-tagged",
		Status: common.ChannelStatusEnabled, Type: 1, Models: v1DiscoveryOtherModels,
		Group: "default", CreatedTime: common.GetTimestamp(),
	}
	tg := v1DiscoveryOtherTag
	otherTagged.Tag = &tg
	if err := db.Create(otherTagged).Error; err != nil {
		t.Fatalf("seed other tagged channel: %v", err)
	}

	prevDB, prevLogDB, prevRedis := repo.DB, repo.LOG_DB, common.RedisEnabled
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.RedisEnabled = false
	t.Cleanup(func() {
		repo.DB, repo.LOG_DB, common.RedisEnabled = prevDB, prevLogDB, prevRedis
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	engine := gin.New()
	engine.Use(gin.Recovery())
	store := cookie.NewStore([]byte("v1-discovery-scope-secret"))
	engine.Use(sessions.Sessions("session", store))
	engine.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("username", admin.Username)
		s.Set("role", admin.Role)
		s.Set("id", admin.Id)
		s.Set("status", common.UserStatusEnabled)
		s.Set("group", admin.Group)
		_ = s.Save()
		c.Next()
	})
	SetApiRouter(engine)
	return engine
}

func v1DiscoveryGet(t *testing.T, engine *gin.Engine, path string) map[string]interface{} {
	t.Helper()
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, want 200, body=%s", path, w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET %s: parse body: %v — raw %s", path, err, w.Body.String())
	}
	if body["success"] != true {
		t.Fatalf("GET %s: success = %v, body=%s", path, body["success"], w.Body.String())
	}
	return body
}

func v1DiscoveryStrings(t *testing.T, body map[string]interface{}) []string {
	t.Helper()
	raw, _ := body["data"].([]interface{})
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func v1DiscoveryHas(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestV1ModelDiscovery_TenantScoped_RealChain(t *testing.T) {
	engine := setupV1DiscoveryScopeChain(t)

	t.Run("user_models", func(t *testing.T) {
		got := v1DiscoveryStrings(t, v1DiscoveryGet(t, engine, "/api/user/models"))
		if !v1DiscoveryHas(got, v1DiscoveryOwnModel) {
			t.Errorf("own model missing from /api/user/models: %v", got)
		}
		if v1DiscoveryHas(got, v1DiscoveryOtherModel) {
			t.Errorf("/api/user/models leaked the other tenant's model: %v", got)
		}
	})

	t.Run("models_enabled", func(t *testing.T) {
		got := v1DiscoveryStrings(t, v1DiscoveryGet(t, engine, "/api/channel/models_enabled"))
		if !v1DiscoveryHas(got, v1DiscoveryOwnModel) {
			t.Errorf("own model missing from /api/channel/models_enabled: %v", got)
		}
		if v1DiscoveryHas(got, v1DiscoveryOtherModel) {
			t.Errorf("/api/channel/models_enabled leaked the other tenant's model: %v", got)
		}
	})

	t.Run("tag_models", func(t *testing.T) {
		body := v1DiscoveryGet(t, engine, "/api/channel/tag/models?tag="+v1DiscoveryOtherTag)
		if got, _ := body["data"].(string); got != "" {
			t.Errorf("/api/channel/tag/models returned another tenant's model list: %q", got)
		}
	})
}
