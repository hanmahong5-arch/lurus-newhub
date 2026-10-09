package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// Supply-side 429 cooldown for channels that have no key pool.
//
// Multi-key channels keep their per-key cooldown inside ChannelInfo (see
// repo.MarkMultiKeyCooldown) and drop out of routing when every key cools.
// A single-key channel has no such state, so its cooldown lives here:
// the chan_cd_idx ZSET in Redis (score = deadline), with a process-local map as
// the fallback when Redis is off or unreachable.
//
// The ZSET chan_cd_idx is the source of truth every replica reads; the
// chan_cd:{id}:{keyIdx} keys are only a per-slot, human-readable mirror (TTL =
// deadline). Deleting a mirror key does not lift a cooldown: to lift one by
// hand call ClearChannelCooldown, which removes the ZSET members and the mirror.
// Other replicas drop their own local copy at their next snapshot refresh (at
// most cooldownSnapshotEvery later), unless that copy never reached Redis.
//
// Selection must stay a pure in-memory decision (the predicate runs under the
// channel cache read lock), so Redis is read as a periodic snapshot taken
// outside that lock rather than one round trip per candidate.

const (
	channelCooldownKeyPrefix = "chan_cd:"
	// channelCooldownIndexKey is a ZSET (score = deadline) over the live
	// cooldown slots; the snapshot reads it instead of scanning the keyspace.
	channelCooldownIndexKey = "chan_cd_idx"
	// channelCooldownIndexTTL must outlive the longest cooldown (24h cap): a
	// shorter TTL would drop the index while a long cooldown is still live, and
	// other replicas would route straight back to the cooling channel. Entries
	// are pruned by score on every write, so the slack costs nothing.
	channelCooldownIndexTTL = 25 * time.Hour
	// cooldownSnapshotEvery bounds how stale another replica's cooldown can be.
	cooldownSnapshotEvery = 2 * time.Second
	cooldownRedisTimeout  = 300 * time.Millisecond
)

// cooldownNow is the clock seam for tests.
var cooldownNow = time.Now

// ErrAllChannelsCooling is returned by channel selection when every channel
// that could serve the request is inside a 429 cooldown. It is deliberately
// not repo.ErrNoChannelSatisfiesPredicate: that one's wire sentence names the
// provider filter, which would tell a caller with no filter that their region
// is the problem. Callers translate it to 503 + Retry-After.
var ErrAllChannelsCooling = errors.New("all channels for the requested model are cooling down after upstream rate limits")

// AllChannelsCoolingError carries the earliest recovery deadline alongside the
// ErrAllChannelsCooling sentinel.
type AllChannelsCoolingError struct {
	RetryAfterUnix int64
}

func (e *AllChannelsCoolingError) Error() string { return ErrAllChannelsCooling.Error() }

func (e *AllChannelsCoolingError) Unwrap() error { return ErrAllChannelsCooling }

// CoolingRetryAfterUnix returns the earliest recovery deadline carried by an
// all-channels-cooling error, or 0 when err is not one.
func CoolingRetryAfterUnix(err error) int64 {
	var ce *AllChannelsCoolingError
	if errors.As(err, &ce) {
		return ce.RetryAfterUnix
	}
	return 0
}

// CoolingRetryAfterSeconds converts the deadline to a Retry-After value of at
// least one second.
func CoolingRetryAfterSeconds(err error) int64 {
	secs := CoolingRetryAfterUnix(err) - cooldownNow().Unix()
	if secs < 1 {
		return 1
	}
	return secs
}

type cooldownSlot struct {
	channelID int
	keyIdx    int
}

var (
	cooldownMu     sync.Mutex
	cooldownLocal  = map[cooldownSlot]int64{} // written by this process
	cooldownRemote = map[cooldownSlot]int64{} // last Redis snapshot
	// cooldownSynced marks local entries whose Redis write succeeded (value =
	// when it was written). Redis is authoritative for those: a later snapshot
	// that no longer lists the slot means another replica lifted it by hand.
	// Entries whose write failed are absent here and are never dropped by a
	// snapshot, since the local map is their only record.
	cooldownSynced   = map[cooldownSlot]time.Time{}
	cooldownRemoteAt time.Time
)

func channelCooldownKey(channelID, keyIdx int) string {
	return fmt.Sprintf("%s%d:%d", channelCooldownKeyPrefix, channelID, keyIdx)
}

