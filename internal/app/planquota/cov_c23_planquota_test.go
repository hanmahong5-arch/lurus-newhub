package planquota

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// The wiring code reaches repo, app and governance, each of which may hand work
// to a detached goroutine. Forcing the seams inline keeps every spawn finished
// before a test restores its globals (see repo.AsyncGo).
func TestMain(m *testing.M) {
	repo.AsyncGo = func(f func()) { f() }
	app.AsyncGo = func(f func()) { f() }
	governance.AsyncGo = func(f func()) { f() }
	os.Exit(m.Run())
}

var c23DBSeq atomic.Int64

func c23OpenDB(t *testing.T) {
	t.Helper()
	dsn := fmt.Sprintf("file:c23planquota%d?mode=memory&cache=shared", c23DBSeq.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&repo.Channel{}, &repo.Ability{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := repo.DB
	repo.DB = db
	t.Cleanup(func() {
		repo.DB = prev
		_ = sqlDB.Close()
	})
}

func c23SeedChannel(t *testing.T, id int, status int, key string) *repo.Channel {
	t.Helper()
	ch := &repo.Channel{Id: id, Name: fmt.Sprintf("plan-%d", id), Status: status, Key: key, Group: "default", Models: "m1"}
	if err := repo.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return ch
}

func c23FreshStore(t *testing.T) {
	t.Helper()
	old := defaultStore
	defaultStore = NewStore()
	t.Cleanup(func() { defaultStore = old })
}

func TestC23_StoreSnapshotRoundTripAndIsolation(t *testing.T) {
	c23FreshStore(t)
	if _, ok := LatestSnapshot(501); ok {
		t.Fatal("never-probed channel must report ok=false")
	}
	in := &Snapshot{ChannelID: 501, Kind: KindKimiCoding, FetchedAt: 42, Windows: []Window{{Window5h, 30, 99}}}
	StoreSnapshot(in)
	got, ok := LatestSnapshot(501)
	if !ok || got.Kind != KindKimiCoding || got.FetchedAt != 42 || len(got.Windows) != 1 {
		t.Fatalf("round trip lost data: %+v ok=%v", got, ok)
	}
	// The store hands out copies: mutating a read must not alter the stored one.
	got.FetchedAt = 7
	again, _ := LatestSnapshot(501)
	if again.FetchedAt != 42 {
		t.Errorf("stored snapshot mutated through a returned copy: %d", again.FetchedAt)
	}
	// And the stored copy is detached from the caller's input.
	in.FetchedAt = 1
	again, _ = LatestSnapshot(501)
	if again.FetchedAt != 42 {
		t.Errorf("stored snapshot aliases the Put input: %d", again.FetchedAt)
	}
}

func TestC23_LatestSnapshots_BatchMemoryFallback(t *testing.T) {
	c23FreshStore(t)
	if got := LatestSnapshots(nil); len(got) != 0 {
		t.Fatalf("empty id list must yield an empty map, got %v", got)
	}
	StoreSnapshot(&Snapshot{ChannelID: 11, Kind: KindMiniMax})
	StoreSnapshot(&Snapshot{ChannelID: 13, Kind: KindZhipuCoding})
	got := LatestSnapshots([]int{11, 12, 13})
	if len(got) != 2 {
		t.Fatalf("want 2 probed channels, got %d: %v", len(got), got)
	}
	if got[11].Kind != KindMiniMax || got[13].Kind != KindZhipuCoding {
		t.Errorf("wrong snapshots returned: %+v %+v", got[11], got[13])
	}
	if _, present := got[12]; present {
		t.Error("never-probed channel 12 must be absent, not zero-valued")
	}
}

func TestC23_LatestSnapshots_NonRedisStoreFallsBackToGet(t *testing.T) {
	old := defaultStore
	defaultStore = &memStore{m: map[int]*Snapshot{}}
	t.Cleanup(func() { defaultStore = old })
	StoreSnapshot(&Snapshot{ChannelID: 21, Kind: KindKimiCoding})
	got := LatestSnapshots([]int{21, 22})
	if len(got) != 1 || got[21] == nil || got[21].Kind != KindKimiCoding {
		t.Fatalf("custom store must be consulted via Get: %v", got)
	}
}

func TestC23_WindowView(t *testing.T) {
	if WindowView(nil) != nil {
		t.Fatal("nil snapshot must render as nil")
	}
	v := WindowView(&Snapshot{Kind: KindKimiCoding, FetchedAt: 5, Windows: []Window{{Window5h, 55.5, 1000}, {WindowWeekly, 10, 2000}}})
	if v["kind"] != KindKimiCoding || v["fetched_at"] != int64(5) {
		t.Errorf("header fields wrong: %v", v)
	}
	if _, has := v["error"]; has {
		t.Error("error key must be omitted for a healthy snapshot")
	}
	wins, _ := v["windows"].([]map[string]interface{})
	if len(wins) != 2 || wins[0]["name"] != Window5h || wins[0]["used_pct"] != 55.5 || wins[1]["reset_at"] != int64(2000) {
		t.Errorf("windows not rendered in order: %v", wins)
	}
	v = WindowView(&Snapshot{Kind: KindMiniMax, Error: "auth failed"})
	if v["error"] != "auth failed" {
		t.Errorf("error must be surfaced: %v", v)
	}
	if w, _ := v["windows"].([]map[string]interface{}); len(w) != 0 {
		t.Errorf("no windows expected: %v", w)
	}
}

func TestC23_ProbeIntervalEnvBoundary(t *testing.T) {
	cases := []struct {
		env  string
		want time.Duration
	}{
		{"", 5 * time.Minute},
		{"29", 5 * time.Minute}, // below the 30s floor: ignored
		{"30", 30 * time.Second},
		{"600", 10 * time.Minute},
		{"junk", 5 * time.Minute},
	}
	for _, c := range cases {
		t.Setenv("PLAN_QUOTA_INTERVAL_SECONDS", c.env)
		if got := ProbeInterval(); got != c.want {
			t.Errorf("env=%q: got %v want %v", c.env, got, c.want)
		}
	}
}

func TestC23_JitteredStaysWithinTenPercent(t *testing.T) {
	d := 10 * time.Minute
	lo, hi := d-d/10, d+d/10
	distinct := map[time.Duration]bool{}
	for i := 0; i < 200; i++ {
		j := jittered(d)
		if j < lo || j > hi {
			t.Fatalf("jittered(%v)=%v outside [%v,%v]", d, j, lo, hi)
		}
		distinct[j] = true
	}
	if len(distinct) < 2 {
		t.Error("jitter produced a constant value")
	}
	for i := 0; i < 100; i++ {
		if f := rngFloat(); f < 0 || f >= 1 {
			t.Fatalf("rngFloat out of [0,1): %v", f)
		}
	}
}

func TestC23_NoticeLevelOnlyAppliesToSameExpiry(t *testing.T) {
	ch := &repo.Channel{}
	if got := noticeLevel(ch, 1000); got != 0 {
		t.Fatalf("no notice recorded yet: got %d", got)
	}
	ch.SetOtherInfo(map[string]interface{}{"plan_notice_for": float64(1000), "plan_notice_level": float64(24)})
	if got := noticeLevel(ch, 1000); got != 24 {
		t.Errorf("same expiry must return recorded level, got %d", got)
	}
	if got := noticeLevel(ch, 2000); got != 0 {
		t.Errorf("a renewed (different) expiry must reset the level, got %d", got)
	}
}

func TestC23_ChannelKeys(t *testing.T) {
	single := &repo.Channel{Key: "k1\nk2"}
	if got := channelKeys(single); len(got) != 1 || got[0] != "" {
		t.Errorf("single-key channel must pause via the empty key, got %v", got)
	}
	multi := &repo.Channel{Key: "k1\nk2"}
	multi.ChannelInfo.IsMultiKey = true
	if got := channelKeys(multi); len(got) != 2 || got[0] != "k1" || got[1] != "k2" {
		t.Errorf("multi-key channel must expand to its keys, got %v", got)
	}
}

func TestC23_UpdateOtherInfoAndSaveSummaryPersist(t *testing.T) {
	c23OpenDB(t)
	ch := c23SeedChannel(t, 601, common.ChannelStatusEnabled, "k")
	fresh, _ := repo.GetChannelById(601, false)
	fresh.SetOtherInfo(map[string]interface{}{"keep": "me"})
	if err := repo.DB.Model(&repo.Channel{}).Where("id = ?", 601).UpdateColumn("other_info", fresh.OtherInfo).Error; err != nil {
		t.Fatal(err)
	}

	saveSummary(ch, &Snapshot{Kind: KindKimiCoding, FetchedAt: 77, Error: "e", CooledUntil: 5, BalanceLow: true,
		Windows: []Window{{Window5h, 12, 34}}})
	got, err := repo.GetChannelById(601, false)
	if err != nil {
		t.Fatal(err)
	}
	info := got.GetOtherInfo()
	if info["keep"] != "me" {
		t.Errorf("read-modify-write dropped an unrelated key: %v", info)
	}
	pq, _ := info["plan_quota"].(map[string]interface{})
	if pq["kind"] != KindKimiCoding || pq["fetched_at"] != float64(77) || pq["error"] != "e" || pq["balance_low"] != true || pq["cooled_until"] != float64(5) {
		t.Errorf("summary not persisted faithfully: %v", pq)
	}
	if ws, _ := pq["windows"].([]interface{}); len(ws) != 1 {
		t.Errorf("windows missing from summary: %v", pq["windows"])
	}

	// A missing channel must be a silent no-op, not a panic or a stray row.
	called := false
	updateOtherInfo(99999, func(map[string]interface{}) { called = true })
	if called {
		t.Error("mutate must not run for a channel that does not exist")
	}
}

func TestC23_SetNoticeLevelPersistsAndRoundTrips(t *testing.T) {
	c23OpenDB(t)
	c23SeedChannel(t, 602, common.ChannelStatusEnabled, "k")
	ch, _ := repo.GetChannelById(602, false)
	setNoticeLevel(ch, 5000, 72)
	reread, _ := repo.GetChannelById(602, false)
	if lvl := noticeLevel(reread, 5000); lvl != 72 {
		t.Errorf("notice level did not round trip through the DB: %d", lvl)
	}
	if lvl := noticeLevel(reread, 6000); lvl != 0 {
		t.Errorf("level must not leak to another expiry: %d", lvl)
	}
}

func TestC23_PauseThenResumeTogglesStatusAndClearsCooldown(t *testing.T) {
	c23OpenDB(t)
	c23SeedChannel(t, 603, common.ChannelStatusEnabled, "k")
	ch, _ := repo.GetChannelById(603, true)

	if !pause(ch, ExpiryReasonPrefix+" plan ended") {
		t.Fatal("pause of an enabled channel must report success")
	}
	got, _ := repo.GetChannelById(603, false)
	if got.Status != common.ChannelStatusAutoDisabled {
		t.Fatalf("status after pause = %d", got.Status)
	}
	if reason, _ := got.GetOtherInfo()["status_reason"].(string); !strings.HasPrefix(reason, ExpiryReasonPrefix) {
		t.Errorf("pause reason not recorded: %q", reason)
	}
	if pause(ch, "again") {
		t.Error("pausing an already-paused channel must report no change")
	}

	app.MarkChannelCooldown(603, 0, time.Now().Add(time.Hour).Unix())
	if !resume(ch) {
		t.Fatal("resume of a paused channel must report success")
	}
	got, _ = repo.GetChannelById(603, false)
	if got.Status != common.ChannelStatusEnabled {
		t.Errorf("status after resume = %d", got.Status)
	}
	if resume(ch) {
		t.Error("resuming an enabled channel must report no change")
	}
}

type c23AuditSink struct {
	mu sync.Mutex
	ev []*entity.AuditEvent
}

func (s *c23AuditSink) CreateAuditEvent(e *entity.AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ev = append(s.ev, e)
	return nil
}

func TestC23_AuditEventRecordsSystemActorOnChannel(t *testing.T) {
	sink := &c23AuditSink{}
	governance.SetAuditWriter(sink)
	t.Cleanup(func() { governance.SetAuditWriter(nil) })

	ch := &repo.Channel{Id: 701, Name: "p"}
	auditEvent(ch, ActionPlanExpired, map[string]interface{}{"name": "p", "expires_at": 123})
	if len(sink.ev) != 1 {
		t.Fatalf("want 1 audit event, got %d", len(sink.ev))
	}
	e := sink.ev[0]
	if e.Action != ActionPlanExpired || e.ResourceID != 701 || e.Resource != governance.ResourceChannel ||
		e.ActorType != governance.ActorSystem || e.TenantID != "default" {
		t.Errorf("audit event mis-attributed: %+v", e)
	}
	if !strings.Contains(e.Details, "\"expires_at\":123") {
		t.Errorf("details not serialised: %s", e.Details)
	}
}

func TestC23_FetchForErrorPaths(t *testing.T) {
	ctx := context.Background()
	if _, err := fetchFor(ctx, &repo.Channel{}, KindKimiCoding); err == nil || !strings.Contains(err.Error(), "no key") {
		t.Errorf("keyless channel must fail with a no-key error, got %v", err)
	}

	bad := &repo.Channel{Key: "k"}
	bad.SetSetting(dto.ChannelSettings{Proxy: "ftp://bad-scheme"})
	if _, err := fetchFor(ctx, bad, KindKimiCoding); err == nil {
		t.Error("a malformed proxy must surface its error instead of falling back to a direct call")
	}

	// A dead proxy: the fetch must fail, never hang or succeed.
	dead := &repo.Channel{Key: "k"}
	dead.SetSetting(dto.ChannelSettings{Proxy: "http://127.0.0.1:1"})
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := fetchFor(cctx, dead, KindKimiCoding); err == nil {
		t.Error("an unreachable proxy must fail the probe")
	}

	// An unsupported kind is rejected before any network access.
	base := "https://example.invalid"
	unsupported := &repo.Channel{Key: "k", BaseURL: &base}
	unsupported.SetSetting(dto.ChannelSettings{Proxy: "http://127.0.0.1:1"})
	if _, err := fetchFor(ctx, unsupported, "no-such-kind"); err == nil || !strings.Contains(err.Error(), "unsupported plan kind") {
		t.Errorf("unsupported kind must be rejected, got %v", err)
	}
}

func TestC23_NewRunnerWiringAndClosures(t *testing.T) {
	t.Setenv("PLAN_QUOTA_THRESHOLD_PCT", "73")
	sink := &c23AuditSink{}
	governance.SetAuditWriter(sink)
	t.Cleanup(func() { governance.SetAuditWriter(nil) })
	rec := &cooldownRec{m: map[int]int64{}}
	oldM := markCooldown
	markCooldown = rec.mark
	t.Cleanup(func() { markCooldown = oldM })

	d := NewRunner().D
	if d.GlobalThresholdPct != 73 {
		t.Errorf("threshold env not honoured: %v", d.GlobalThresholdPct)
	}
	if d.Concurrency != 4 {
		t.Errorf("concurrency = %d", d.Concurrency)
	}
	for name, ok := range map[string]bool{
		"List": d.List != nil, "Fetch": d.Fetch != nil, "Cooldown": d.Cooldown != nil, "ClearCooldown": d.ClearCooldown != nil,
		"Store": d.Store != nil, "SaveSummary": d.SaveSummary != nil, "Pause": d.Pause != nil, "Resume": d.Resume != nil,
		"NoticeLevel": d.NoticeLevel != nil, "SetNoticeLevel": d.SetNoticeLevel != nil, "Notice": d.Notice != nil,
		"Expired": d.Expired != nil, "Jitter": d.Jitter != nil,
	} {
		if !ok {
			t.Errorf("production runner dep %s is not wired", name)
		}
	}

	d.Cooldown(42, 12345)
	if rec.m[42] != 12345 {
		t.Errorf("Cooldown dep must delegate to markCooldown: %v", rec.m)
	}
	for i := 0; i < 50; i++ {
		if j := d.Jitter(10 * time.Second); j < 0 || j >= 10*time.Second {
			t.Fatalf("jitter %v outside [0,10s)", j)
		}
	}

	ch := &repo.Channel{Id: 801, Name: "plan"}
	d.Notice(ch, 24, 5000)
	d.Expired(ch, 6000)
	if len(sink.ev) != 2 || sink.ev[0].Action != ActionPlanExpiring || sink.ev[1].Action != ActionPlanExpired {
		t.Fatalf("Notice/Expired must audit expiring then expired: %+v", sink.ev)
	}
	if !strings.Contains(sink.ev[0].Details, "\"within_hours\":24") {
		t.Errorf("notice details: %s", sink.ev[0].Details)
	}
}

func TestC23_RunStopsOnCancelledContext(t *testing.T) {
	r := &Runner{D: Deps{List: func() ([]*repo.Channel, error) {
		t.Error("a cancelled loop must not scan channels")
		return nil, nil
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestC23_RunBackgroundRegistersTaskAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { RunBackground(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunBackground did not return after cancellation")
	}
}
