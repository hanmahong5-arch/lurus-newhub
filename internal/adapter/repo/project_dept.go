package repo

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/redis/go-redis/v9"
)

// project_dept.go — department-header -> project resolution for the relay
// hot path (X-Lurus-Dept, migration 045). Read-only by design: a header that
// names no project resolves to "not found" and the caller keeps the token's
// own project. Nothing here (or anywhere on the relay path) may create a
// project, or a client-controlled header would mint cost centres.

const (
	// deptCacheTTL bounds how stale a mapping can be: a renamed, re-coded or
	// deleted project is honoured for at most this long. Short on purpose —
	// attribution is stamped permanently, so a long TTL would mis-bill.
	deptCacheTTL = 30 * time.Second
	// deptNegativeTTL is deliberately short: a freshly created external_code
	// must start attributing within seconds, not stay a cached miss.
	deptNegativeTTL = 5 * time.Second
	// deptCacheMax caps the cache. The key is attacker-influenced (header
	// text), so the map must not grow without bound; at the cap it is simply
	// reset, which only costs one extra query per live code.
	deptCacheMax = 4096
	// deptVersionKey is a Redis counter bumped by every project write; each
	// replica compares it with the value it last saw and drops its own cache on
	// a change, so a rename is honoured fleet-wide in about deptVersionPollEvery
	// instead of waiting out deptCacheTTL on the other replicas.
	deptVersionKey       = "dept_cache_ver"
	deptVersionPollEvery = time.Second
	deptRedisTimeout     = 300 * time.Millisecond
)

type deptCacheEntry struct {
	projectID int
	found     bool
	expires   time.Time
}

var (
	deptCacheMu  sync.Mutex
	deptCache    = map[string]deptCacheEntry{}
	deptCacheNow = time.Now // swapped by tests

	// deptVerSeen is the Redis version this replica's cache corresponds to;
	// deptVerKnown is false until the first successful read. deptVerPolledAt
	// throttles the Redis read to one per deptVersionPollEvery.
	deptVerSeen     int64
	deptVerKnown    bool
	deptVerPolledAt time.Time
)

// ResetProjectDeptCache drops every cached mapping (tests, and any writer
// that wants an immediate effect instead of waiting out the TTL).
//
// With Redis it also bumps the shared version counter so the other replicas
// drop theirs at their next poll. Without Redis (or when it errors) the other
// replicas fall back to the TTL, exactly as before.
func ResetProjectDeptCache() {
	deptCacheMu.Lock()
	deptCache = map[string]deptCacheEntry{}
	deptCacheMu.Unlock()
	bumpDeptVersion()
}

func bumpDeptVersion() {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), deptRedisTimeout)
	defer cancel()
	v, err := common.RDB.Incr(ctx, deptVersionKey).Result()
	if err != nil {
		common.SysLog("dept cache: redis version bump failed, other replicas fall back to TTL: " + err.Error())
		return
	}
	deptCacheMu.Lock()
	deptVerSeen, deptVerKnown = v, true
	deptCacheMu.Unlock()
}

// syncDeptCacheVersion drops the local cache when another replica bumped the
// shared version. At most one Redis read per deptVersionPollEvery; a Redis error
// leaves the cache alone (the TTL still bounds staleness).
func syncDeptCacheVersion(now time.Time) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	deptCacheMu.Lock()
	if !deptVerPolledAt.IsZero() && now.Sub(deptVerPolledAt) < deptVersionPollEvery {
		deptCacheMu.Unlock()
		return
	}
	deptVerPolledAt = now
	deptCacheMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), deptRedisTimeout)
	defer cancel()
	v, err := common.RDB.Get(ctx, deptVersionKey).Int64()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			return
		}
		v = 0 // counter never bumped yet
	}
	deptCacheMu.Lock()
	if deptVerKnown && v != deptVerSeen {
		deptCache = map[string]deptCacheEntry{}
	}
	deptVerSeen, deptVerKnown = v, true
	deptCacheMu.Unlock()
}

// ResolveProjectByDeptCode maps a department header value to a live project
// id inside tenantID: projects.external_code first, then projects.name. The
// tenant clause is part of both queries AND of the cache key, so a same-named
// project of another tenant can neither match nor be served from cache.
// Negative results are cached too (an unknown code from a misconfigured
// gateway would otherwise hit the DB on every request). A DB error is not
// cached and reads as not-found, so the request degrades to the token's own
// project rather than failing.
func ResolveProjectByDeptCode(tenantID, dept string) (projectID int, found bool) {
	if tenantID == "" || dept == "" {
		return 0, false
	}
	key := tenantID + "\x00" + dept
	now := deptCacheNow()
	syncDeptCacheVersion(now)

	deptCacheMu.Lock()
	if e, ok := deptCache[key]; ok && now.Before(e.expires) {
		deptCacheMu.Unlock()
		return e.projectID, e.found
	}
	deptCacheMu.Unlock()

	id, ok, err := lookupProjectByDept(tenantID, dept)
	if err != nil {
		return 0, false
	}

	ttl := deptCacheTTL
	if !ok {
		ttl = deptNegativeTTL
	}
	deptCacheMu.Lock()
	if len(deptCache) >= deptCacheMax {
		deptCache = map[string]deptCacheEntry{}
	}
	deptCache[key] = deptCacheEntry{projectID: id, found: ok, expires: now.Add(ttl)}
	deptCacheMu.Unlock()
	return id, ok
}

func lookupProjectByDept(tenantID, dept string) (int, bool, error) {
	// Soft-deleted rows are excluded by the model's DeletedAt scope.
	for _, col := range []string{"external_code", "name"} {
		var ids []int
		err := DB.Model(&entity.Project{}).
			Where("tenant_id = ? AND "+col+" = ?", tenantID, dept).
			Order("id ASC").Limit(1).Pluck("id", &ids).Error
		if err != nil {
			return 0, false, err
		}
		if len(ids) > 0 {
			return ids[0], true, nil
		}
	}
	return 0, false, nil
}
