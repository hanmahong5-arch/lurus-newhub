package repo

// cov_repo_token_cache_test.go — token_cache.go wraps the hot-path Redis cache
// used to avoid a DB round-trip per relay request. Money-relevant: the cached
// RemainQuota field is what the relay path decrements per-request, so a cache
// write/read/incr mismatch could let a token spend past its real remaining
// quota (cache says more than the DB) or get wrongly rejected (cache says
// less). Run against an in-process miniredis so the happy paths are exercised
// hermetically, matching the pattern in internal/pkg/common/redis_miniredis_test.go.

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// repoWithMiniRedis points common.RDB at an in-process miniredis for the
// duration of a test and restores the prior client + RedisEnabled + SyncFrequency
// on cleanup.
func repoWithMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prevRDB, prevEnabled, prevFreq := common.RDB, common.RedisEnabled, common.SyncFrequency
	common.RDB, common.RedisEnabled, common.SyncFrequency = client, true, 60
	t.Cleanup(func() {
		common.RDB, common.RedisEnabled, common.SyncFrequency = prevRDB, prevEnabled, prevFreq
		_ = client.Close()
		mr.Close()
	})
	return mr
}

func TestCacheSetToken_ThenGet_RoundTrips(t *testing.T) {
	repoWithMiniRedis(t)

	tok := Token{
		Id:          1,
		UserId:      7,
		Key:         "sk-cache-roundtrip-" + common.GetUUID(),
		Name:        "roundtrip",
		Status:      common.TokenStatusEnabled,
		RemainQuota: 5000,
	}
	rawKey := tok.Key

	if err := cacheSetToken(tok); err != nil {
		t.Fatalf("cacheSetToken: %v", err)
	}

	got, err := cacheGetTokenByKey(rawKey)
	if err != nil {
		t.Fatalf("cacheGetTokenByKey: %v", err)
	}
	if got.RemainQuota != 5000 {
		t.Errorf("RemainQuota=%d want 5000", got.RemainQuota)
	}
	if got.UserId != 7 {
		t.Errorf("UserId=%d want 7", got.UserId)
	}
	if got.Name != "roundtrip" {
		t.Errorf("Name=%q want roundtrip", got.Name)
	}
	// cacheGetTokenByKey restores the raw (un-hashed) key onto the result.
	if got.Key != rawKey {
		t.Errorf("Key=%q want raw key %q restored on read", got.Key, rawKey)
	}
}

func TestCacheSetToken_StripsRawKeyFromStoredHash(t *testing.T) {
	// cacheSetToken calls token.Clean() before writing, so the raw secret key
	// itself must never land as a value in the Redis hash (only its HMAC is
	// used as the hash's key name). This is a secret-hygiene property, not
	// just a round-trip check.
	mr := repoWithMiniRedis(t)

	rawKey := "sk-secret-should-not-be-stored-" + common.GetUUID()
	tok := Token{Key: rawKey, RemainQuota: 1}
	if err := cacheSetToken(tok); err != nil {
		t.Fatalf("cacheSetToken: %v", err)
	}

	hashName := "token:" + common.GenerateHMAC(rawKey)
	storedKeyField := mr.HGet(hashName, "Key")
	if storedKeyField == rawKey {
		t.Fatal("raw key must not be stored as a hash field value (Clean() should have blanked it)")
	}
}

