package common

import (
	"sync"
	"sync/atomic"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

// isLeader caches whether this process currently holds the HA leader lease.
// It is the single source of truth that background tasks consult to decide
// whether to do work; the lifecycle LeaderManager is the only writer.
var isLeader atomic.Bool

// IsLeader reports whether this process currently holds the HA leader lease.
// Background tasks that must run on exactly one replica gate their per-tick
// work on this. Until the LeaderManager wins the lease it returns false, so a
// node never does leader-only work before it is confirmed leader.
func IsLeader() bool {
	return isLeader.Load()
}

// SetLeader caches the current leadership state and publishes the
// lurus_gateway_leader gauge. It is the sole writer of isLeader, and the
// three call sites that change leadership (boot lease acquisition in
// repo/main.go, LeaderManager.step, and release-on-shutdown) all go through
// it, so the gauge cannot drift from the cached flag.
func SetLeader(v bool) {
	isLeader.Store(v)
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
