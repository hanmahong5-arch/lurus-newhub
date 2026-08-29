package lifecycle

import (
	"context"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// renewDivisor renews at ttl/renewDivisor so a couple of transient renewal
// failures can be absorbed before the lease lapses. ttl=30s → renew ~10s.
const renewDivisor = 3

// LeaderManager runs the renewal loop that maintains this node's HA leadership
// lease and reconciles common.IsLeader(). It implements lifecycle.Task so it
// can be registered like any other background task.
type LeaderManager struct {
	name        string
	holderId    string
	ttlSeconds  int64
	renewEvery  time.Duration
	localExpiry int64 // unix sec; best local estimate of when our lease lapses
}

// NewLeaderManager builds a manager for the well-known master lease using this
// process's stable holder identity.
func NewLeaderManager() *LeaderManager {
	renewEvery := time.Duration(entity.LeaderLeaseTTLSeconds/renewDivisor) * time.Second
	if renewEvery < time.Second {
		// Guard against a future TTL < renewDivisor yielding a 0 interval,
		// which would panic time.NewTicker.
		renewEvery = time.Second
	}
	return &LeaderManager{
		name:       entity.LeaderElectionName,
		holderId:   common.NodeHolderID(),
		ttlSeconds: entity.LeaderLeaseTTLSeconds,
		renewEvery: renewEvery,
	}
}

// Name implements lifecycle.Task.
func (m *LeaderManager) Name() string { return "leader-election" }

// IsLeader reports whether this node currently holds leadership.
func (m *LeaderManager) IsLeader() bool { return common.IsLeader() }

// step performs one acquire-or-renew attempt at the given unix-second time and
// reconciles the cached leadership flag. It is exposed for deterministic
// testing with an injected clock; production drives it from Run on a ticker.
// Returns whether this node is leader after the step.
func (m *LeaderManager) step(now int64) bool {
	ok, err := repo.TryAcquireOrRenew(m.name, m.holderId, m.ttlSeconds, now)
	if err != nil {
		// Transient DB error: keep leadership until our local lease estimate
		// lapses, then step down. Dropping on every blip would cause needless
		// failover churn and let master-only work stall.
		if common.IsLeader() && now >= m.localExpiry {
			common.SetLeader(false)
		}
		common.SysError("leader election step: " + err.Error())
		return common.IsLeader()
	}
	if ok {
		m.localExpiry = now + m.ttlSeconds
		common.SetLeader(true)
	} else {
		common.SetLeader(false)
	}
	return ok
}

// Run drives the renewal loop until ctx is cancelled, at which point a leader
// releases its lease so the successor takes over immediately. Implements
// lifecycle.Task.
func (m *LeaderManager) Run(ctx context.Context) error {
	// Attempt immediately so a single-node deploy becomes leader without
	// waiting a full renewal interval.
	m.step(common.GetTimestamp())

	ticker := time.NewTicker(m.renewEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if common.IsLeader() {
				if err := repo.ReleaseLease(m.name, m.holderId); err != nil {
					common.SysError("leader election release on shutdown: " + err.Error())
				}
				common.SetLeader(false)
			}
			return ctx.Err()
		case <-ticker.C:
			m.step(common.GetTimestamp())
		}
	}
}

// LeaderTask runs fn on an interval but only while this node holds leadership.
// It is the building block for HA-safe periodic work (e.g. secret rotation):
// every replica registers the task, yet only the leader's ticks do work, and
// leadership loss pauses it without restarting the task.
type LeaderTask struct {
	name     string
	interval time.Duration
	fn       func(ctx context.Context) error
	isLeader func() bool
	// poll is how often Run re-checks leadership between work intervals so a
	// freshly acquired lease triggers a catch-up run promptly instead of after
	// a full interval. min(interval, leaderTaskPollInterval); a field only so
	// tests can shrink it.
	poll time.Duration
}

// leaderTaskPollInterval bounds how long a new leader waits before its
// catch-up run. 30s is well under the shortest production task interval and
// far above the lease renewal cadence, so the edge is seen promptly without
// busy-polling.
const leaderTaskPollInterval = 30 * time.Second

// NewLeaderTask creates a leader-gated periodic task. fn runs promptly when
// this process first becomes leader (catching up work a predecessor may never
// have reached — with a 24h interval and deployments more frequent than that,
// a ticker-only task could otherwise never fire), then at most once per
// interval while leadership is held. fn must therefore be self-gating /
// idempotent about what is actually due; every current user already is.
func NewLeaderTask(name string, interval time.Duration, fn func(ctx context.Context) error) *LeaderTask {
	poll := interval
	if poll > leaderTaskPollInterval {
		poll = leaderTaskPollInterval
	}
	return &LeaderTask{
		name:     name,
		interval: interval,
		fn:       fn,
		isLeader: common.IsLeader,
		poll:     poll,
	}
}

// Name implements lifecycle.Task.
func (t *LeaderTask) Name() string { return t.name }

// Run implements lifecycle.Task: polls leadership and runs fn when this node
// is leader and either has never run fn in this process or the interval has
// elapsed since the last run. Regaining leadership within an interval does
// NOT re-run fn (lastRun survives the flap), preserving the at-most-once-per-
// interval contract per process; a failover to a fresh process runs promptly
// because its lastRun is zero.
func (t *LeaderTask) Run(ctx context.Context) error {
	ticker := time.NewTicker(t.poll)
	defer ticker.Stop()
	var lastRun time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if !t.isLeader() {
				continue
			}
			if !lastRun.IsZero() && time.Since(lastRun) < t.interval {
				continue
			}
			lastRun = time.Now()
			// fn errors are swallowed so a single failure does not stop the
			// loop; fn itself is responsible for logging.
			_ = t.fn(ctx)
		}
	}
}
