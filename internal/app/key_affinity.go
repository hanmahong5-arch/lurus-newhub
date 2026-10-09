package app

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// Key-level session stickiness. Channel affinity (session_affinity.go) pins a
// conversation to a channel; inside a multi-key channel every key is a
// separate upstream account with its own prompt cache, so the same
// conversation must keep landing on the same key. The binding is
// (channel id, affinity key) -> key index, TTL 1h, renewed only when less than
// keyAffinityRenewBelow remains (a hot conversation does not write every
// request). A bound key that is no longer routable is re-bound at once.
// The sliding-TTL session mapping follows Wei-Shaw relay-service project (MIT);
// the code is original. See THIRD_PARTY_NOTICES.md.

const (
	keyAffinityPrefix     = "key_affinity:"
	keyAffinityTTL        = time.Hour
	keyAffinityRenewBelow = 15 * time.Minute
	keyAffinityMemMax     = 50000
)

// Outcomes reported to KeyAffinityOutcomeHook and the counters.
const (
	KeyAffinityHit    = "hit"    // bound key still routable, reused
	KeyAffinityMiss   = "miss"   // no binding yet, a key was chosen and bound
	KeyAffinityRebind = "rebind" // bound key unroutable, switched to a new key
)

// KeyAffinityOutcomeHook is the metrics seam: the observability lane assigns a
// function that records one outcome. Nil means counters only.
var KeyAffinityOutcomeHook func(outcome string)

var keyAffinityHit, keyAffinityMiss, keyAffinityRebind atomic.Int64

// KeyAffinityStats returns the per-process outcome counters.
func KeyAffinityStats() (hit, miss, rebind int64) {
	return keyAffinityHit.Load(), keyAffinityMiss.Load(), keyAffinityRebind.Load()
}

// KeyAffinityHitRate is hit / (hit+miss+rebind), 0 when nothing was observed.
func KeyAffinityHitRate() float64 {
	h, m, r := KeyAffinityStats()
	if total := h + m + r; total > 0 {
		return float64(h) / float64(total)
	}
	return 0
}

func recordKeyAffinity(outcome string) {
	switch outcome {
	case KeyAffinityHit:
		keyAffinityHit.Add(1)
	case KeyAffinityMiss:
		keyAffinityMiss.Add(1)
	case KeyAffinityRebind:
		keyAffinityRebind.Add(1)
	}
	metrics.RecordKeyAffinity(outcome)
	if h := KeyAffinityOutcomeHook; h != nil {
		h(outcome)
	}
}

type keyAffinityEntry struct {
	idx     int
	expires time.Time
}

var (
	keyAffinityMu  sync.Mutex
	keyAffinityMem = map[string]keyAffinityEntry{}
	keyAffinityNow = time.Now
)

func keyAffinityStoreKey(channelID int, affinityKey string) string {
	return keyAffinityPrefix + strconv.Itoa(channelID) + ":" + affinityKey
}

// keyAffinityLoad returns the bound index and refreshes the TTL when less
// than keyAffinityRenewBelow is left.
func keyAffinityLoad(ctx context.Context, storeKey string) (int, bool) {
	if common.RedisEnabled && common.RDB != nil {
		val, err := common.RedisGet(ctx, storeKey)
		if err != nil || val == "" {
			return 0, false
		}
		idx, err := strconv.Atoi(val)
		if err != nil || idx < 0 {
			return 0, false
		}
		if left, err := common.RDB.TTL(ctx, storeKey).Result(); err == nil && left > 0 && left < keyAffinityRenewBelow {
			_ = common.RDB.Expire(ctx, storeKey, keyAffinityTTL).Err()
		}
		return idx, true
	}
	keyAffinityMu.Lock()
	defer keyAffinityMu.Unlock()
	e, ok := keyAffinityMem[storeKey]
	now := keyAffinityNow()
	if !ok || !now.Before(e.expires) {
		delete(keyAffinityMem, storeKey)
		return 0, false
	}
	if e.expires.Sub(now) < keyAffinityRenewBelow {
		e.expires = now.Add(keyAffinityTTL)
		keyAffinityMem[storeKey] = e
	}
	return e.idx, true
}

func keyAffinityBind(ctx context.Context, storeKey string, idx int) {
	if common.RedisEnabled && common.RDB != nil {
		_ = common.RedisSet(ctx, storeKey, strconv.Itoa(idx), keyAffinityTTL)
		return
	}
	keyAffinityMu.Lock()
	defer keyAffinityMu.Unlock()
	if len(keyAffinityMem) >= keyAffinityMemMax {
		now := keyAffinityNow()
		for k, e := range keyAffinityMem {
			if !now.Before(e.expires) {
				delete(keyAffinityMem, k)
			}
		}
		if len(keyAffinityMem) >= keyAffinityMemMax {
			return // full of live entries: losing a binding costs one cache miss
		}
	}
	keyAffinityMem[storeKey] = keyAffinityEntry{idx: idx, expires: keyAffinityNow().Add(keyAffinityTTL)}
}

func resetKeyAffinityForTest() {
	keyAffinityMu.Lock()
	keyAffinityMem = map[string]keyAffinityEntry{}
	keyAffinityMu.Unlock()
	keyAffinityHit.Store(0)
	keyAffinityMiss.Store(0)
	keyAffinityRebind.Store(0)
	keyAffinityNow = time.Now
}

// SelectKeyWithAffinity picks the upstream key for a request. Without an
// affinity key, or on a single-key channel, it is exactly
// channel.GetNextEnabledKey. With one, the conversation sticks to its bound
// key while that key is routable; otherwise a key is chosen by the channel's
// normal mode and the binding moves to it.
func SelectKeyWithAffinity(ctx context.Context, channel *repo.Channel, affinityKey string) (string, int, *types.NewAPIError) {
	if affinityKey == "" || !channel.ChannelInfo.IsMultiKey {
		return channel.GetNextEnabledKey()
	}
	storeKey := keyAffinityStoreKey(channel.Id, affinityKey)
	bound, had := keyAffinityLoad(ctx, storeKey)
	if had {
		if key, ok := channel.KeyIfRoutable(bound); ok {
			recordKeyAffinity(KeyAffinityHit)
			return key, bound, nil
		}
	}
	key, idx, err := channel.GetNextEnabledKey()
	if err != nil {
		return key, idx, err
	}
	keyAffinityBind(ctx, storeKey, idx)
	if had {
		recordKeyAffinity(KeyAffinityRebind)
	} else {
		recordKeyAffinity(KeyAffinityMiss)
	}
	return key, idx, nil
}
