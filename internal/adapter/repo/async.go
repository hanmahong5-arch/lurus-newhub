package repo

import "github.com/bytedance/gopkg/util/gopool"

// AsyncGo runs a fire-and-forget side effect — cache writes, log exports — off
// the caller's path. Every such spawn in this package goes through it.
//
// It exists as a variable, not a direct gopool.Go call, because those
// goroutines read package globals (common.RDB, common.RedisEnabled) that
// production sets once at startup and never touches again, while tests swap
// them per-test and restore them in t.Cleanup. A detached goroutine outlives
// the test that spawned it, so the read and the restore run concurrently: a
// data race, and when the swapped-out client has already been Closed, a nil
// dereference inside the pool.
//
// internal/app already learned this and added the same seam for its own
// spawns (AsyncGo in quota.go, forced inline by TestMain in
// async_seam_test.go). That seam had a hole: PostConsumeQuota reaches
// IncreaseTokenQuota here, which spawned through gopool directly and so was
// never covered. Observed as a -race failure in CI on an unrelated PR, with
// the cache writer racing another test's miniredis teardown.
//
// Production behaviour is unchanged: the default value IS gopool.Go, and
// nothing outside a test ever assigns to this.
var AsyncGo = gopool.Go
