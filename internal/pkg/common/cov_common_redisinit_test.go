package common

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

// TestInitRedisClient_DefaultsSyncFrequencyWhenUnset covers the SYNC_FREQUENCY-
// unset branch: InitRedisClient must fall back to 60 without erroring, whereas
// TestInitRedisClient_Success (elsewhere in the suite) always sets it and never
// exercises this default-fill path.
func TestInitRedisClient_DefaultsSyncFrequencyWhenUnset(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)

	prevRDB, prevEnabled, prevSync := RDB, RedisEnabled, SyncFrequency
	t.Cleanup(func() {
		if RDB != nil {
			_ = RDB.Close()
		}
		RDB, RedisEnabled, SyncFrequency = prevRDB, prevEnabled, prevSync
	})
	RedisEnabled = true
	SyncFrequency = 999 // sentinel that must be overwritten by the default-fill

	t.Setenv("REDIS_CONN_STRING", "redis://"+mr.Addr())
	t.Setenv("SYNC_FREQUENCY", "")

	if err := InitRedisClient(); err != nil {
		t.Fatalf("InitRedisClient: %v", err)
	}
	if SyncFrequency != 60 {
		t.Errorf("SyncFrequency = %d, want default 60 when SYNC_FREQUENCY unset", SyncFrequency)
	}
}

// TestInitRedisClient_DebugLogsAndPoolSizeOverride covers the DebugEnabled
// branch (extra connection-detail SysLog calls) and the REDIS_POOL_SIZE env
// override, asserting the resulting client actually uses the configured pool
// size rather than silently keeping the hardcoded default of 10.
func TestInitRedisClient_DebugLogsAndPoolSizeOverride(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)

	prevRDB, prevEnabled, prevDebug := RDB, RedisEnabled, DebugEnabled
	t.Cleanup(func() {
		if RDB != nil {
			_ = RDB.Close()
		}
		RDB, RedisEnabled, DebugEnabled = prevRDB, prevEnabled, prevDebug
	})
	RedisEnabled = true
	DebugEnabled = true

	t.Setenv("REDIS_CONN_STRING", "redis://"+mr.Addr())
	t.Setenv("SYNC_FREQUENCY", "60")
	t.Setenv("REDIS_POOL_SIZE", "37")

	if err := InitRedisClient(); err != nil {
		t.Fatalf("InitRedisClient: %v", err)
	}
	if RDB.Options().PoolSize != 37 {
		t.Errorf("PoolSize = %d, want 37 from REDIS_POOL_SIZE override", RDB.Options().PoolSize)
	}
	if err := RDB.Ping(context.Background()).Err(); err != nil {
		t.Errorf("client built under DebugEnabled should still be usable: %v", err)
	}
}

// TestParseRedisOption_DatabaseIndexParsed covers the DB-index portion of the
// URL that TestRedisKeyCacheSecondsAndParseOption (elsewhere in the suite)
// leaves at the implicit default of 0.
func TestParseRedisOption_DatabaseIndexParsed(t *testing.T) {
	t.Setenv("REDIS_CONN_STRING", "redis://cache-host:6380/5")
	opt := ParseRedisOption()
	if opt == nil {
		t.Fatal("ParseRedisOption returned nil")
	}
	if opt.Addr != "cache-host:6380" {
		t.Errorf("Addr = %q, want cache-host:6380", opt.Addr)
	}
	if opt.DB != 5 {
		t.Errorf("DB = %d, want 5", opt.DB)
	}
}