func TestCacheDeleteToken_RemovesEntry(t *testing.T) {
	repoWithMiniRedis(t)

	rawKey := "sk-delete-me-" + common.GetUUID()
	if err := cacheSetToken(Token{Key: rawKey, RemainQuota: 10}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := cacheGetTokenByKey(rawKey); err != nil {
		t.Fatalf("precondition get before delete: %v", err)
	}

	if err := cacheDeleteToken(rawKey); err != nil {
		t.Fatalf("cacheDeleteToken: %v", err)
	}

	if _, err := cacheGetTokenByKey(rawKey); err == nil {
		t.Fatal("want error reading a deleted cache entry")
	}
}

func TestCacheIncrDecrTokenQuota_AdjustsCachedRemainQuota(t *testing.T) {
	repoWithMiniRedis(t)

	rawKey := "sk-incr-" + common.GetUUID()
	if err := cacheSetToken(Token{Key: rawKey, RemainQuota: 1000}); err != nil {
		t.Fatalf("set: %v", err)
	}

	if err := cacheDecrTokenQuota(rawKey, 300); err != nil {
		t.Fatalf("decr: %v", err)
	}
	got, err := cacheGetTokenByKey(rawKey)
	if err != nil {
		t.Fatalf("get after decr: %v", err)
	}
	if got.RemainQuota != 700 {
		t.Fatalf("RemainQuota=%d want 700 after -300", got.RemainQuota)
	}

	if err := cacheIncrTokenQuota(rawKey, 250); err != nil {
		t.Fatalf("incr: %v", err)
	}
	got, err = cacheGetTokenByKey(rawKey)
	if err != nil {
		t.Fatalf("get after incr: %v", err)
	}
	if got.RemainQuota != 950 {
		t.Fatalf("RemainQuota=%d want 950 after +250", got.RemainQuota)
	}
}

// TestCacheIncrTokenQuota_OnUncachedKeyIsSilentNoop locks the current
// (surprising) behavior: incrementing a token that was never cacheSetToken'd
// does not error and does not create the hash — RedisHIncrBy only acts when
// the target key already carries a TTL (see internal/pkg/common/redis.go),
// which an un-set key never does. Callers must not rely on incr alone to
// materialize a cache entry.
// FINDING: cacheIncrTokenQuota returns nil (success) on a cold/uncached key
// while silently performing no write — a caller checking only `err == nil`
// cannot distinguish "quota adjusted" from "cache entry doesn't exist yet".
func TestCacheIncrTokenQuota_OnUncachedKeyIsSilentNoop(t *testing.T) {
	repoWithMiniRedis(t)

	rawKey := "sk-never-cached-" + common.GetUUID()
	if err := cacheIncrTokenQuota(rawKey, 100); err != nil {
		t.Fatalf("want nil error (documented current behavior), got %v", err)
	}

	if _, err := cacheGetTokenByKey(rawKey); err == nil {
		t.Fatal("incr on an uncached key must not have materialized a cache entry")
	}
}

func TestCacheSetTokenField_UpdatesSingleFieldOnCachedToken(t *testing.T) {
	repoWithMiniRedis(t)

	rawKey := "sk-field-" + common.GetUUID()
	if err := cacheSetToken(Token{Key: rawKey, RemainQuota: 10, Status: common.TokenStatusEnabled}); err != nil {
		t.Fatalf("set: %v", err)
	}

	if err := cacheSetTokenField(rawKey, "Status", "2"); err != nil {
		t.Fatalf("cacheSetTokenField: %v", err)
	}

	got, err := cacheGetTokenByKey(rawKey)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != 2 {
		t.Fatalf("Status=%d want 2 after cacheSetTokenField", got.Status)
	}
	// The untouched field must survive the single-field update.
	if got.RemainQuota != 10 {
		t.Fatalf("RemainQuota=%d want unchanged 10", got.RemainQuota)
	}
}

func TestCacheGetTokenByKey_MissingKeyErrors(t *testing.T) {
	repoWithMiniRedis(t)

	if _, err := cacheGetTokenByKey("sk-does-not-exist-" + common.GetUUID()); err == nil {
		t.Fatal("want error for a key never cached")
	}
}

func TestCacheGetTokenByKey_RedisDisabledErrors(t *testing.T) {
	prevEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prevEnabled })

	if _, err := cacheGetTokenByKey("sk-anything"); err == nil {
		t.Fatal("want error when Redis is disabled")
	}
}

// TestCacheSetToken_ZeroSyncFrequencyLeavesEntryWithoutTTL locks the interaction
// between RedisKeyCacheSeconds()==0 (SyncFrequency unset) and RedisHSetObj's
// "only set TTL if expiration>0" guard: the hash IS written (readable via
// cacheGetTokenByKey) but subsequent incr/field-update calls silently no-op
// because they gate on the key already carrying a TTL.
func TestCacheSetToken_ZeroSyncFrequencyLeavesEntryWithoutTTL(t *testing.T) {
	repoWithMiniRedis(t)
	prevFreq := common.SyncFrequency
	common.SyncFrequency = 0
	t.Cleanup(func() { common.SyncFrequency = prevFreq })

	rawKey := "sk-notl-" + common.GetUUID()
	if err := cacheSetToken(Token{Key: rawKey, RemainQuota: 42}); err != nil {
		t.Fatalf("set: %v", err)
	}

	// The hash itself is readable...
	got, err := cacheGetTokenByKey(rawKey)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RemainQuota != 42 {
		t.Fatalf("RemainQuota=%d want 42", got.RemainQuota)
	}

	// ...but with no TTL set, an incr against it is a silent no-op.
	if err := cacheIncrTokenQuota(rawKey, 8); err != nil {
		t.Fatalf("incr: %v", err)
	}
	got, err = cacheGetTokenByKey(rawKey)
	if err != nil {
		t.Fatalf("get after incr: %v", err)
	}
	if got.RemainQuota != 42 {
		t.Fatalf("RemainQuota=%d want still 42 (incr on a TTL-less key must no-op), got a mutation", got.RemainQuota)
	}
}
