package app

// channel_select_provider_filter_test.go — cycle 18 L8: the request's provider
// filter (constant.ContextKeyProviderFilter, a dto.ProviderFilter) narrows
// CacheGetRandomSatisfiedChannel to channels whose Setting JSON matches, and a
// session-affinity pin that points at a non-matching channel is ignored rather
// than honoured — affinity biases the choice among eligible channels, it never
// widens eligibility (same rule as the tenant check in lookupAffinityChannel).
//
// Runs the DB path (memory cache off), like channel_select_auto_test.go: a
// fresh replica selects this way until its first cache sync, so the filter has
// to hold there too, not only in the cache.

import (
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

const (
	providerFilterEUChannel = 9351
	providerFilterUSChannel = 9352
)

func setupProviderFilterSelection(t *testing.T) {
	t.Helper()
	db := setupServiceTestDB(t)
	if err := db.AutoMigrate(&repo.Ability{}); err != nil {
		t.Fatalf("automigrate abilities: %v", err)
	}
	withMemoryCacheDisabled(t)
	resetAffinityMemForTest()

	for _, ch := range []struct {
		id      int
		setting string
	}{
		{providerFilterEUChannel, `{"region":"eu"}`},
		{providerFilterUSChannel, `{"region":"us","data_collection":"deny"}`},
	} {
		w := uint(1)
		channel := &repo.Channel{
			Id: ch.id, Type: 1, Status: common.ChannelStatusEnabled,
			Name: "provider-filter-test", Models: "l8-filter-model", Group: "default", TenantId: "default",
			Weight: &w, Setting: common.GetPointer(ch.setting),
		}
		if err := db.Create(channel).Error; err != nil {
			t.Fatalf("seed channel %d: %v", ch.id, err)
		}
		if err := db.Create(&repo.Ability{
			Group: "default", Model: "l8-filter-model", ChannelId: ch.id, Enabled: true, Weight: w,
		}).Error; err != nil {
			t.Fatalf("seed ability %d: %v", ch.id, err)
		}
	}
}

func TestCacheGetRandomSatisfiedChannel_ProviderFilterNarrowsSelection(t *testing.T) {
	setupProviderFilterSelection(t)

	cases := []struct {
		name   string
		filter dto.ProviderFilter
		want   int
	}{
		{"region", dto.ProviderFilter{Region: "eu"}, providerFilterEUChannel},
		{"zdr", dto.ProviderFilter{DataCollection: dto.DataCollectionDeny}, providerFilterUSChannel},
		{"region_and_zdr", dto.ProviderFilter{Region: "us", DataCollection: dto.DataCollectionDeny}, providerFilterUSChannel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := affinitySelectCtx(t, "")
			common.SetContextKey(c, constant.ContextKeyProviderFilter, tc.filter)
			retry := 0
			param := &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "l8-filter-model", Retry: &retry}
			// Equal weights: without the filter this is a coin flip, so repeat
			// enough that an unwired implementation cannot pass by luck.
			for i := 0; i < 50; i++ {
				got, _, err := CacheGetRandomSatisfiedChannel(param)
				if err != nil || got == nil {
					t.Fatalf("iteration %d: selection failed: %v", i, err)
				}
				if got.Id != tc.want {
					t.Fatalf("iteration %d: filter ignored, got channel #%d want #%d", i, got.Id, tc.want)
				}
			}
		})
	}
}

func TestCacheGetRandomSatisfiedChannel_ProviderFilterMissIsAnError(t *testing.T) {
	setupProviderFilterSelection(t)

	c := affinitySelectCtx(t, "")
	common.SetContextKey(c, constant.ContextKeyProviderFilter, dto.ProviderFilter{Region: "cn", DataCollection: dto.DataCollectionDeny})
	retry := 0
	got, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: c, TokenGroup: "default", ModelName: "l8-filter-model", Retry: &retry})
	if got != nil {
		t.Fatalf("got channel #%d, want none", got.Id)
	}
	// The sentence names the filter the caller sent, so an EU customer reading
	// the 503 can tell "no channel in your region" from a general outage.
	want := "no channel satisfies provider filter (region=cn, data_collection=deny)"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to contain %q", err, want)
	}
}

func TestCacheGetRandomSatisfiedChannel_ProviderFilterOverridesAffinityPin(t *testing.T) {
	setupProviderFilterSelection(t)

	c := affinitySelectCtx(t, "pinned-to-us")
	common.SetContextKey(c, constant.ContextKeyProviderFilter, dto.ProviderFilter{Region: "eu"})
	// An earlier turn (sent without a filter) landed on the US channel.
	affinityStore(c, "pinned-to-us", affinityRecord{ChannelID: providerFilterUSChannel, Group: "default"})

	retry := 0
	param := &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "l8-filter-model", Retry: &retry}
	got, _, err := CacheGetRandomSatisfiedChannel(param)
	if err != nil || got == nil {
		t.Fatalf("selection failed: %v", err)
	}
	if got.Id != providerFilterEUChannel {
		t.Fatalf("affinity pin overrode the provider filter: got channel #%d want #%d", got.Id, providerFilterEUChannel)
	}
	// And the pin now follows the channel that actually served the request.
	rec, ok := affinityLoad(c, "pinned-to-us")
	if !ok || rec.ChannelID != providerFilterEUChannel {
		t.Fatalf("binding after selection = %+v (ok=%v), want re-pinned to #%d", rec, ok, providerFilterEUChannel)
	}
}
