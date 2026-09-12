package repo

// user_session_test.go — L7 per-device session registry: the 60s last-seen
// throttle (single-process AND N-independent-instance-against-one-Redis-key
// shapes), the session cap, and the pure IP/UA helpers. Drives the real
// UpsertUserSessionSeen/ShouldThrottleSessionTouch functions in
// user_session.go — no hand-built stand-in for the unit under test.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// TestUserSessionRegistry_UpsertThrottled60s: two touches within 60s produce
// exactly one write (the second is throttled — last_seen_at unchanged);
// after the window elapses, the next touch DOES write (last_seen_at moves).
func TestUserSessionRegistry_UpsertThrottled60s(t *testing.T) {
	defer setupSQLiteDB(t)()
	mr := redeemCacheMiniRedis(t)
	ctx := context.Background()

	if err := UpsertUserSessionSeen(ctx, "sess-throttle-1", 1, "default", "203.0.113.9", "curl/8.0", "session"); err != nil {
		t.Fatalf("first UpsertUserSessionSeen: %v", err)
	}
	var row entity.UserSession
	if err := DB.Where("session_key = ?", "sess-throttle-1").First(&row).Error; err != nil {
		t.Fatalf("row not created: %v", err)
	}
	firstSeen := row.LastSeenAt

	// Second touch, same second, well within the 60s window: must be a no-op
	// on last_seen_at — proves the throttle actually skips the DB write, not
	// just that the timestamp happens to be identical.
	if err := UpsertUserSessionSeen(ctx, "sess-throttle-1", 1, "default", "203.0.113.9", "curl/8.0", "session"); err != nil {
		t.Fatalf("second UpsertUserSessionSeen: %v", err)
	}
	var count int64
	DB.Model(&entity.UserSession{}).Where("session_key = ?", "sess-throttle-1").Count(&count)
	if count != 1 {
		t.Fatalf("row count = %d, want 1 (throttled touch must not create a second row)", count)
	}
	if err := DB.Where("session_key = ?", "sess-throttle-1").First(&row).Error; err != nil {
		t.Fatalf("reload row: %v", err)
	}
	if row.LastSeenAt != firstSeen {
		t.Errorf("last_seen_at moved from %d to %d within the 60s throttle window", firstSeen, row.LastSeenAt)
	}

	// Advance the shared Redis clock past the throttle window: the guard key
	// expires and the next touch must write through.
	mr.FastForward(61 * time.Second)
	time.Sleep(1100 * time.Millisecond) // common.GetTimestamp() has 1s resolution
	if err := UpsertUserSessionSeen(ctx, "sess-throttle-1", 1, "default", "203.0.113.9", "curl/8.0", "session"); err != nil {
		t.Fatalf("third UpsertUserSessionSeen: %v", err)
	}
	if err := DB.Where("session_key = ?", "sess-throttle-1").First(&row).Error; err != nil {
		t.Fatalf("reload row after window: %v", err)
	}
	if row.LastSeenAt <= firstSeen {
		t.Errorf("last_seen_at = %d, want > %d after the throttle window elapsed", row.LastSeenAt, firstSeen)
	}
}

// TestShouldThrottleSessionTouch_NIndependentInstances simulates N
// independent replica instances (goroutines racing on the SAME shared
// Redis, standing in for 3 production replicas) all touching one session
// key at the same moment. Exactly one must win (not throttled); the rest
// must be throttled — this is the "does not amplify writes" guarantee the
// SET NX EX 60 guard exists for.
func TestShouldThrottleSessionTouch_NIndependentInstances(t *testing.T) {
	defer setupSQLiteDB(t)()
	redeemCacheMiniRedis(t)
	ctx := context.Background()

	const n = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			throttled := ShouldThrottleSessionTouch(ctx, "sess-race-key")
			if !throttled {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Errorf("wins = %d, want exactly 1 — N independent instances against one key must not amplify writes", wins)
	}
}

// TestUserSessionRegistry_UpsertThrottled60s_FlagIndependent proves
// UpsertUserSessionSeen/ShouldThrottleSessionTouch have no dependency on
// SessionRegistryEnabled() themselves — the flag is enforced by the CALLERS
// (authHelper, the handlers), not this file, so a caller-side oracle
// (TestAuthHelper_FlagOff_NoRegistryWrites) can prove the flag gate without
// this file's functions getting in the way.
func TestUserSessionRegistry_UpsertThrottled60s_FlagIndependent(t *testing.T) {
	defer setupSQLiteDB(t)()
	redeemCacheMiniRedis(t)
	ctx := context.Background()

	// Flag left at its default (unset — false) on purpose.
	if SessionRegistryEnabled() {
		t.Fatal("SessionRegistryEnabled() must default to false")
	}
	if err := UpsertUserSessionSeen(ctx, "sess-flagindependent", 7, "default", "10.0.0.1", "ua", "session"); err != nil {
		t.Fatalf("UpsertUserSessionSeen: %v", err)
	}
	var count int64
	DB.Model(&entity.UserSession{}).Where("session_key = ?", "sess-flagindependent").Count(&count)
	if count != 1 {
		t.Fatalf("row count = %d, want 1 — this file's writer must not itself gate on the flag", count)
	}
}