func parseChannelCooldownKey(key string) (cooldownSlot, bool) {
	parts := strings.Split(strings.TrimPrefix(key, channelCooldownKeyPrefix), ":")
	if len(parts) != 2 {
		return cooldownSlot{}, false
	}
	id, err1 := strconv.Atoi(parts[0])
	idx, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return cooldownSlot{}, false
	}
	return cooldownSlot{id, idx}, true
}

// MarkChannelCooldown records that key keyIdx of channel channelID may not be
// used until the Unix-second deadline until. The process-local entry is always
// written; Redis (TTL = time to deadline) is written too when available so the
// other replicas see it within one snapshot interval.
func MarkChannelCooldown(channelID, keyIdx int, until int64) {
	now := cooldownNow()
	ttl := time.Unix(until, 0).Sub(now)
	if ttl <= 0 {
		return
	}
	cooldownMu.Lock()
	for slot, u := range cooldownLocal {
		if u <= now.Unix() {
			delete(cooldownLocal, slot)
		}
	}
	slot := cooldownSlot{channelID, keyIdx}
	cooldownLocal[slot] = until
	delete(cooldownSynced, slot)
	cooldownMu.Unlock()

	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), cooldownRedisTimeout)
		defer cancel()
		pipe := common.RDB.Pipeline()
		pipe.Set(ctx, channelCooldownKey(channelID, keyIdx), until, ttl)
		pipe.ZAdd(ctx, channelCooldownIndexKey, redis.Z{Score: float64(until), Member: fmt.Sprintf("%d:%d", channelID, keyIdx)})
		pipe.ZRemRangeByScore(ctx, channelCooldownIndexKey, "-inf", strconv.FormatInt(now.Unix(), 10))
		pipe.Expire(ctx, channelCooldownIndexKey, channelCooldownIndexTTL)
		if _, err := pipe.Exec(ctx); err != nil {
			common.SysLog("channel cooldown: redis write failed, process-local only: " + err.Error())
		} else {
			cooldownMu.Lock()
			if cooldownLocal[slot] == until {
				cooldownSynced[slot] = now
			}
			cooldownMu.Unlock()
		}
	}
}

// ClearChannelCooldowns drops every cooldown this process knows about,
// including the cached Redis snapshot. Redis keys expire on their own.
func ClearChannelCooldowns() {
	resetLocalChannelCooldowns()
	cooldownMu.Lock()
	cooldownRemote = map[cooldownSlot]int64{}
	cooldownRemoteAt = time.Time{}
	cooldownMu.Unlock()
}

func resetLocalChannelCooldowns() {
	cooldownMu.Lock()
	cooldownLocal = map[cooldownSlot]int64{}
	cooldownSynced = map[cooldownSlot]time.Time{}
	cooldownMu.Unlock()
}

// refreshCooldownSnapshot reloads the Redis view at most once per
// cooldownSnapshotEvery. Concurrent callers past the first keep using the
// previous snapshot instead of queueing behind the network call. A failed
// read keeps the old snapshot; the local map still covers this process.
func refreshCooldownSnapshot() {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	now := cooldownNow()
	cooldownMu.Lock()
	if !cooldownRemoteAt.IsZero() && now.Sub(cooldownRemoteAt) < cooldownSnapshotEvery {
		cooldownMu.Unlock()
		return
	}
	cooldownRemoteAt = now
	cooldownMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), cooldownRedisTimeout)
	defer cancel()
	// One ZRANGEBYSCORE over the deadline index: no keyspace scan, so the cost
	// does not grow with the session / rate-limit keys sharing this Redis DB.
	entries, err := common.RDB.ZRangeByScoreWithScores(ctx, channelCooldownIndexKey, &redis.ZRangeBy{
		Min: strconv.FormatInt(now.Unix()+1, 10), Max: "+inf",
	}).Result()
	if err != nil {
		return
	}
	snap := make(map[cooldownSlot]int64, len(entries))
	for _, e := range entries {
		member, _ := e.Member.(string)
		slot, ok := parseChannelCooldownKey(channelCooldownKeyPrefix + member)
		if !ok {
			continue
		}
		snap[slot] = int64(e.Score)
	}
	cooldownMu.Lock()
	cooldownRemote = snap
	// Redis is the source of truth for entries it accepted: drop the local copy
	// when the index no longer carries it (lifted elsewhere) or carries an
	// earlier deadline. Entries written after this read started are kept, the
	// read may predate them.
	for slot, writtenAt := range cooldownSynced {
		if !writtenAt.Before(now) {
			continue
		}
		if snap[slot] < cooldownLocal[slot] {
			delete(cooldownLocal, slot)
			delete(cooldownSynced, slot)
		}
	}
	cooldownMu.Unlock()
}

