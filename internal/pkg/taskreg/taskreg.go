// Package taskreg is a process-local registry of periodic background
// tasks — the static "what SHOULD be running" half of the system-tasks
// picture. It deliberately does not know whether a task has actually
// succeeded: that half lives in metrics.LeaderTaskLastSuccess, which each
// task stamps directly after a successful pass. Keeping the two separate
// means GET /api/v2/admin/system/tasks (v2_admin_system_tasks.go) can list
// a task that has NEVER once succeeded — the failure mode the gauge's
// Set(0)-at-boot convention exists to make visible — rather than that task
// being silently absent because nothing recorded it.
//
// No imports from internal/adapter/handler or internal/lifecycle: every
// Start*WithContext entry point in those packages (and in internal/app,
// internal/app/openrouter_pool) imports taskreg to register itself, so the
// reverse would be an import cycle.
package taskreg

import (
	"sync"
	"time"
)

// Task describes one registered periodic background job.
type Task struct {
	// Name matches the "task" label on metrics.LeaderTaskLastSuccess for
	// this job. The set of names registered in a given process is whatever
	// its Start*WithContext entry points chose to call Register with — see
	// each call site, not a fixed list here.
	Name string
	// Interval returns the job's current tick period. A func, not a plain
	// time.Duration, because some jobs (channel-health-test) read their
	// period from mutable operator settings on every tick rather than
	// fixing it at process start.
	Interval func() time.Duration
	// LeaderOnly is true when the job only runs on the replica holding the
	// HA leader lease (common.IsLeader) — as opposed to running on every
	// master-capable replica regardless of leadership. The system-tasks
	// endpoint uses this to tell "this follower has never been leader, so
	// last_success_at==0 is standby" apart from "this task is actually
	// overdue".
	LeaderOnly bool
	// Active reports whether the job is currently scheduled to run at all
	// on this replica, independent of leadership — e.g.
	// channel-health-test's Active returns false when the operator has
	// AutoTestChannelEnabled=false, the code default. nil means "always
	// active"; channel-health-test supplies a real predicate instead of
	// nil — see each Register call site for which tasks pass which.
	// The system-tasks endpoint reports an inactive task as standby
	// (reason "disabled"), never overdue: a job the operator turned off
	// cannot be late for a schedule it isn't on.
	Active func() bool
}

var (
	mu    sync.Mutex
	tasks []Task
)

// Register adds one periodic task to the registry. Intended to be called
// once per task from its Start*WithContext entry point, before its
// goroutine is spawned. The registry does not dedupe: a name registered
// more than once in the same process (e.g. a Start*WithContext function
// invoked from more than one test in the same test binary) shows up as a
// repeated row in Snapshot() rather than being silently collapsed — callers
// that need an exact/short task list should not assume this list is short
// or duplicate-free.
//
// active follows the same nil-means-default convention as interval does
// not: unlike Interval, which every caller must supply, active may be nil
// to mean "always active" — most jobs have no notion of being
// administratively disabled and should pass nil.
func Register(name string, interval func() time.Duration, leaderOnly bool, active func() bool) {
	mu.Lock()
	defer mu.Unlock()
	tasks = append(tasks, Task{Name: name, Interval: interval, LeaderOnly: leaderOnly, Active: active})
}

// Snapshot returns a copy of every task registered so far, in registration
// order. Safe for concurrent use with Register.
func Snapshot() []Task {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Task, len(tasks))
	copy(out, tasks)
	return out
}