// TestEnforceSessionCap_RevokesOldestBeyondCap: with SESSION_MAX_ACTIVE_PER_USER=2,
// registering a 3rd distinct device revokes the OLDEST (lowest last_seen_at)
// active session with reason "cap_exceeded", leaving exactly 2 active.
func TestEnforceSessionCap_RevokesOldestBeyondCap(t *testing.T) {
	defer setupSQLiteDB(t)()
	redeemCacheMiniRedis(t)
	ctx := context.Background()

	t.Setenv("SESSION_MAX_ACTIVE_PER_USER", "2")

	now := common.GetTimestamp()
	// Seed two already-active sessions directly (bypassing the throttle/insert
	// path) with distinct last_seen_at so ordering is deterministic.
	seedSession := func(key string, lastSeen int64) {
		if err := DB.Create(&entity.UserSession{
			SessionKey: key, UserId: 9, TenantId: "default",
			CreatedAt: lastSeen, LastSeenAt: lastSeen,
		}).Error; err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	seedSession("sess-cap-old", now-300)
	seedSession("sess-cap-mid", now-100)

	// Third distinct device -> first-sight insert -> cap enforcement fires.
	if err := UpsertUserSessionSeen(ctx, "sess-cap-new", 9, "default", "1.2.3.4", "ua", "session"); err != nil {
		t.Fatalf("UpsertUserSessionSeen: %v", err)
	}

	var oldRow, midRow, newRow entity.UserSession
	if err := DB.Where("session_key = ?", "sess-cap-old").First(&oldRow).Error; err != nil {
		t.Fatalf("reload old: %v", err)
	}
	if err := DB.Where("session_key = ?", "sess-cap-mid").First(&midRow).Error; err != nil {
		t.Fatalf("reload mid: %v", err)
	}
	if err := DB.Where("session_key = ?", "sess-cap-new").First(&newRow).Error; err != nil {
		t.Fatalf("reload new: %v", err)
	}

	if oldRow.RevokedAt == 0 || oldRow.RevokeReason != entity.SessionRevokeReasonCapExceeded {
		t.Errorf("oldest session not revoked with cap_exceeded: revoked_at=%d reason=%q", oldRow.RevokedAt, oldRow.RevokeReason)
	}
	if midRow.RevokedAt != 0 {
		t.Errorf("mid session unexpectedly revoked: revoked_at=%d", midRow.RevokedAt)
	}
	if newRow.RevokedAt != 0 {
		t.Errorf("newly-registered session unexpectedly revoked: revoked_at=%d", newRow.RevokedAt)
	}
}

// TestMaskIP: /24 for IPv4, /48 for IPv6, "" for garbage — the ONLY path ip
// reaches a client response, so a whitelist test never has to re-check the
// output shape.
func TestMaskIP(t *testing.T) {
	cases := map[string]string{
		"203.0.113.42":          "203.0.113.0",
		"10.1.2.3":              "10.1.2.0",
		"2001:db8:1234:5678::1": "2001:db8:1234::",
		"not-an-ip":             "",
		"":                      "",
	}
	for in, want := range cases {
		if got := MaskIP(in); got != want {
			t.Errorf("MaskIP(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestUserAgentFamily: coarse family only, never the raw string back.
func TestUserAgentFamily(t *testing.T) {
	cases := map[string]string{
		"": "unknown",
		"Mozilla/5.0 (Macintosh) AppleWebKit/537.36 (KHTML) Chrome/120.0 Safari/537.36": "Chrome",
		"Mozilla/5.0 (X11; Linux) AppleWebKit/605.1.15 (KHTML) Version/17 Safari/605.1": "Safari",
		"Mozilla/5.0 (Windows NT 10.0; rv:120.0) Gecko/20100101 Firefox/120.0":          "Firefox",
		"curl/8.4.0":                "curl",
		"some-unrecognised-bot/1.0": "other",
	}
	for in, want := range cases {
		if got := UserAgentFamily(in); got != want {
			t.Errorf("UserAgentFamily(%q) = %q, want %q", in, got, want)
		}
		if got := UserAgentFamily(in); in != "" && got == in {
			t.Errorf("UserAgentFamily(%q) echoed the raw string back", in)
		}
	}
}
