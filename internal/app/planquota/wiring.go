package planquota

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

const (
	snapshotKeyPrefix = "planquota:snap:"
	snapshotTTL       = 24 * time.Hour
	taskName          = "plan-quota"
	// Audit actions of the plan lifecycle events.
	ActionPlanExpiring = "channel.plan_expiring"
	ActionPlanExpired  = "channel.plan_expired"
)

// ---- snapshot store: Redis when available, process memory otherwise ----

type redisStore struct {
	mu  sync.Mutex
	mem map[int]*Snapshot
}

// NewStore returns the default Store.
func NewStore() Store { return &redisStore{mem: map[int]*Snapshot{}} }

func (s *redisStore) Get(ctx context.Context, id int) (*Snapshot, bool) {
	if common.RedisEnabled && common.RDB != nil {
		if raw, err := common.RedisGet(ctx, snapshotKeyPrefix+strconv.Itoa(id)); err == nil {
			var snap Snapshot
			if json.Unmarshal([]byte(raw), &snap) == nil {
				return &snap, true
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.mem[id]
	if !ok {
		return nil, false
	}
	cp := *snap
	return &cp, true
}

func (s *redisStore) Put(ctx context.Context, snap *Snapshot) {
	cp := *snap
	s.mu.Lock()
	s.mem[snap.ChannelID] = &cp
	s.mu.Unlock()
	if common.RedisEnabled && common.RDB != nil {
		if b, err := json.Marshal(snap); err == nil {
			if err := common.RedisSet(ctx, snapshotKeyPrefix+strconv.Itoa(snap.ChannelID), string(b), snapshotTTL); err != nil {
				common.SysLog("planquota: snapshot redis write failed: " + err.Error())
			}
		}
	}
}

var defaultStore = NewStore()

// ---- channel JSON helpers ----

// updateOtherInfo read-modify-writes only the other_info column.
func updateOtherInfo(id int, mutate func(info map[string]interface{})) {
	fresh, err := repo.GetChannelById(id, false)
	if err != nil || fresh == nil {
		return
	}
	info := fresh.GetOtherInfo()
	mutate(info)
	fresh.SetOtherInfo(info)
	if err := repo.DB.Model(&repo.Channel{}).Where("id = ?", id).UpdateColumn("other_info", fresh.OtherInfo).Error; err != nil {
		common.SysLog(fmt.Sprintf("planquota: save other_info failed: channel=%d err=%v", id, err))
	}
}

func saveSummary(ch *repo.Channel, s *Snapshot) {
	updateOtherInfo(ch.Id, func(info map[string]interface{}) {
		info["plan_quota"] = map[string]interface{}{
			"kind": s.Kind, "windows": s.Windows, "fetched_at": s.FetchedAt,
			"error": s.Error, "cooled_until": s.CooledUntil, "balance_low": s.BalanceLow,
		}
	})
}

func noticeLevel(ch *repo.Channel, expiresAt int64) int {
	info := ch.GetOtherInfo()
	forAt, _ := info["plan_notice_for"].(float64)
	if int64(forAt) != expiresAt {
		return 0
	}
	lvl, _ := info["plan_notice_level"].(float64)
	return int(lvl)
}

func setNoticeLevel(ch *repo.Channel, expiresAt int64, level int) {
	updateOtherInfo(ch.Id, func(info map[string]interface{}) {
		info["plan_notice_for"] = expiresAt
		info["plan_notice_level"] = level
	})
}

func channelKeys(ch *repo.Channel) []string {
	if !ch.ChannelInfo.IsMultiKey {
		return []string{""}
	}
	return ch.GetKeys()
}

func pause(ch *repo.Channel, reason string) bool {
	ok := false
	for _, k := range channelKeys(ch) {
		if repo.UpdateChannelStatus(ch.Id, k, common.ChannelStatusAutoDisabled, reason) {
			ok = true
		}
	}
	return ok
}

func resume(ch *repo.Channel) bool {
	ok := false
	for _, k := range channelKeys(ch) {
		if repo.UpdateChannelStatus(ch.Id, k, common.ChannelStatusEnabled, "") {
			ok = true
		}
	}
	if ok {
		app.ClearChannelCooldown(ch.Id)
	}
	return ok
}

func auditEvent(ch *repo.Channel, action string, details map[string]interface{}) {
	b, _ := json.Marshal(details)
	governance.RecordAuditEvent(&entity.AuditEvent{
		TenantID:   "default",
		Timestamp:  common.GetTimestamp(),
		ActorType:  governance.ActorSystem,
		Action:     action,
		Resource:   governance.ResourceChannel,
		ResourceID: ch.Id,
		Details:    string(b),
	})
}

func fetchFor(ctx context.Context, ch *repo.Channel, kind string) ([]Window, error) {
	keys := ch.GetKeys()
	if len(keys) == 0 {
		return nil, fmt.Errorf("channel has no key")
	}
	base := ""
	if ch.BaseURL != nil {
		base = *ch.BaseURL
	}
	client := http.DefaultClient
	if p := ch.GetSetting().Proxy; p != "" {
		c, err := app.GetHttpClientWithProxy(p)
		if err != nil {
			return nil, err
		}
		client = c
	} else if c := app.GetHttpClient(); c != nil {
		client = c
	}
	// A multi-key channel is probed with its first key: all keys of one plan
	// channel are expected to belong to the same account.
	return Fetch(ctx, client, kind, base, keys[0])
}

// NewRunner builds the production runner.
func NewRunner() *Runner {
	global := float64(common.GetEnvOrDefault("PLAN_QUOTA_THRESHOLD_PCT", int(DefaultThresholdPct)))
	return &Runner{D: Deps{
		List:           func() ([]*repo.Channel, error) { return repo.GetAllChannels(0, 0, true, false) },
		Fetch:          fetchFor,
		Cooldown:       func(id int, until int64) { markCooldown(id, 0, until) },
		ClearCooldown:  app.ClearChannelCooldown,
		Store:          defaultStore,
		SaveSummary:    saveSummary,
		Pause:          pause,
		Resume:         resume,
		NoticeLevel:    noticeLevel,
		SetNoticeLevel: setNoticeLevel,
		Notice: func(ch *repo.Channel, level int, expiresAt int64) {
			common.SysLog(fmt.Sprintf("planquota: channel #%d %q plan expires within %dh (at %s)", ch.Id, ch.Name, level, time.Unix(expiresAt, 0).UTC().Format(time.RFC3339)))
			auditEvent(ch, ActionPlanExpiring, map[string]interface{}{"name": ch.Name, "within_hours": level, "expires_at": expiresAt})
		},
		Expired: func(ch *repo.Channel, expiresAt int64) {
			common.SysLog(fmt.Sprintf("planquota: channel #%d %q paused, plan expired at %s", ch.Id, ch.Name, time.Unix(expiresAt, 0).UTC().Format(time.RFC3339)))
			auditEvent(ch, ActionPlanExpired, map[string]interface{}{"name": ch.Name, "expires_at": expiresAt})
		},
		Jitter: func(max time.Duration) time.Duration {
			return time.Duration(float64(max) * rngFloat())
		},
		GlobalThresholdPct: global,
		Concurrency:        4,
	}}
}

// RunBackground is the entry for cmd/server (master nodes only).
func RunBackground(ctx context.Context) {
	taskreg.Register(taskName, ProbeInterval, true, nil)
	NewRunner().Run(ctx)
}

// ---- relay error hook ----

// Seams for tests.
var (
	lookupChannel = repo.CacheGetChannel
	markCooldown  = app.MarkChannelCooldown
)

func rngFloat() float64 { return rand.Float64() }

// FilterScheduledTest drops the channels a scheduled (non-manual) test must
// not touch, per planquota test modes. globalMode is the monitor setting.
func FilterScheduledTest(chans []*repo.Channel, globalMode string, now time.Time) []*repo.Channel {
	out := make([]*repo.Channel, 0, len(chans))
	for _, ch := range chans {
		if AllowScheduledTest(ch.GetSetting(), globalMode, ch.Status == common.ChannelStatusAutoDisabled, now) {
			out = append(out, ch)
		}
	}
	return out
}

// HandleChannelError classifies a failed relay attempt on a plan channel. It
// returns true when the error is a "window used up" or "balance low" answer
// that has been turned into a cooldown; the caller must then NOT auto-disable
// the channel nor apply another cooldown over it.
func HandleChannelError(ce types.ChannelError, e *types.NewAPIError) bool {
	if e == nil || ce.ChannelId == 0 {
		return false
	}
	switch e.StatusCode {
	case http.StatusPaymentRequired, http.StatusForbidden, http.StatusTooManyRequests:
	default:
		return false
	}
	ch, err := lookupChannel(ce.ChannelId)
	if err != nil || ch == nil {
		return false
	}
	if NormalizeKind(ch.GetSetting().PlanKind) == "" {
		return false
	}
	ctx := context.Background()
	text := e.UpstreamBodyHint + " " + e.Error()
	class := ClassifyError(e.StatusCode, string(e.ToOpenAIError().Type), text)
	snap, _ := defaultStore.Get(ctx, ce.ChannelId)
	now := time.Now()
	if class == ClassNone && e.StatusCode == http.StatusTooManyRequests && snap != nil {
		// A plan channel's 429 with a known future window reset is the window
		// talking (sub2api does the same); without a snapshot fall through to
		// the generic 429 handling.
		for _, w := range snap.Windows {
			if w.ResetAt > now.Unix() {
				class = ClassWindowExhausted
				break
			}
		}
	}
	switch class {
	case ClassWindowExhausted:
		var windows []Window
		if snap != nil {
			windows = snap.Windows
		}
		until := CooldownTarget(windows, now)
		markCooldown(ce.ChannelId, 0, until)
		metrics.RecordChannelCooldown(ce.ChannelId, "plan_window")
		common.SysLog(fmt.Sprintf("planquota: channel #%d window exhausted, cooling until %s", ce.ChannelId, time.Unix(until, 0).UTC().Format(time.RFC3339)))
		return true
	case ClassBalanceLow:
		until := now.Add(BalanceLowCooldown).Unix()
		markCooldown(ce.ChannelId, 0, until)
		metrics.RecordChannelCooldown(ce.ChannelId, "balance_low")
		if snap == nil {
			snap = &Snapshot{ChannelID: ce.ChannelId, Kind: NormalizeKind(ch.GetSetting().PlanKind)}
		}
		snap.BalanceLow = true
		defaultStore.Put(ctx, snap)
		common.SysLog(fmt.Sprintf("planquota: channel #%d balance low, cooling until %s", ce.ChannelId, time.Unix(until, 0).UTC().Format(time.RFC3339)))
		return true
	}
	return false
}
