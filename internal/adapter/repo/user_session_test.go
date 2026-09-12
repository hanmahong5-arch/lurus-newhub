package repo

// user_session_test.go — L7 per-device session registry: the 60s last-seen
// throttle (single-process AND N-independent-instance-against-one-Redis-key
// shapes), the session cap, and the pure IP/UA helpers. Drives the real
// UpsertUserSessionSeen/ShouldThrottleSessionTouch functions in
// user_session.go — no hand-built stand-in for the unit under test.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"gorm.io/gorm"
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

// sessionAuditRowWriter implements governance.AuditWriter against a fixed
// *gorm.DB — the repo-package equivalent of handler's pinnedAuditWriter (this
// package has no such helper of its own yet).
type sessionAuditRowWriter struct{ db *gorm.DB }

func (w *sessionAuditRowWriter) CreateAuditEvent(event *entity.AuditEvent) error {
	return w.db.Create(event).Error
}

// pollCapExceededAuditRow polls for the first auth.session_revoked row whose
// details mention cap_exceeded — RecordAuditEvent persists via gopool.Go, so
// the row is not guaranteed to exist the instant the call returns.
func pollCapExceededAuditRow(t *testing.T, timeout time.Duration) *entity.AuditEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		events, _, err := GetAuditEvents("", governance.ActionAuthSessionRevoked, 0, "", 0, 0, 0, 10)
		if err == nil {
			for _, ev := range events {
				if strings.Contains(ev.Details, entity.SessionRevokeReasonCapExceeded) {
					return ev
				}
			}
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestEnforceSessionCap_RevokesOldestBeyondCap: with SESSION_MAX_ACTIVE_PER_USER=2,
// registering a 3rd distinct device revokes the OLDEST (lowest last_seen_at)
// active session with reason "cap_exceeded", leaving exactly 2 active; the
// capped session's Redis key is deleted (same authoritative logout an
// explicit revoke gets) and a durable auth.session_revoked audit row records
// the cap_exceeded reason.
func TestEnforceSessionCap_RevokesOldestBeyondCap(t *testing.T) {
	defer setupSQLiteDB(t)()
	mr := redeemCacheMiniRedis(t)
	ctx := context.Background()

	governance.SetAuditWriter(&sessionAuditRowWriter{db: DB})

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

	// Seed the oldest session's Redis key directly (as if it were a real
	// login) so the deletion assertion below has something to check.
	mr.Set("session_sess-cap-old", "seeded-session-payload")

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

	if mr.Exists("session_sess-cap-old") {
		t.Error("capped session's Redis key still exists — enforceSessionCap must delete it, same as an explicit revoke")
	}

	event := pollCapExceededAuditRow(t, 2*time.Second)
	if event == nil {
		t.Fatal("no auth.session_revoked audit row with reason cap_exceeded appeared")
	}
	if event.ActorType != governance.ActorSystem {
		t.Errorf("cap-exceeded audit row actor_type = %q, want %q", event.ActorType, governance.ActorSystem)
	}
}

// TestShouldThrottleSessionTouch_FallbackWithoutRedis: with common.RedisEnabled
// forced false (the single-process dev/test deployment shape, distinct from
// every other test in this file which runs against a real miniredis), the
// in-process sync.Map fallback throttles a SECOND touch of the same key
// within the window and admits it again once we advance past the window —
// mirrors the Redis-backed guard's own two-call shape in
// TestUserSessionRegistry_UpsertThrottled60s above, but exercises the OTHER
// branch of ShouldThrottleSessionTouch (user_session.go's "Redis reachable
// but erroring... in-process fallback below" comment describes when this
// branch runs).
func TestShouldThrottleSessionTouch_FallbackWithoutRedis(t *testing.T) {
	prevRDB, prevEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = nil, false
	t.Cleanup(func() { common.RDB, common.RedisEnabled = prevRDB, prevEnabled })

	ctx := context.Background()
	key := "sess-fallback-throttle-key"

	if throttled := ShouldThrottleSessionTouch(ctx, key); throttled {
		t.Fatal("first call must not be throttled (no prior touch recorded)")
	}
	if throttled := ShouldThrottleSessionTouch(ctx, key); !throttled {
		t.Fatal("second call within the window must be throttled by the in-process fallback")
	}

	// Fake the elapsed window by backdating the recorded touch directly —
	// the fallback has no clock to fast-forward like miniredis does, so we
	// reach into the package-level map it uses.
	sessionTouchFallback.Store(key, time.Now().Add(-sessionLastSeenThrottleWindow-time.Second))
	if throttled := ShouldThrottleSessionTouch(ctx, key); throttled {
		t.Error("call after the window elapsed must NOT be throttled by the in-process fallback")
	}
}

// TestListActiveUserSessions_90DayWindowMatchesStoreLifetime: a session last
// seen 40 days ago (well past the OLD 30-day list window, well within the
// session store's real 90-day cookie lifetime — cmd/server/main.go's
// sessionOpts.MaxAge) must still be listed: it is a genuinely live,
// revocable session, and hiding it from its own owner would make it
// unrevokable by id.
func TestListActiveUserSessions_90DayWindowMatchesStoreLifetime(t *testing.T) {
	defer setupSQLiteDB(t)()

	now := common.GetTimestamp()
	seedAt := func(key string, lastSeen int64) {
		if err := DB.Create(&entity.UserSession{
			SessionKey: key, UserId: 55, TenantId: "default",
			CreatedAt: lastSeen, LastSeenAt: lastSeen,
		}).Error; err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	seedAt("sess-40d-old", now-int64(40*24*time.Hour/time.Second))
	seedAt("sess-recent", now-60)

	rows, err := ListActiveUserSessions(55)
	if err != nil {
		t.Fatalf("ListActiveUserSessions: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 — a session 40 days old is still within the store's 90-day lifetime and must be listed", len(rows))
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
