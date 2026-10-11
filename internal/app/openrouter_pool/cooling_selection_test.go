package openrouter_pool

// End-to-end failover: the relay's cooldown write (MaybeMarkCooldown) followed
// by real channel selection. A multi-key channel whose every key is parked must
// stop being picked while a healthy channel exists, with the channel status
// flipped or not and with the in-memory routing cache on or off.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

var selectionDBCounter atomic.Int64

const (
	selMultiID   = 9901
	selHealthyID = 9902
	selGroup     = "default"
	selModel     = "sel-model"
)

func setupSelectionDB(t *testing.T, memoryCache bool) {
	t.Helper()
	dsn := fmt.Sprintf("file:orpoolsel%d?mode=memory&cache=shared", selectionDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&repo.Channel{}, &repo.Ability{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prevDB, prevSQLite, prevPG := repo.DB, common.UsingSQLite, common.UsingPostgreSQL
	prevMem, prevRedis := common.MemoryCacheEnabled, common.RedisEnabled
	repo.DB = db
	repo.InitCol()
	common.UsingSQLite, common.UsingPostgreSQL = true, false
	common.RedisEnabled = false
	common.MemoryCacheEnabled = memoryCache
	app.ClearChannelCooldowns()
	t.Cleanup(func() {
		_ = sqlDB.Close()
		repo.DB, common.UsingSQLite, common.UsingPostgreSQL = prevDB, prevSQLite, prevPG
		common.MemoryCacheEnabled, common.RedisEnabled = prevMem, prevRedis
		app.ClearChannelCooldowns()
	})

	w, p := uint(1), int64(0)
	multi := &repo.Channel{
		Id: selMultiID, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "multi",
		Key: "k-a\nk-b", Models: selModel, Group: selGroup, TenantId: "default", Weight: &w, Priority: &p,
	}
	multi.ChannelInfo.IsMultiKey = true
	multi.ChannelInfo.MultiKeySize = 2
	healthy := &repo.Channel{
		Id: selHealthyID, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "healthy",
		Key: "k-h", Models: selModel, Group: selGroup, TenantId: "default", Weight: &w, Priority: &p,
	}
	for _, ch := range []*repo.Channel{multi, healthy} {
		if err := db.Create(ch).Error; err != nil {
			t.Fatalf("seed channel %d: %v", ch.Id, err)
		}
		if err := db.Create(&repo.Ability{Group: selGroup, Model: selModel, ChannelId: ch.Id, Enabled: true, Weight: w, Priority: &p}).Error; err != nil {
			t.Fatalf("seed ability %d: %v", ch.Id, err)
		}
	}
	if memoryCache {
		repo.InitChannelCache()
	}
}

func selectOnce(t *testing.T) *repo.Channel {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	retry := 0
	ch, _, err := app.CacheGetRandomSatisfiedChannel(&app.RetryParam{Ctx: c, TokenGroup: selGroup, ModelName: selModel, Retry: &retry})
	if err != nil {
		t.Fatalf("selection failed: %v", err)
	}
	return ch
}

func TestAllKeysParked_SelectionFailsOverToHealthyChannel(t *testing.T) {
	withAutoDisable(t)
	cases := []struct {
		name    string
		memory  bool
		autoBan bool
	}{
		{"db fallback, auto-ban off (status kept)", false, false},
		{"db fallback, auto-ban on (status flipped)", false, true},
		{"memory cache, auto-ban on (status flipped)", true, true},
		{"memory cache, auto-ban off (status kept)", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupSelectionDB(t, tc.memory)
			for _, key := range []string{"k-a", "k-b"} {
				// IsMultiKey=false: what the first attempt really carries.
				MaybeMarkCooldown(types.ChannelError{
					ChannelId: selMultiID, ChannelType: constant.ChannelTypeOpenAI,
					AutoBan: tc.autoBan, UsingKey: key,
				}, base429Err())
			}
			for i := 0; i < 40; i++ {
				ch := selectOnce(t)
				if ch == nil || ch.Id != selHealthyID {
					t.Fatalf("attempt %d selected %+v, want the healthy channel %d", i, ch, selHealthyID)
				}
			}
		})
	}
}

// With one key still serving, the multi-key channel stays selectable.
func TestOneKeyParked_ChannelStaysSelectable(t *testing.T) {
	withAutoDisable(t)
	setupSelectionDB(t, false)
	MaybeMarkCooldown(types.ChannelError{
		ChannelId: selMultiID, ChannelType: constant.ChannelTypeOpenAI, AutoBan: true, UsingKey: "k-a",
	}, base429Err())
	seen := map[int]bool{}
	for i := 0; i < 60; i++ {
		seen[selectOnce(t).Id] = true
	}
	if !seen[selMultiID] || !seen[selHealthyID] {
		t.Fatalf("expected both channels selectable, saw %v", seen)
	}
}
