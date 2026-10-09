package app

import (
	"context"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func affinityTestChannel(id int) *repo.Channel {
	ch := &repo.Channel{Id: id, Key: "ka\nkb\nkc"}
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeySize = 3
	ch.ChannelInfo.MultiKeyMode = constant.MultiKeyModeRandom
	return ch
}

func TestKeyAffinity_StickyThenRebind(t *testing.T) {
	resetKeyAffinityForTest()
	defer resetKeyAffinityForTest()
	prev := common.RedisEnabled
	common.RedisEnabled = false
	defer func() { common.RedisEnabled = prev }()

	var seen []string
	KeyAffinityOutcomeHook = func(o string) { seen = append(seen, o) }
	defer func() { KeyAffinityOutcomeHook = nil }()

	ch := affinityTestChannel(8801)
	ctx := context.Background()
	_, first, err := SelectKeyWithAffinity(ctx, ch, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		_, idx, err := SelectKeyWithAffinity(ctx, ch, "sess-1")
		if err != nil || idx != first {
			t.Fatalf("session must stick to key %d, got %d (err %v)", first, idx, err)
		}
	}
	// The bound key stops being routable: the very next call re-binds.
	ch.ChannelInfo.MultiKeyStatusList = map[int]int{first: common.ChannelStatusAutoDisabled}
	_, second, err := SelectKeyWithAffinity(ctx, ch, "sess-1")
	if err != nil || second == first {
		t.Fatalf("must re-bind away from key %d, got %d (err %v)", first, second, err)
	}
	_, again, _ := SelectKeyWithAffinity(ctx, ch, "sess-1")
	if again != second {
		t.Fatalf("new binding must stick: %d != %d", again, second)
	}
	h, m, r := KeyAffinityStats()
	if h != 31 || m != 1 || r != 1 {
		t.Fatalf("stats hit/miss/rebind = %d/%d/%d, want 31/1/1", h, m, r)
	}
	if rate := KeyAffinityHitRate(); rate < 0.9 {
		t.Fatalf("hit rate %v", rate)
	}
	if len(seen) != 33 || seen[0] != KeyAffinityMiss {
		t.Fatalf("hook saw %d outcomes, first %q", len(seen), seen[0])
	}
}

func TestKeyAffinity_NoKeyOrSingleKeyBypasses(t *testing.T) {
	resetKeyAffinityForTest()
	defer resetKeyAffinityForTest()
	ch := affinityTestChannel(8802)
	if _, _, err := SelectKeyWithAffinity(context.Background(), ch, ""); err != nil {
		t.Fatal(err)
	}
	single := &repo.Channel{Id: 8803, Key: "only"}
	k, idx, err := SelectKeyWithAffinity(context.Background(), single, "sess")
	if err != nil || k != "only" || idx != 0 {
		t.Fatalf("single key: %q %d %v", k, idx, err)
	}
	if h, m, r := KeyAffinityStats(); h+m+r != 0 {
		t.Fatal("bypassed selections must not count")
	}
}

func TestKeyAffinity_TTLRenewOnlyBelowThreshold(t *testing.T) {
	resetKeyAffinityForTest()
	defer resetKeyAffinityForTest()
	prev := common.RedisEnabled
	common.RedisEnabled = false
	defer func() { common.RedisEnabled = prev }()

	now := time.Now()
	keyAffinityNow = func() time.Time { return now }
	ch := affinityTestChannel(8804)
	ctx := context.Background()
	_, idx, _ := SelectKeyWithAffinity(ctx, ch, "s")
	sk := keyAffinityStoreKey(8804, "s")
	bound := keyAffinityMem[sk].expires

	now = now.Add(10 * time.Minute) // 50 min left: no renewal
	_, _, _ = SelectKeyWithAffinity(ctx, ch, "s")
	if !keyAffinityMem[sk].expires.Equal(bound) {
		t.Fatal("TTL must not be renewed while more than 15 minutes remain")
	}
	now = now.Add(40 * time.Minute) // 10 min left: renew
	_, got, _ := SelectKeyWithAffinity(ctx, ch, "s")
	if got != idx || !keyAffinityMem[sk].expires.After(bound) {
		t.Fatal("TTL must be renewed below 15 minutes")
	}
	now = now.Add(2 * time.Hour) // expired
	if _, ok := keyAffinityLoad(ctx, sk); ok {
		t.Fatal("expired binding must be gone")
	}
}

// recordKeyAffinity is the only bridge to the Prometheus series; dropping its
// metrics.RecordKeyAffinity call must turn this red.
func TestKeyAffinity_FeedsMetricsSeries(t *testing.T) {
	resetKeyAffinityForTest()
	defer resetKeyAffinityForTest()
	prev := common.RedisEnabled
	common.RedisEnabled = false
	defer func() { common.RedisEnabled = prev }()

	read := func(o string) float64 { return testutil.ToFloat64(metrics.KeyAffinityTotal.WithLabelValues(o)) }
	miss0, hit0 := read(KeyAffinityMiss), read(KeyAffinityHit)
	ch := affinityTestChannel(8802)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, _, err := SelectKeyWithAffinity(ctx, ch, "sess-metrics"); err != nil {
			t.Fatal(err)
		}
	}
	if read(KeyAffinityMiss) != miss0+1 || read(KeyAffinityHit) != hit0+2 {
		t.Errorf("miss %v->%v hit %v->%v, want +1 / +2", miss0, read(KeyAffinityMiss), hit0, read(KeyAffinityHit))
	}
}
