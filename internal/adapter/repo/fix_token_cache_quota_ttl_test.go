package repo

// fix_token_cache_quota_ttl_test.go — 回归测试：token 缓存条目没有 TTL 时
// （SYNC_FREQUENCY=0，cacheSetToken 写入的 hash 不带过期时间），配额扣减
// 曾经被静默丢弃：cacheDecrTokenQuota 返回 nil，却一个字节都没写。
// 于是 ValidateUserToken 一直读到旧的 RemainQuota，令牌永远撞不到耗尽闸门。
// 这里用进程内 Redis 真实读回改动值，不以 err == nil 当作成功。

import (
	"context"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// fixTokenQuotaMiniRedis 把 common.RDB 指向进程内 Redis，并把 SyncFrequency
// 设为调用方给定值（0 => RedisKeyCacheSeconds() == 0 => 缓存 hash 无 TTL）。
// 结束时恢复所有全局量。全程无网络、无外部依赖，可在 -short 下运行。
func fixTokenQuotaMiniRedis(t *testing.T, syncFrequency int) *miniredis.Miniredis {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prevRDB, prevEnabled, prevSync := common.RDB, common.RedisEnabled, common.SyncFrequency
	common.RDB, common.RedisEnabled, common.SyncFrequency = client, true, syncFrequency
	t.Cleanup(func() {
		common.RDB, common.RedisEnabled, common.SyncFrequency = prevRDB, prevEnabled, prevSync
		_ = client.Close()
		mr.Close()
	})
	return mr
}

// fixTokenQuotaHashKey 返回 token 缓存 hash 的真实 key。
func fixTokenQuotaHashKey(rawKey string) string {
	return "token:" + common.GenerateHMAC(rawKey)
}

// TestFixCacheDecrTokenQuota_AppliesOnEntryWithoutTTL 锁住核心缺陷：
// 无 TTL 的缓存条目上，扣减必须真正落到 Redis。
func TestFixCacheDecrTokenQuota_AppliesOnEntryWithoutTTL(t *testing.T) {
	fixTokenQuotaMiniRedis(t, 0)
	ctx := context.Background()

	tok := Token{
		Id:          9101,
		UserId:      91,
		Key:         "sk-fix-ttlless-quota",
		Status:      common.TokenStatusEnabled,
		Name:        "fix-ttlless",
		RemainQuota: 1000,
	}
	if err := cacheSetToken(tok); err != nil {
		t.Fatalf("cacheSetToken: %v", err)
	}

	// 前置条件：这个 hash 确实存在且没有过期时间（TTL 返回 -1）。
	if ttl := common.RDB.TTL(ctx, fixTokenQuotaHashKey(tok.Key)).Val(); ttl != -1 {
		t.Fatalf("precondition: TTL = %v, want -1 (键存在但无过期时间)", ttl)
	}

	if err := cacheDecrTokenQuota(tok.Key, 400); err != nil {
		t.Fatalf("cacheDecrTokenQuota: %v", err)
	}

	got, err := cacheGetTokenByKey(tok.Key)
	if err != nil {
		t.Fatalf("cacheGetTokenByKey: %v", err)
	}
	if got.RemainQuota != 600 {
		t.Fatalf("扣减后 RemainQuota = %d, want 600（写入被静默丢弃）", got.RemainQuota)
	}

	// 反向补充：加回配额同样必须生效。
	if err := cacheIncrTokenQuota(tok.Key, 150); err != nil {
		t.Fatalf("cacheIncrTokenQuota: %v", err)
	}
	got, err = cacheGetTokenByKey(tok.Key)
	if err != nil {
		t.Fatalf("cacheGetTokenByKey after incr: %v", err)
	}
	if got.RemainQuota != 750 {
		t.Fatalf("回补后 RemainQuota = %d, want 750", got.RemainQuota)
	}
}

// TestFixCacheSetTokenField_AppliesOnEntryWithoutTTL 覆盖同一类写入丢弃：
// 无 TTL 条目上的字段更新（如禁用令牌）也必须真正写进缓存。
func TestFixCacheSetTokenField_AppliesOnEntryWithoutTTL(t *testing.T) {
	fixTokenQuotaMiniRedis(t, 0)

	tok := Token{
		Id:     9102,
		UserId: 92,
		Key:    "sk-fix-ttlless-field",
		Status: common.TokenStatusEnabled,
		Name:   "before",
	}
	if err := cacheSetToken(tok); err != nil {
		t.Fatalf("cacheSetToken: %v", err)
	}

	if err := cacheSetTokenField(tok.Key, "Name", "after"); err != nil {
		t.Fatalf("cacheSetTokenField: %v", err)
	}
	got, err := cacheGetTokenByKey(tok.Key)
	if err != nil {
		t.Fatalf("cacheGetTokenByKey: %v", err)
	}
	if got.Name != "after" {
		t.Fatalf("字段更新后 Name = %q, want \"after\"（写入被静默丢弃）", got.Name)
	}
}

// TestFixCacheIncrTokenQuota_UncachedKeyStaysAbsent 守住有意保留的行为：
// 缓存里根本没有这个 token 时不能写入，否则会造出只有 RemainQuota 一个字段
// 的残缺 hash，后续读取会拿到其它字段全零的“令牌”。缺失即回落数据库。
func TestFixCacheIncrTokenQuota_UncachedKeyStaysAbsent(t *testing.T) {
	fixTokenQuotaMiniRedis(t, 60)
	ctx := context.Background()

	const rawKey = "sk-fix-never-cached"
	if err := cacheIncrTokenQuota(rawKey, 500); err != nil {
		t.Fatalf("cacheIncrTokenQuota on absent key: %v", err)
	}
	if n, err := common.RDB.Exists(ctx, fixTokenQuotaHashKey(rawKey)).Result(); err != nil || n != 0 {
		t.Fatalf("未缓存的 token 不应被创建: exists=%d err=%v", n, err)
	}
	if _, err := cacheGetTokenByKey(rawKey); err == nil {
		t.Fatal("未缓存的 token 读取应报错（回落数据库），而非返回残缺对象")
	}
}
