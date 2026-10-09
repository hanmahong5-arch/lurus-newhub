package planquota

import (
	"context"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// Store keeps the latest Snapshot of each plan channel.
type Store interface {
	Get(ctx context.Context, channelID int) (*Snapshot, bool)
	Put(ctx context.Context, s *Snapshot)
}

// Deps are the runner's side effects, injectable for tests.
type Deps struct {
	List          func() ([]*repo.Channel, error)
	Fetch         func(ctx context.Context, ch *repo.Channel, kind string) ([]Window, error)
	Cooldown      func(channelID int, until int64)
	ClearCooldown func(channelID int)
	Store         Store
	// SaveSummary writes the compact snapshot summary into the channel's JSON.
	SaveSummary func(ch *repo.Channel, s *Snapshot)
	Pause       func(ch *repo.Channel, reason string) bool
	Resume      func(ch *repo.Channel) bool
	// NoticeLevel / SetNoticeLevel persist which expiry notice (72/24, 0 =
	// none) was already recorded for a given expires_at.
	NoticeLevel    func(ch *repo.Channel, expiresAt int64) int
	SetNoticeLevel func(ch *repo.Channel, expiresAt int64, level int)
	// Notice records the pre-expiry event (audit + log).
	Notice func(ch *repo.Channel, level int, expiresAt int64)
	// Expired records the auto-pause event.
	Expired func(ch *repo.Channel, expiresAt int64)
	Now     func() time.Time
	// Jitter returns a random delay in [0,max); nil = no delay.
	Jitter func(max time.Duration) time.Duration
	// GlobalThresholdPct is the default threshold (0 = DefaultThresholdPct).
	GlobalThresholdPct float64
	// Concurrency bounds simultaneous quota requests (default 4).
	Concurrency int
}

// Runner executes the probe and expiry passes.
type Runner struct{ D Deps }

func (r *Runner) now() time.Time {
	if r.D.Now != nil {
		return r.D.Now()
	}
	return time.Now()
}

// ProbeOnce probes every plan channel (bounded concurrency) and applies the
// threshold policy. Returns the number of channels probed.
func (r *Runner) ProbeOnce(ctx context.Context) int {
	chans, err := r.D.List()
	if err != nil {
		common.SysLog("planquota: list channels failed: " + err.Error())
		return 0
	}
	conc := r.D.Concurrency
	if conc <= 0 {
		conc = 4
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	n := 0
	for _, ch := range chans {
		st := ch.GetSetting()
		kind := NormalizeKind(st.PlanKind)
		if kind == "" || ch.Status == common.ChannelStatusManuallyDisabled {
			continue
		}
		if st.ExpiresAt > 0 && st.ExpiresAt <= r.now().Unix() {
			continue
		}
		n++
		wg.Add(1)
		sem <- struct{}{}
		go func(ch *repo.Channel, kind string, threshold float64) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if p := recover(); p != nil {
					common.SysLog("planquota: probe panic recovered")
				}
			}()
			if r.D.Jitter != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(r.D.Jitter(2 * time.Second)):
				}
			}
			r.ProbeChannel(ctx, ch, kind, threshold)
		}(ch, kind, ThresholdFor(st, r.D.GlobalThresholdPct))
	}
	wg.Wait()
	return n
}

// ProbeChannel runs one probe and applies the threshold policy.
func (r *Runner) ProbeChannel(ctx context.Context, ch *repo.Channel, kind string, threshold float64) {
	now := r.now()
	prev, _ := r.D.Store.Get(ctx, ch.Id)
	snap := &Snapshot{ChannelID: ch.Id, Kind: kind, FetchedAt: now.Unix()}
	if prev != nil {
		snap.CooledUntil = prev.CooledUntil
		snap.BalanceLow = prev.BalanceLow
	}
	windows, err := r.D.Fetch(ctx, ch, kind)
	if err != nil {
		// Keep the last known windows and any park we set: a failed probe is
		// not evidence that the window recovered.
		if prev != nil {
			snap.Windows = prev.Windows
		}
		snap.Error = err.Error()
		r.save(ctx, ch, snap)
		return
	}
	snap.Windows = windows
	if until, hit := EvalThreshold(windows, threshold, now); hit {
		r.D.Cooldown(ch.Id, until) // re-asserted every pass: idempotent, survives restarts
		snap.CooledUntil = until
	} else {
		if snap.CooledUntil > now.Unix() {
			r.D.ClearCooldown(ch.Id) // window recovered before its stated reset
		}
		snap.CooledUntil = 0
		snap.BalanceLow = false
	}
	r.save(ctx, ch, snap)
}

