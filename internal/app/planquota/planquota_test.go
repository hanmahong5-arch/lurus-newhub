package planquota

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func sample(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParse(t *testing.T) {
	kimiWeekly := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC).Unix()
	kimi5h := time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC).Unix()
	cases := []struct {
		name    string
		kind    string
		file    string
		want    []Window
		wantErr bool
	}{
		{"zhipu by unit", KindZhipuCoding, "zhipu_units.json",
			[]Window{{Window5h, 96, 1790003600}, {WindowWeekly, 40, 1790600000}}, false},
		{"zhipu without unit orders by reset", KindZhipuCoding, "zhipu_no_unit.json",
			[]Window{{Window5h, 20, 1790003600}, {WindowWeekly, 70, 1790600000}}, false},
		{"zhipu error envelope", KindZhipuCoding, "zhipu_error.json", nil, true},
		{"kimi", KindKimiCoding, "kimi_usages.json",
			[]Window{{Window5h, 97, kimi5h}, {WindowWeekly, 75, kimiWeekly}}, false},
		{"minimax", KindMiniMax, "minimax_remains.json",
			[]Window{{Window5h, 87.5, 1790003600}, {WindowWeekly, 40, 1790600000}}, false},
		{"minimax error envelope", KindMiniMax, "minimax_error.json", nil, true},
		{"unknown kind", "other", "kimi_usages.json", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.kind, sample(t, tc.file))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("window %d: got %+v want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseZhipuCreditFallbackOnlyWithoutTokens(t *testing.T) {
	body := []byte(`{"success":true,"data":{"limits":[{"type":"CREDIT_LIMIT","percentage":50,"nextResetTime":1790003600000}]}}`)
	got, _ := Parse(KindZhipuCoding, body)
	if len(got) != 1 || got[0].UsedPct != 50 || got[0].Name != Window5h {
		t.Fatalf("credit fallback: %+v", got)
	}
	body = []byte(`{"success":true,"data":{"limits":[{"type":"CREDIT_LIMIT","percentage":99},{"type":"TOKENS_LIMIT","unit":3,"percentage":10,"nextResetTime":1790003600000}]}}`)
	got, _ = Parse(KindZhipuCoding, body)
	if len(got) != 1 || got[0].UsedPct != 10 {
		t.Fatalf("credit must not mix with tokens: %+v", got)
	}
}

func TestQuotaURL(t *testing.T) {
	cases := []struct{ kind, base, want string }{
		{KindZhipuCoding, "https://open.bigmodel.cn/api/coding/paas/v4", "https://open.bigmodel.cn/api/monitor/usage/quota/limit"},
		{KindZhipuCoding, "https://api.z.ai/api/coding/paas/v4", "https://api.z.ai/api/monitor/usage/quota/limit"},
		{KindZhipuCoding, "https://evil.example/x", "https://open.bigmodel.cn/api/monitor/usage/quota/limit"},
		{KindKimiCoding, "https://evil.example", "https://api.kimi.com/coding/v1/usages"},
		{KindMiniMax, "https://api.minimax.io/v1", "https://api.minimax.io/v1/api/openplatform/coding_plan/remains"},
		{KindMiniMax, "", "https://api.minimaxi.com/v1/api/openplatform/coding_plan/remains"},
	}
	for _, c := range cases {
		got, err := QuotaURL(c.kind, c.base)
		if err != nil || got != c.want {
			t.Errorf("%s %s: got %q err=%v want %q", c.kind, c.base, got, err, c.want)
		}
	}
	if _, err := QuotaURL("x", ""); err == nil {
		t.Error("unknown kind must error")
	}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchAuthHeaderAndStatus(t *testing.T) {
	var gotAuth, gotURL string
	status := 200
	body := string(sample(t, "zhipu_units.json"))
	c := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		gotAuth, gotURL = r.Header.Get("Authorization"), r.URL.String()
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	w, err := Fetch(context.Background(), c, KindZhipuCoding, "", " key1 ")
	if err != nil || len(w) != 2 {
		t.Fatalf("w=%v err=%v", w, err)
	}
	if gotAuth != "key1" || !strings.HasPrefix(gotURL, "https://open.bigmodel.cn/") {
		t.Errorf("zhipu auth=%q url=%q", gotAuth, gotURL)
	}
	body = string(sample(t, "kimi_usages.json"))
	if _, err := Fetch(context.Background(), c, KindKimiCoding, "", "k2"); err != nil || gotAuth != "Bearer k2" {
		t.Errorf("kimi auth=%q err=%v", gotAuth, err)
	}
	status = 401
	if _, err := Fetch(context.Background(), c, KindKimiCoding, "", "k2"); !errors.Is(err, ErrAuth) {
		t.Errorf("401 => ErrAuth, got %v", err)
	}
	status = 500
	if _, err := Fetch(context.Background(), c, KindKimiCoding, "", "k2"); err == nil || errors.Is(err, ErrAuth) {
		t.Errorf("500 => plain error, got %v", err)
	}
}

// ---- thresholds ----

var t0 = time.Date(2026, 10, 9, 10, 20, 0, 0, time.UTC)

func TestEvalThreshold(t *testing.T) {
	n := t0.Unix()
	cases := []struct {
		name      string
		w         []Window
		wantHit   bool
		wantUntil int64
	}{
		{"below", []Window{{Window5h, 94.9, n + 100}}, false, 0},
		{"at threshold", []Window{{Window5h, 95, n + 100}}, true, n + 100},
		{"multiple windows take latest reset", []Window{{Window5h, 99, n + 100}, {WindowWeekly, 96, n + 5000}}, true, n + 5000},
		{"only hit window counts", []Window{{Window5h, 99, n + 100}, {WindowWeekly, 10, n + 5000}}, true, n + 100},
		{"already reset is not a hit", []Window{{Window5h, 99, n - 1}}, false, 0},
		{"unknown reset uses conservative", []Window{{Window5h, 99, 0}}, true, ConservativeReset(t0)},
		{"empty", nil, false, 0},
	}
	for _, c := range cases {
		until, hit := EvalThreshold(c.w, 95, t0)
		if hit != c.wantHit || until != c.wantUntil {
			t.Errorf("%s: got (%d,%v) want (%d,%v)", c.name, until, hit, c.wantUntil, c.wantHit)
		}
	}
}

func TestConservativeReset(t *testing.T) {
	want := time.Date(2026, 10, 9, 11, 5, 0, 0, time.UTC).Unix()
	if got := ConservativeReset(t0); got != want {
		t.Errorf("got %d want %d", got, want)
	}
}

func TestThresholdFor(t *testing.T) {
	if ThresholdFor(dto.ChannelSettings{}, 0) != 95 {
		t.Error("default 95")
	}
	if ThresholdFor(dto.ChannelSettings{}, 80) != 80 {
		t.Error("global")
	}
	if ThresholdFor(dto.ChannelSettings{PlanThresholdPct: 70}, 80) != 70 {
		t.Error("channel wins")
	}
	if ThresholdFor(dto.ChannelSettings{PlanThresholdPct: 170}, 0) != 95 {
		t.Error("out of range ignored")
	}
}

func TestCooldownTarget(t *testing.T) {
	n := t0.Unix()
	if got := CooldownTarget([]Window{{Window5h, 100, n + 100}, {WindowWeekly, 30, n + 9000}}, t0); got != n+100 {
		t.Errorf("full 5h: %d", got)
	}
	if got := CooldownTarget([]Window{{Window5h, 100, n + 100}, {WindowWeekly, 100, n + 9000}}, t0); got != n+9000 {
		t.Errorf("both full => latest: %d", got)
	}
	if got := CooldownTarget([]Window{{Window5h, 60, n + 100}, {WindowWeekly, 30, n + 9000}}, t0); got != n+100 {
		t.Errorf("none full => earliest: %d", got)
	}
	if got := CooldownTarget(nil, t0); got != ConservativeReset(t0) {
		t.Errorf("no snapshot => conservative: %d", got)
	}
	if got := CooldownTarget([]Window{{Window5h, 100, n - 5}}, t0); got != ConservativeReset(t0) {
		t.Errorf("stale snapshot => conservative: %d", got)
	}
}

// ---- error classification ----

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name   string
		status int
		typ    string
		text   string
		want   ErrorClass
	}{
		{"kimi usage limit 403", 403, "", "You've reached the usage limit for this plan", ClassWindowExhausted},
		{"quota will reset", 429, "", "quota will reset at 15:00", ClassWindowExhausted},
		{"structured type", 403, "access_terminated_error", "terminated", ClassWindowExhausted},
		{"concurrency 403 is something else", 403, "access_terminated_error", "You've reached your concurrent request limit. Please wait", ClassNone},
		{"balance zh", 429, "", "账户余额不足", ClassBalanceLow},
		{"balance en 402", 402, "", "Insufficient balance", ClassBalanceLow},
		{"plain 403", 403, "", "forbidden", ClassNone},
		{"500 never", 500, "access_terminated_error", "usage limit", ClassNone},
		{"402 without text", 402, "", "pay", ClassNone},
	}
	for _, c := range cases {
		if got := ClassifyError(c.status, c.typ, c.text); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func planChannel(id int, kind string) *repo.Channel {
	ch := &repo.Channel{Id: id, Status: common.ChannelStatusEnabled}
	ch.SetSetting(dto.ChannelSettings{PlanKind: kind})
	return ch
}

type cooldownRec struct {
	mu sync.Mutex
	m  map[int]int64
}

func (c *cooldownRec) mark(id, _ int, until int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[id] = until
}

func withSeams(t *testing.T, ch *repo.Channel) *cooldownRec {
	t.Helper()
	rec := &cooldownRec{m: map[int]int64{}}
	oldL, oldM, oldS := lookupChannel, markCooldown, defaultStore
	lookupChannel = func(id int) (*repo.Channel, error) {
		if ch == nil || ch.Id != id {
			return nil, errors.New("nf")
		}
		return ch, nil
	}
	markCooldown = rec.mark
	defaultStore = NewStore()
	t.Cleanup(func() { lookupChannel, markCooldown, defaultStore = oldL, oldM, oldS })
	return rec
}

func apiErr(status int, msg, body string) *types.NewAPIError {
	return &types.NewAPIError{Err: errors.New(msg), StatusCode: status, UpstreamBodyHint: body}
}

func TestHandleChannelError_ExhaustedCoolsToSnapshotResetNeverDisables(t *testing.T) {
	ch := planChannel(7, KindKimiCoding)
	rec := withSeams(t, ch)
	reset := time.Now().Add(3 * time.Hour).Unix()
	defaultStore.Put(context.Background(), &Snapshot{ChannelID: 7, Kind: KindKimiCoding,
		Windows: []Window{{Window5h, 100, reset}, {WindowWeekly, 40, reset + 86400}}})

	e := apiErr(403, "forbidden", `{"error":{"type":"access_terminated_error","message":"You've reached the usage limit"}}`)
	if !HandleChannelError(types.ChannelError{ChannelId: 7, AutoBan: true}, e) {
		t.Fatal("must be handled")
	}
	if rec.m[7] != reset {
		t.Errorf("cooldown until %d want %d", rec.m[7], reset)
	}
}

func TestHandleChannelError_NoSnapshotUsesConservative(t *testing.T) {
	ch := planChannel(8, KindZhipuCoding)
	rec := withSeams(t, ch)
	e := apiErr(429, "rate limited", "usage limit exceeded")
	if !HandleChannelError(types.ChannelError{ChannelId: 8}, e) {
		t.Fatal("handled")
	}
	if got, want := rec.m[8], ConservativeReset(time.Now()); got != want && got != ConservativeReset(time.Now().Add(-time.Second)) {
		t.Errorf("got %d want %d", got, want)
	}
}

func TestHandleChannelError_BalanceLowIsRecoverableAndFlagged(t *testing.T) {
	ch := planChannel(9, KindMiniMax)
	rec := withSeams(t, ch)
	e := apiErr(429, "x", "insufficient balance")
	if !HandleChannelError(types.ChannelError{ChannelId: 9}, e) {
		t.Fatal("handled")
	}
	if d := rec.m[9] - time.Now().Unix(); d < int64(BalanceLowCooldown.Seconds())-5 || d > int64(BalanceLowCooldown.Seconds())+5 {
		t.Errorf("cooldown %ds", d)
	}
	snap, ok := defaultStore.Get(context.Background(), 9)
	if !ok || !snap.BalanceLow {
		t.Errorf("snapshot not flagged: %+v", snap)
	}
}

func TestHandleChannelError_Untouched(t *testing.T) {
	plan := planChannel(1, KindKimiCoding)
	cases := []struct {
		name string
		ch   *repo.Channel
		e    *types.NewAPIError
	}{
		{"non-plan channel", planChannel(1, ""), apiErr(403, "x", "usage limit")},
		{"none kind", planChannel(1, KindNone), apiErr(403, "x", "usage limit")},
		{"unrelated 403", plan, apiErr(403, "x", "invalid api key")},
		{"401", plan, apiErr(401, "x", "usage limit")},
		{"plain 429 without snapshot", plan, apiErr(429, "x", "slow down")},
	}
	for _, c := range cases {
		rec := withSeams(t, c.ch)
		if HandleChannelError(types.ChannelError{ChannelId: 1}, c.e) || len(rec.m) != 0 {
			t.Errorf("%s: must not handle", c.name)
		}
	}
}

func TestHandleChannelError_Plain429UsesSnapshotReset(t *testing.T) {
	ch := planChannel(2, KindKimiCoding)
	rec := withSeams(t, ch)
	reset := time.Now().Add(time.Hour).Unix()
	defaultStore.Put(context.Background(), &Snapshot{ChannelID: 2, Windows: []Window{{Window5h, 90, reset}}})
	if !HandleChannelError(types.ChannelError{ChannelId: 2}, apiErr(429, "x", "slow down")) || rec.m[2] != reset {
		t.Errorf("plain 429 with known window should cool to reset, got %v", rec.m)
	}
}

// ---- runner: threshold trigger and recovery ----

type memStore struct{ m map[int]*Snapshot }

func (s *memStore) Get(_ context.Context, id int) (*Snapshot, bool) {
	v, ok := s.m[id]
	if !ok {
		return nil, false
	}
	cp := *v
	return &cp, true
}
func (s *memStore) Put(_ context.Context, v *Snapshot) { cp := *v; s.m[v.ChannelID] = &cp }

func TestRunner_ThresholdParksThenRecovers(t *testing.T) {
	now := t0
	n := now.Unix()
	ch := planChannel(5, KindZhipuCoding)
	pct := 97.0
	rec := &cooldownRec{m: map[int]int64{}}
	cleared := 0
	r := &Runner{D: Deps{
		List: func() ([]*repo.Channel, error) { return []*repo.Channel{ch, planChannel(6, "")}, nil },
		Fetch: func(context.Context, *repo.Channel, string) ([]Window, error) {
			return []Window{{Window5h, pct, n + 600}}, nil
		},
		Cooldown:      func(id int, until int64) { rec.mark(id, 0, until) },
		ClearCooldown: func(int) { cleared++ },
		Store:         &memStore{m: map[int]*Snapshot{}},
		Now:           func() time.Time { return now },
	}}
	if got := r.ProbeOnce(context.Background()); got != 1 {
		t.Fatalf("only declared plan channels are probed, got %d", got)
	}
	if rec.m[5] != n+600 {
		t.Fatalf("parked until %d want %d", rec.m[5], n+600)
	}
	if len(rec.m) != 1 {
		t.Errorf("non-plan channel must not be touched: %v", rec.m)
	}
	// Still over threshold: re-asserted, not cleared.
	r.ProbeOnce(context.Background())
	if cleared != 0 || rec.m[5] != n+600 {
		t.Fatalf("still parked: cleared=%d until=%d", cleared, rec.m[5])
	}
	// Usage fell below threshold before the stated reset: park lifted.
	pct = 10
	r.ProbeOnce(context.Background())
	if cleared != 1 {
		t.Fatalf("expected park lifted, cleared=%d", cleared)
	}
	snap, _ := r.D.Store.Get(context.Background(), 5)
	if snap.CooledUntil != 0 {
		t.Errorf("CooledUntil should reset: %+v", snap)
	}
	// After the stated reset nothing is cleared again.
	now = now.Add(time.Hour)
	r.ProbeOnce(context.Background())
	if cleared != 1 {
		t.Errorf("no spurious clear, cleared=%d", cleared)
	}
}

func TestRunner_FailedProbeKeepsParkAndWindows(t *testing.T) {
	n := t0.Unix()
	ch := planChannel(5, KindKimiCoding)
	fail := false
	cleared := 0
	st := &memStore{m: map[int]*Snapshot{}}
	r := &Runner{D: Deps{
		List: func() ([]*repo.Channel, error) { return []*repo.Channel{ch}, nil },
		Fetch: func(context.Context, *repo.Channel, string) ([]Window, error) {
			if fail {
				return nil, errors.New("boom")
			}
			return []Window{{Window5h, 99, n + 600}}, nil
		},
		Cooldown: func(int, int64) {}, ClearCooldown: func(int) { cleared++ }, Store: st,
		Now: func() time.Time { return t0 },
	}}
	r.ProbeOnce(context.Background())
	fail = true
	r.ProbeOnce(context.Background())
	snap, _ := st.Get(context.Background(), 5)
	if cleared != 0 || snap.CooledUntil != n+600 || len(snap.Windows) != 1 || snap.Error == "" {
		t.Errorf("failed probe must not lift the park: cleared=%d %+v", cleared, snap)
	}
}

func TestRunner_SkipsManuallyDisabledAndExpired(t *testing.T) {
	a := planChannel(1, KindKimiCoding)
	a.Status = common.ChannelStatusManuallyDisabled
	b := &repo.Channel{Id: 2, Status: common.ChannelStatusEnabled}
	b.SetSetting(dto.ChannelSettings{PlanKind: KindKimiCoding, ExpiresAt: t0.Unix() - 1})
	calls := 0
	r := &Runner{D: Deps{
		List:  func() ([]*repo.Channel, error) { return []*repo.Channel{a, b}, nil },
		Fetch: func(context.Context, *repo.Channel, string) ([]Window, error) { calls++; return nil, nil },
		Store: &memStore{m: map[int]*Snapshot{}}, Now: func() time.Time { return t0 },
	}}
	if r.ProbeOnce(context.Background()) != 0 || calls != 0 {
		t.Errorf("must not probe, calls=%d", calls)
	}
}

// ---- expiry ----

func TestDecideExpiry(t *testing.T) {
	n := t0.Unix()
	h := int64(3600)
	cases := []struct {
		name   string
		exp    int64
		last   int
		paused bool
		want   ExpiryAction
	}{
		{"no expiry", 0, 0, false, ExpiryNone},
		{"far future", n + 100*h, 0, false, ExpiryNone},
		{"72h notice", n + 70*h, 0, false, ExpiryNotice72},
		{"72h notice once", n + 70*h, 72, false, ExpiryNone},
		{"24h notice", n + 20*h, 72, false, ExpiryNotice24},
		{"24h notice skipping 72", n + 20*h, 0, false, ExpiryNotice24},
		{"24h notice once", n + 20*h, 24, false, ExpiryNone},
		{"expired pauses", n - 1, 24, false, ExpiryPause},
		{"expired already paused", n - 1, 24, true, ExpiryNone},
		{"renewed after pause", n + 50*h, 0, true, ExpiryRenew},
		{"expiry cleared after pause", 0, 0, true, ExpiryRenew},
	}
	for _, c := range cases {
		if got := DecideExpiry(c.exp, t0, c.last, c.paused); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func expChannel(id int, exp int64, status int, reason string) *repo.Channel {
	ch := &repo.Channel{Id: id, Status: status}
	ch.SetSetting(dto.ChannelSettings{ExpiresAt: exp})
	if reason != "" {
		ch.SetOtherInfo(map[string]interface{}{"status_reason": reason})
	}
	return ch
}

func TestRunner_ExpiryPausesNoticesOnceAndRenews(t *testing.T) {
	n := t0.Unix()
	h := int64(3600)
	expired := expChannel(1, n-10, common.ChannelStatusEnabled, "")
	soon := expChannel(2, n+60*h, common.ChannelStatusEnabled, "")
	healthy := expChannel(3, n+500*h, common.ChannelStatusEnabled, "")
	manual := expChannel(4, n-10, common.ChannelStatusManuallyDisabled, "")
	renewed := expChannel(5, n+500*h, common.ChannelStatusAutoDisabled, ExpiryReasonPrefix+" plan ended")
	otherBan := expChannel(6, n-10, common.ChannelStatusAutoDisabled, "401 bad key")

	var paused, resumed, noticed []int
	levels := map[int]int{}
	r := &Runner{D: Deps{
		List: func() ([]*repo.Channel, error) {
			return []*repo.Channel{expired, soon, healthy, manual, renewed, otherBan}, nil
		},
		Pause: func(ch *repo.Channel, reason string) bool {
			if !strings.HasPrefix(reason, ExpiryReasonPrefix) {
				t.Errorf("pause reason must carry the expired: prefix, got %q", reason)
			}
			paused = append(paused, ch.Id)
			return true
		},
		Resume:         func(ch *repo.Channel) bool { resumed = append(resumed, ch.Id); return true },
		NoticeLevel:    func(ch *repo.Channel, _ int64) int { return levels[ch.Id] },
		SetNoticeLevel: func(ch *repo.Channel, _ int64, l int) { levels[ch.Id] = l },
		Notice:         func(ch *repo.Channel, l int, _ int64) { noticed = append(noticed, ch.Id*1000+l) },
		Now:            func() time.Time { return t0 },
	}}
	r.ExpiryOnce()
	r.ExpiryOnce() // second pass must be idempotent for notices
	if len(paused) != 2 || paused[0] != 1 {
		t.Errorf("paused=%v (expired enabled channel pauses each pass until status updates)", paused)
	}
	if len(noticed) != 1 || noticed[0] != 2072 {
		t.Errorf("noticed=%v want exactly one 72h notice for #2", noticed)
	}
	if len(resumed) != 2 || resumed[0] != 5 {
		t.Errorf("resumed=%v", resumed)
	}
	for _, id := range paused {
		if id == 4 || id == 6 {
			t.Errorf("channel %d is already out of rotation and must not be touched", id)
		}
	}
}

func TestPausedByExpiryMultiKey(t *testing.T) {
	ch := &repo.Channel{Status: common.ChannelStatusAutoDisabled}
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeyDisabledReason = map[int]string{0: "expired: x", 1: "expired: x"}
	if !pausedByExpiry(ch) {
		t.Error("all keys expired => paused by expiry")
	}
	ch.ChannelInfo.MultiKeyDisabledReason[1] = "401"
	if pausedByExpiry(ch) {
		t.Error("mixed reasons => not ours")
	}
}

// ---- scheduled test mode ----

func TestEffectiveTestModeAndFilter(t *testing.T) {
	plan := dto.ChannelSettings{PlanKind: KindKimiCoding}
	cases := []struct {
		name   string
		s      dto.ChannelSettings
		global string
		want   string
	}{
		{"default stays all", dto.ChannelSettings{}, "", TestModeAll},
		{"global all", dto.ChannelSettings{}, "all", TestModeAll},
		{"global none", dto.ChannelSettings{}, "none", TestModeNone},
		{"plan defaults to auto_ban_only", plan, "all", TestModeAutoBanOnly},
		{"explicit channel mode wins over plan default", dto.ChannelSettings{PlanKind: KindKimiCoding, TestMode: "all"}, "none", TestModeAll},
		{"garbage global falls back to all", dto.ChannelSettings{}, "bogus", TestModeAll},
		{"none kind is not a plan", dto.ChannelSettings{PlanKind: "none"}, "", TestModeAll},
	}
	for _, c := range cases {
		if got := EffectiveTestMode(c.s, c.global); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}

	mk := func(id int, s dto.ChannelSettings, status int) *repo.Channel {
		ch := &repo.Channel{Id: id, Status: status}
		ch.SetSetting(s)
		return ch
	}
	chans := []*repo.Channel{
		mk(1, dto.ChannelSettings{}, common.ChannelStatusEnabled),                              // payg, enabled
		mk(2, plan, common.ChannelStatusEnabled),                                               // plan, healthy => skipped
		mk(3, plan, common.ChannelStatusAutoDisabled),                                          // plan, banned => recovery test
		mk(4, dto.ChannelSettings{TestMode: "none"}, common.ChannelStatusAutoDisabled),         // never
		mk(5, dto.ChannelSettings{TestMode: "auto_ban_only"}, common.ChannelStatusEnabled),     // skipped
		mk(6, dto.ChannelSettings{ExpiresAt: t0.Unix() - 5}, common.ChannelStatusAutoDisabled), // expired: a pass would re-enable it
	}
	var ids []int
	for _, ch := range FilterScheduledTest(chans, "all", t0) {
		ids = append(ids, ch.Id)
	}
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 3 {
		t.Errorf("filtered ids=%v want [1 3]", ids)
	}
	if got := FilterScheduledTest(chans, "none", t0); len(got) != 1 || got[0].Id != 3 {
		// global none: only channels that override it or plan channels in recovery remain
		var g []int
		for _, c := range got {
			g = append(g, c.Id)
		}
		t.Errorf("global none: ids=%v want [3]", g)
	}
}
