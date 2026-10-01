package common

import (
	"net"
	"testing"
	"time"
)

// Redis is soft everywhere else (health treats it as degraded, not down), but
// an unreachable Redis at boot used to FatalLog -> os.Exit(1) after the retry
// budget, so a Redis blip during a rollout crash-looped the whole gateway.
// InitRedisClient must now return the error and leave the already-built client
// in place: ~20 call sites dereference RDB whenever RedisEnabled is true, and
// go-redis reconnects per command, so the client heals once Redis is back.
func TestInitRedisClient_UnreachableIsSoftAndKeepsClient(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing listens here any more

	prevRDB, prevEnabled := RDB, RedisEnabled
	prevRetries, prevBase, prevMax, prevPing := DBConnectRetries, DBConnectRetryBaseDelay, DBConnectRetryMaxDelay, DBConnectPingTimeout
	t.Cleanup(func() {
		if RDB != nil && RDB != prevRDB {
			_ = RDB.Close()
		}
		RDB, RedisEnabled = prevRDB, prevEnabled
		DBConnectRetries, DBConnectRetryBaseDelay, DBConnectRetryMaxDelay, DBConnectPingTimeout = prevRetries, prevBase, prevMax, prevPing
	})
	RedisEnabled = true
	DBConnectRetries = 2
	DBConnectRetryBaseDelay = time.Millisecond
	DBConnectRetryMaxDelay = 2 * time.Millisecond
	DBConnectPingTimeout = 200 * time.Millisecond

	t.Setenv("REDIS_CONN_STRING", "redis://"+addr)
	t.Setenv("SYNC_FREQUENCY", "60")

	if err := InitRedisClient(); err == nil {
		t.Fatal("InitRedisClient against a closed port returned nil, want the connect error")
	}
	if RDB == nil {
		t.Error("RDB is nil after a failed boot ping; call sites dereference it when RedisEnabled")
	}
	if !RedisEnabled {
		t.Error("RedisEnabled was cleared; the client is kept so it can reconnect later")
	}
}