func (r *Runner) save(ctx context.Context, ch *repo.Channel, s *Snapshot) {
	r.D.Store.Put(ctx, s)
	if r.D.SaveSummary != nil {
		r.D.SaveSummary(ch, s)
	}
}

// pausedByExpiry reports whether a channel is currently disabled by the
// expiry scan (as opposed to manually or by an error).
func pausedByExpiry(ch *repo.Channel) bool {
	if ch.Status != common.ChannelStatusAutoDisabled {
		return false
	}
	if ch.ChannelInfo.IsMultiKey {
		if len(ch.ChannelInfo.MultiKeyDisabledReason) == 0 {
			return false
		}
		for _, reason := range ch.ChannelInfo.MultiKeyDisabledReason {
			if !strings.HasPrefix(reason, ExpiryReasonPrefix) {
				return false
			}
		}
		return true
	}
	reason, _ := ch.GetOtherInfo()["status_reason"].(string)
	return strings.HasPrefix(reason, ExpiryReasonPrefix)
}

// ExpiryOnce scans channels with expires_at: pauses expired ones, resumes ones
// whose expiry moved into the future, and records 72h / 24h notices once.
func (r *Runner) ExpiryOnce() {
	chans, err := r.D.List()
	if err != nil {
		common.SysLog("planquota: list channels failed: " + err.Error())
		return
	}
	now := r.now()
	for _, ch := range chans {
		st := ch.GetSetting()
		paused := pausedByExpiry(ch)
		if st.ExpiresAt <= 0 && !paused {
			continue
		}
		if ch.Status == common.ChannelStatusManuallyDisabled {
			continue
		}
		last := 0
		if st.ExpiresAt > 0 && r.D.NoticeLevel != nil {
			last = r.D.NoticeLevel(ch, st.ExpiresAt)
		}
		switch DecideExpiry(st.ExpiresAt, now, last, paused) {
		case ExpiryPause:
			if ch.Status != common.ChannelStatusEnabled {
				continue // already out of rotation for another reason
			}
			reason := ExpiryReasonPrefix + " plan ended at " + time.Unix(st.ExpiresAt, 0).UTC().Format(time.RFC3339)
			if r.D.Pause(ch, reason) && r.D.Expired != nil {
				r.D.Expired(ch, st.ExpiresAt)
			}
		case ExpiryRenew:
			r.D.Resume(ch)
		case ExpiryNotice72, ExpiryNotice24:
			level := 72
			if DecideExpiry(st.ExpiresAt, now, last, paused) == ExpiryNotice24 {
				level = 24
			}
			if r.D.Notice != nil {
				r.D.Notice(ch, level, st.ExpiresAt)
			}
			if r.D.SetNoticeLevel != nil {
				r.D.SetNoticeLevel(ch, st.ExpiresAt, level)
			}
		}
	}
}

// Intervals (env-tunable at start-up).
const (
	defaultProbeInterval = 5 * time.Minute
	expiryInterval       = time.Minute
)

// ProbeInterval returns the probe period: PLAN_QUOTA_INTERVAL_SECONDS or 5 min.
func ProbeInterval() time.Duration {
	if s := common.GetEnvOrDefault("PLAN_QUOTA_INTERVAL_SECONDS", 0); s >= 30 {
		return time.Duration(s) * time.Second
	}
	return defaultProbeInterval
}

// jittered spreads a period by +-10% so replicas and passes do not align.
func jittered(d time.Duration) time.Duration {
	return d + time.Duration((rand.Float64()-0.5)*0.2*float64(d))
}

// Run is the leader-only background loop: the expiry scan every minute, the
// quota probe every ProbeInterval (jittered). Followers idle.
func (r *Runner) Run(ctx context.Context) {
	probeEvery := ProbeInterval()
	nextProbe := r.now().Add(10 * time.Second)
	t := time.NewTicker(expiryInterval)
	defer t.Stop()
	common.SysLog("planquota: started, probe interval " + probeEvery.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !common.IsLeader() {
				continue
			}
			r.ExpiryOnce()
			if !r.now().Before(nextProbe) {
				n := r.ProbeOnce(ctx)
				if n > 0 {
					common.SysLog("planquota: probed " + strconv.Itoa(n) + " plan channel(s)")
				}
				nextProbe = r.now().Add(jittered(probeEvery))
			}
		}
	}
}
