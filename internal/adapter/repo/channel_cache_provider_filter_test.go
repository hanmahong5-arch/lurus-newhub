package repo

// channel_cache_provider_filter_test.go — cycle 18 L8: request-side provider
// filter (region / zero-data-retention) applied at channel selection.
//
// Both selection paths must honour the predicate — the in-memory cache
// (GetRandomSatisfiedChannelWhere, MemoryCacheEnabled=true) and the DB
// fallback a fresh replica runs until its first cache sync — and a predicate
// that drops every candidate must surface as ErrNoChannelSatisfiesPredicate,
// not as the generic "no channel" nil that the distributor turns into a 404
// existence probe.

import (
	"errors"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// seedFilterChannel is seedTenantRelayChannel plus the channel's Setting JSON,
// which is where region / data_collection live (dto.ChannelSettings).
func seedFilterChannel(t *testing.T, id int, group, model, setting string) {
	t.Helper()
	ch := &Channel{
		Id: id, Type: 1, Status: common.ChannelStatusEnabled,
		Name: "pf" + model, Models: model, Group: group, TenantId: "default",
		Setting: common.GetPointer(setting),
	}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel %d: %v", id, err)
	}
	if err := DB.Create(&Ability{Group: group, Model: model, ChannelId: id, Enabled: true}).Error; err != nil {
		t.Fatalf("seed ability %d: %v", id, err)
	}
}

func regionPred(region string) ChannelPredicate {
	return func(ch *Channel) bool { return ch.GetSetting().Region == region }
}

func assertProviderFilterSelection(t *testing.T, euID, usID int) {
	t.Helper()
	for i := 0; i < 50; i++ {
		ch, err := GetRandomSatisfiedChannelWhere("", "default", "l8-filter-model", 0, regionPred("eu"))
		if err != nil || ch == nil || ch.Id != euID {
			t.Fatalf("draw %d (region=eu): got ch=%+v err=%v, want channel %d only", i, ch, err, euID)
		}
	}
	zdr := func(ch *Channel) bool { return ch.GetSetting().DataCollection == dto.DataCollectionDeny }
	for i := 0; i < 50; i++ {
		ch, err := GetRandomSatisfiedChannelWhere("", "default", "l8-filter-model", 0, zdr)
		if err != nil || ch == nil || ch.Id != usID {
			t.Fatalf("draw %d (data_collection=deny): got ch=%+v err=%v, want channel %d only", i, ch, err, usID)
		}
	}
	// Candidates existed for (group, model) but none satisfied the filter:
	// a distinct, recognisable error — not (nil, nil), which the caller reads
	// as "model never configured".
	ch, err := GetRandomSatisfiedChannelWhere("", "default", "l8-filter-model", 0, regionPred("cn"))
	if !errors.Is(err, ErrNoChannelSatisfiesPredicate) {
		t.Fatalf("region=cn: got ch=%+v err=%v, want ErrNoChannelSatisfiesPredicate", ch, err)
	}
	// A nil predicate is the old contract: both channels still get drawn.
	seen := map[int]bool{}
	for i := 0; i < 50; i++ {
		ch, err := GetRandomSatisfiedChannelWhere("", "default", "l8-filter-model", 0, nil)
		if err != nil || ch == nil {
			t.Fatalf("draw %d (no predicate): err=%v ch=%+v", i, err, ch)
		}
		seen[ch.Id] = true
	}
	if !seen[euID] || !seen[usID] {
		t.Fatalf("nil predicate narrowed selection: seen=%v", seen)
	}
}

func TestGetRandomSatisfiedChannelWhere_ProviderFilter(t *testing.T) {
	t.Run("memory_cache", func(t *testing.T) {
		cleanup := setupSQLiteDB(t)
		defer cleanup()
		withMemoryCache(t)

		seedFilterChannel(t, 9601, "default", "l8-filter-model", `{"region":"eu"}`)
		seedFilterChannel(t, 9602, "default", "l8-filter-model", `{"region":"us","data_collection":"deny"}`)
		InitChannelCache()

		assertProviderFilterSelection(t, 9601, 9602)
	})

	t.Run("db_path", func(t *testing.T) {
		cleanup := setupSQLiteDB(t)
		defer cleanup()
		prev := common.MemoryCacheEnabled
		common.MemoryCacheEnabled = false
		t.Cleanup(func() { common.MemoryCacheEnabled = prev })

		seedFilterChannel(t, 9611, "default", "l8-filter-model", `{"region":"eu"}`)
		seedFilterChannel(t, 9612, "default", "l8-filter-model", `{"region":"us","data_collection":"deny"}`)

		assertProviderFilterSelection(t, 9611, 9612)
	})
}

// A predicate over a (group, model) that has no candidates at all keeps the
// existing (nil, nil) answer: the filter did not narrow anything, so the
// distributor's never-configured probe must still be able to say 404.
func TestGetRandomSatisfiedChannelWhere_NoCandidatesIsNotAFilterMiss(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	withMemoryCache(t)
	InitChannelCache()

	ch, err := GetRandomSatisfiedChannelWhere("", "default", "never-configured", 0, regionPred("eu"))
	if ch != nil || err != nil {
		t.Fatalf("got ch=%+v err=%v, want (nil, nil)", ch, err)
	}
}
