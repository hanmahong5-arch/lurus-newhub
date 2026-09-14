package common

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

// isLeader caches whether this process currently holds the HA leader lease.
// It is the single source of truth that background tasks consult to decide
// whether to do work; the lifecycle LeaderManager is the only writer.
var isLeader atomic.Bool

// leaderSince is the unix timestamp of the most recent false→true edge of
// isLeader — when this replica most recently WON the lease, not when it
// first booted. Zero until SetLeader(true) is called at least once.
//
// L3 repair round (B-F5, new-leader grace): GetSystemTasksV2 uses this to
// judge a leader-only task that has never stamped on this replica against
// `now - max(StartTime, leaderSince)` rather than raw process uptime — a
// replica that has been running for days as a follower and only just won
// the lease has had zero chances to run a 24h leader-only job, and must
// not be reported "overdue" for up to 24h the moment it takes over.
var leaderSince atomic.Int64

// IsLeader reports whether this process currently holds the HA leader lease.
// Background tasks that must run on exactly one replica gate their per-tick
// work on this. Until the LeaderManager wins the lease it returns false, so a
// node never does leader-only work before it is confirmed leader.
func IsLeader() bool {
	return isLeader.Load()
}

// LeaderSince returns the unix timestamp of this replica's most recent
// false→true leadership edge, or 0 if it has never held the lease in this
// process's lifetime. See leaderSince's doc comment for why this is
// distinct from StartTime.
func LeaderSince() int64 {
	return leaderSince.Load()
}

// SetLeader caches the current leadership state and publishes the
// lurus_gateway_leader gauge. It is the sole writer of isLeader, and the
// three call sites that change leadership (boot lease acquisition in
// repo/main.go, LeaderManager.step, and release-on-shutdown) all go through
// it, so the gauge cannot drift from the cached flag.
func SetLeader(v bool) {
	wasLeader := isLeader.Swap(v)
	if v && !wasLeader {
		leaderSince.Store(time.Now().Unix())
	}
	metrics.SetLeader(v)
}

var (
	nodeHolderID   string
	nodeHolderOnce sync.Once
)

// NodeHolderID returns this process's stable, unique leader-election identity,
// computed once per process. It combines the pod hostname (HOSTNAME, which
// K8s sets to the pod name) with a random suffix so two processes that share a
// hostname — or run with an empty hostname in dev — still get distinct
// identities and cannot both believe they hold the same lease.
func NodeHolderID() string {
	nodeHolderOnce.Do(func() {
		host := GetEnvOrDefaultString("HOSTNAME", "node")
		nodeHolderID = host + "-" + GetRandomString(6)
	})
	return nodeHolderID
}
