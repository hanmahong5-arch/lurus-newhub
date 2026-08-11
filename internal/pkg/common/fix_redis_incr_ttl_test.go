package common

// fix_redis_incr_ttl_test.go — 回归测试：TTL 为 -1（key 存在但没有过期时间）时，
// RedisIncr/RedisHIncrBy/RedisHSetField 曾经直接 return nil，把写入静默丢弃。
// 只要有调用方把 key 写成永不过期（例如限流窗口被配成 0），计数/缓存就再也不会
// 更新，而调用方拿到的仍是 nil（“成功”）。

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// fixRedisTTLMiniRedis 把 common.RDB 指向进程内的 miniredis，测试结束后恢复。
func fixRedisTTLMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prevRDB, prevEnabled := RDB, RedisEnabled
	RDB, RedisEnabled = client, true
	t.Cleanup(func() {
		RDB, RedisEnabled = prevRDB, prevEnabled
		_ = client.Close()
		mr.Close()
	})
	return mr
}

// TestFixRedisIncr_PersistentKeyIsIncremented 锁定修复后的行为：没有 TTL 的 key
// 照常自增，且不会因此被塞进一个过期时间。修复前该调用是彻底的空操作（值保持 3）。
func TestFixRedisIncr_PersistentKeyIsIncremented(t *testing.T) {
	fixRedisTTLMiniRedis(t)
	ctx := context.Background()

	if err := RDB.Set(ctx, "fixttl:ctr", 3, 0).Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := RedisIncr(ctx, "fixttl:ctr", 5); err != nil {
		t.Fatalf("RedisIncr: %v", err)
	}
	if v, err := RDB.Get(ctx, "fixttl:ctr").Int64(); err != nil || v != 8 {
		t.Errorf("counter = %d (err=%v), want 8", v, err)
	}
	// 自增不得给持久 key 引入过期时间
	if ttl := RDB.TTL(ctx, "fixttl:ctr").Val(); ttl >= 0 {
		t.Errorf("persistent key gained a TTL: %v", ttl)
	}
}

// TestFixRedisIncr_MissingKeyStaysAbsent 保证修复没有顺带改变“key 不存在时不创建”
// 的既有语义（TTL 返回 -2 的分支）。
func TestFixRedisIncr_MissingKeyStaysAbsent(t *testing.T) {
	fixRedisTTLMiniRedis(t)
	ctx := context.Background()

	if err := RedisIncr(ctx, "fixttl:absent", 1); err != nil {
		t.Fatalf("RedisIncr on missing key: %v", err)
	}
	if n := RDB.Exists(ctx, "fixttl:absent").Val(); n != 0 {
		t.Errorf("missing key must not be created, exists=%d", n)
	}
}

// TestFixRedisHash_PersistentKeyIsWritten 覆盖同文件里同一形态的另外两个函数：
// 修复前，配额/令牌缓存的 hash 一旦没有 TTL，扣减与字段更新都会被静默丢弃。
func TestFixRedisHash_PersistentKeyIsWritten(t *testing.T) {
	fixRedisTTLMiniRedis(t)
	ctx := context.Background()

	if err := RDB.HSet(ctx, "fixttl:h", "Quota", 100).Err(); err != nil {
		t.Fatalf("seed hash: %v", err)
	}
	if ttl := RDB.TTL(ctx, "fixttl:h").Val(); ttl >= 0 {
		t.Fatalf("precondition: hash must have no TTL, got %v", ttl)
	}

	if err := RedisHIncrBy(ctx, "fixttl:h", "Quota", -30); err != nil {
		t.Fatalf("RedisHIncrBy: %v", err)
	}
	if v, err := RDB.HGet(ctx, "fixttl:h", "Quota").Int64(); err != nil || v != 70 {
		t.Errorf("Quota = %d (err=%v), want 70", v, err)
	}

	if err := RedisHSetField(ctx, "fixttl:h", "Status", "2"); err != nil {
		t.Fatalf("RedisHSetField: %v", err)
	}
	if v := RDB.HGet(ctx, "fixttl:h", "Status").Val(); v != "2" {
		t.Errorf("Status = %q, want \"2\"", v)
	}
	if ttl := RDB.TTL(ctx, "fixttl:h").Val(); ttl >= 0 {
		t.Errorf("persistent hash gained a TTL: %v", ttl)
	}
}

// TestFixRedisIncr_KeyWithTTLKeepsIt 守住原有的主路径：有 TTL 的 key 自增后 TTL 仍在。
func TestFixRedisIncr_KeyWithTTLKeepsIt(t *testing.T) {
	fixRedisTTLMiniRedis(t)
	ctx := context.Background()

	if err := RDB.Set(ctx, "fixttl:win", 1, 2*time.Minute).Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := RedisIncr(ctx, "fixttl:win", 1); err != nil {
		t.Fatalf("RedisIncr: %v", err)
	}
	if v, _ := RDB.Get(ctx, "fixttl:win").Int64(); v != 2 {
		t.Errorf("counter = %d, want 2", v)
	}
	if ttl := RDB.TTL(ctx, "fixttl:win").Val(); ttl <= 0 || ttl > 2*time.Minute {
		t.Errorf("TTL not preserved: %v", ttl)
	}
}