// coolingUntil returns the deadline of the cooldown on a slot, or 0.
func coolingUntil(channelID, keyIdx int) int64 {
	nowUnix := cooldownNow().Unix()
	slot := cooldownSlot{channelID, keyIdx}
	cooldownMu.Lock()
	defer cooldownMu.Unlock()
	var until int64
	if u := cooldownLocal[slot]; u > nowUnix {
		until = u
	}
	if u := cooldownRemote[slot]; u > nowUnix && u > until {
		until = u
	}
	return until
}

func anyCooldownActive() bool {
	nowUnix := cooldownNow().Unix()
	cooldownMu.Lock()
	defer cooldownMu.Unlock()
	for _, u := range cooldownLocal {
		if u > nowUnix {
			return true
		}
	}
	for _, u := range cooldownRemote {
		if u > nowUnix {
			return true
		}
	}
	return false
}

// cooldownProbe records what the cooldown predicate rejected during one
// selection so the caller can tell "everything is cooling" from a filter miss.
type cooldownProbe struct {
	rejected int
	earliest int64
}

func (p *cooldownProbe) note(until int64) {
	p.rejected++
	if p.earliest == 0 || until < p.earliest {
		p.earliest = until
	}
}

// cooldownPredicate rejects channels whose channel-level slot (keyIdx 0) is
// cooling. That slot is written for single-key channels and for multi-key
// channels whose last usable key just parked (MaybeMarkCooldown records the
// earliest key recovery), whether or not the channel status was flipped. A
// multi-key channel with a key left carries no channel-level slot: per-key
// state removes the cooling key from rotation. It never takes the per-channel
// polling lock, which would invert the order against channelSyncLock.
// Returns nil when no cooldown is active, so the common case adds
// no predicate (and no per-request allocation) to selection.
func cooldownPredicate(probe *cooldownProbe) repo.ChannelPredicate {
	if !anyCooldownActive() {
		return nil
	}
	return func(ch *repo.Channel) bool {
		if until := coolingUntil(ch.Id, 0); until > 0 {
			probe.note(until)
			return false
		}
		return true
	}
}

// usedChannelPredicate rejects the channels earlier attempts of this request
// already went through (relay.addUsedChannel records them in "use_channel").
func usedChannelPredicate(used map[int]struct{}) repo.ChannelPredicate {
	if len(used) == 0 {
		return nil
	}
	return func(ch *repo.Channel) bool {
		_, hit := used[ch.Id]
		return !hit
	}
}

// usedChannelIDs reads the channels this request has already tried.
func usedChannelIDs(c *gin.Context) map[int]struct{} {
	if c == nil {
		return nil
	}
	list := c.GetStringSlice("use_channel")
	if len(list) == 0 {
		return nil
	}
	out := make(map[int]struct{}, len(list))
	for _, s := range list {
		if id, err := strconv.Atoi(s); err == nil {
			out[id] = struct{}{}
		}
	}
	return out
}

// andChannelPredicates combines predicates with AND, skipping nils and
// short-circuiting in argument order. Order matters for the cooldown probe:
// put it last so it only counts channels the other predicates let through.
func andChannelPredicates(preds ...repo.ChannelPredicate) repo.ChannelPredicate {
	live := preds[:0:0]
	for _, p := range preds {
		if p != nil {
			live = append(live, p)
		}
	}
	switch len(live) {
	case 0:
		return nil
	case 1:
		return live[0]
	}
	return func(ch *repo.Channel) bool {
		for _, p := range live {
			if !p(ch) {
				return false
			}
		}
		return true
	}
}

// classifySelectionMiss turns the repo's generic predicate miss into the right
// sentinel: if any channel that passed the other predicates was cooling, the
// answer is "back off and retry" (ErrAllChannelsCooling with the earliest
// recovery); otherwise it is a provider-filter miss.
func classifySelectionMiss(err error, probe *cooldownProbe) error {
	if errors.Is(err, repo.ErrNoChannelSatisfiesPredicate) && probe.rejected > 0 {
		return &AllChannelsCoolingError{RetryAfterUnix: probe.earliest}
	}
	return err
}

// ChannelCoolingUntil returns the Unix-second deadline of the cooldown on one
// key slot of a channel as this process currently sees it, or 0 when none.
func ChannelCoolingUntil(channelID, keyIdx int) int64 {
	return coolingUntil(channelID, keyIdx)
}
