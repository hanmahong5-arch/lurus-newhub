package app

// channel_select_affinity_test.go — proves session affinity is actually WIRED
// INTO channel selection, not merely implemented next to it.
//
// The unit tests in session_affinity_test.go cover key derivation and storage
// in isolation; those would still pass if CacheGetRandomSatisfiedChannel never
// consulted a binding. These tests drive the real selection entry point and
// assert on which channel comes back.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
)

func affinitySelectCtx(t *testing.T, key string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if key != "" {
		common.SetContextKey(c, constant.ContextKeySessionAffinity, key)
	}
	return c
}

func setupAffinitySelection(t *testing.T) {
	t.Helper()
	db := setupServiceTestDB(t)
	if err := db.AutoMigrate(&repo.Ability{}); err != nil {
		t.Fatalf("automigrate abilities: %v", err)
	}

	// Memory cache off → selection runs the SQL path, which is what a fresh
	// replica does before its first cache sync.
	prevCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = prevCache })

	resetAffinityMemForTest()

	for _, ch := range []struct {
		id     int
		weight uint
	}{{9301, 1}, {9302, 1}} {
		channel := &repo.Channel{
			Id: ch.id, Type: 1, Status: common.ChannelStatusEnabled,
			Name: "affinity-test", Models: "gpt-4o", Group: "default", TenantId: "default",
			Weight: &ch.weight,
		}
		if err := db.Create(channel).Error; err != nil {
			t.Fatalf("seed channel %d: %v", ch.id, err)
		}
		if err := db.Create(&repo.Ability{
			Group: "default", Model: "gpt-4o", ChannelId: ch.id, Enabled: true, Weight: ch.weight,
		}).Error; err != nil {
			t.Fatalf("seed ability %d: %v", ch.id, err)
		}
	}
}

func TestCacheGetRandomSatisfiedChannel_AffinityPinsFirstAttempt(t *testing.T) {
	setupAffinitySelection(t)

	c := affinitySelectCtx(t, "pinned-key")
	// Pre-existing binding, as if an earlier turn had landed on 9302.
	affinityStore(c, "pinned-key", affinityRecord{ChannelID: 9302, Group: "default"})

	retry := 0
	param := &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "gpt-4o", Retry: &retry}

	// Two equally-weighted channels: without affinity this is a coin flip, so
	// repeat enough that an unwired implementation cannot pass by luck.
	for i := 0; i < 20; i++ {
		got, group, err := CacheGetRandomSatisfiedChannel(param)
		if err != nil || got == nil {
			t.Fatalf("iteration %d: selection failed: %v", i, err)
		}
		if got.Id != 9302 {
			t.Fatalf("iteration %d: affinity ignored, got channel #%d want #9302", i, got.Id)
		}
		if group != "default" {
			t.Fatalf("iteration %d: group = %q, want default", i, group)
		}
	}
}

func TestCacheGetRandomSatisfiedChannel_AffinityIgnoredOnRetry(t *testing.T) {
	setupAffinitySelection(t)

	c := affinitySelectCtx(t, "retry-key")
	affinityStore(c, "retry-key", affinityRecord{ChannelID: 9302, Group: "default"})

	// retry > 0 means the previous attempt just failed. Re-pinning to the same
	// channel would retry the exact upstream that failed.
	retry := 1
	param := &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "gpt-4o", Retry: &retry}

	sawOther := false
	for i := 0; i < 30; i++ {
		got, _, err := CacheGetRandomSatisfiedChannel(param)
		if err != nil || got == nil {
			t.Fatalf("iteration %d: selection failed: %v", i, err)
		}
		if got.Id != 9302 {
			sawOther = true
			break
		}
	}
	if !sawOther {
		t.Error("retry path never left the pinned channel — affinity must not survive a failover")
	}
}

func TestCacheGetRandomSatisfiedChannel_RecordsBindingAfterSelection(t *testing.T) {
	setupAffinitySelection(t)

	c := affinitySelectCtx(t, "fresh-key")
	retry := 0
	param := &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "gpt-4o", Retry: &retry}

	got, _, err := CacheGetRandomSatisfiedChannel(param)
	if err != nil || got == nil {
		t.Fatalf("selection failed: %v", err)
	}

	rec, ok := affinityLoad(c, "fresh-key")
	if !ok {
		t.Fatal("first turn must record a binding for the next turn to reuse")
	}
	if rec.ChannelID != got.Id || rec.Group != "default" {
		t.Errorf("recorded binding %+v does not match served channel #%d", rec, got.Id)
	}
}

func TestCacheGetRandomSatisfiedChannel_StaleBindingFallsBack(t *testing.T) {
	setupAffinitySelection(t)

	c := affinitySelectCtx(t, "stale-key")
	// Channel 9999 does not exist — models the case where the pinned channel was
	// deleted or disabled between turns.
	affinityStore(c, "stale-key", affinityRecord{ChannelID: 9999, Group: "default"})

	retry := 0
	param := &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "gpt-4o", Retry: &retry}

	got, _, err := CacheGetRandomSatisfiedChannel(param)
	if err != nil || got == nil {
		t.Fatalf("stale binding must fall back to normal selection, got err=%v", err)
	}
	if got.Id != 9301 && got.Id != 9302 {
		t.Errorf("unexpected channel #%d", got.Id)
	}

	// The stale binding must be replaced, not left to cost a lookup every turn.
	rec, ok := affinityLoad(c, "stale-key")
	if !ok || rec.ChannelID != got.Id {
		t.Errorf("stale binding was not re-pinned: %+v ok=%v (served #%d)", rec, ok, got.Id)
	}
}

func TestCacheGetRandomSatisfiedChannel_NoAffinityKeyIsUnchanged(t *testing.T) {
	setupAffinitySelection(t)

	c := affinitySelectCtx(t, "") // one-shot request: no session identifier
	retry := 0
	param := &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "gpt-4o", Retry: &retry}

	got, _, err := CacheGetRandomSatisfiedChannel(param)
	if err != nil || got == nil {
		t.Fatalf("selection failed: %v", err)
	}

	affinityMemMu.Lock()
	stored := len(affinityMem)
	affinityMemMu.Unlock()
	if stored != 0 {
		t.Errorf("requests without a session identifier must not create bindings, got %d", stored)
	}
}
