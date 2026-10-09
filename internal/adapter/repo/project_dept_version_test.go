package repo

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func deptRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prevRDB, prevOn := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = client, true
	deptVerSeen, deptVerKnown, deptVerPolledAt = 0, false, time.Time{}
	t.Cleanup(func() {
		common.RDB, common.RedisEnabled = prevRDB, prevOn
		deptVerSeen, deptVerKnown, deptVerPolledAt = 0, false, time.Time{}
		deptCacheNow = time.Now
		_ = client.Close()
		mr.Close()
		ResetProjectDeptCache()
	})
	return mr, client
}

// Another replica renames a project and bumps the shared version; this replica
// must stop serving its cached mapping within a poll interval, not the 30s TTL.
func TestDeptCache_RemoteBumpInvalidatesLocalCache(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	_, other := deptRedis(t)
	now := time.Now()
	deptCacheNow = func() time.Time { return now }
	ResetProjectDeptCache()

	p := mustCreateCodedProject(t, "ten-a", "Research", "R1")
	if id, ok := ResolveProjectByDeptCode("ten-a", "R1"); !ok || id != p.Id {
		t.Fatalf("first resolve = %d,%v", id, ok)
	}
	// Replica B changes the code and bumps the version.
	if err := DB.Model(p).Update("external_code", "R2").Error; err != nil {
		t.Fatal(err)
	}
	if err := other.Incr(context.Background(), deptVersionKey).Err(); err != nil {
		t.Fatal(err)
	}

	// Inside the poll window the cached (stale) answer is still served.
	if _, ok := ResolveProjectByDeptCode("ten-a", "R1"); !ok {
		t.Fatal("within the poll interval the cache is expected to answer")
	}
	now = now.Add(deptVersionPollEvery + time.Millisecond)
	if _, ok := ResolveProjectByDeptCode("ten-a", "R1"); ok {
		t.Fatal("after the poll the old code must no longer resolve")
	}
	if id, ok := ResolveProjectByDeptCode("ten-a", "R2"); !ok || id != p.Id {
		t.Fatalf("new code = %d,%v; want %d", id, ok, p.Id)
	}
}

func TestDeptCache_LocalResetBumpsSharedVersion(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	mr, _ := deptRedis(t)
	ResetProjectDeptCache()
	ResetProjectDeptCache()
	v, err := mr.Get(deptVersionKey)
	if err != nil || v != "2" {
		t.Fatalf("version = %q err=%v, want 2", v, err)
	}
}

// Redis down: the writer still clears its own cache, and a failing poll leaves
// readers on the TTL path without errors.
func TestDeptCache_RedisDownDegradesToLocal(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	mr, _ := deptRedis(t)
	now := time.Now()
	deptCacheNow = func() time.Time { return now }
	ResetProjectDeptCache()

	p := mustCreateCodedProject(t, "ten-a", "Ops", "O1")
	if _, ok := ResolveProjectByDeptCode("ten-a", "O1"); !ok {
		t.Fatal("resolve failed")
	}
	mr.SetError("down")
	DB.Model(p).Update("external_code", "O2")
	ResetProjectDeptCache() // bump fails, local cache still dropped
	if _, ok := ResolveProjectByDeptCode("ten-a", "O1"); ok {
		t.Fatal("local reset must take effect even when Redis is down")
	}
	now = now.Add(2 * deptVersionPollEvery)
	if id, ok := ResolveProjectByDeptCode("ten-a", "O2"); !ok || id != p.Id {
		t.Fatalf("resolve with failing poll = %d,%v", id, ok)
	}
}
