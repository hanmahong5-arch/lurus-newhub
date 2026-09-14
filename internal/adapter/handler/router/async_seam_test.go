package router

import (
	"os"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/handler"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// Tests in this package swap common.RDB / common.RedisEnabled and restore them
// in t.Cleanup, and they call into repo, which spawns fire-and-forget cache
// writers. A detached goroutine outlives the test that spawned it, so the
// restore and the cache write run concurrently — a data race, and a nil
// dereference once the swapped-out client has been closed.
//
// Forcing the seam inline means every spawn has finished before the spawning
// test returns. See repo.AsyncGo (internal/adapter/repo/async.go) for the full
// story; production is unaffected, since AsyncGo's value there is gopool.Go.
//
// handler.AsyncGo is the same seam one layer up:
// TestRootSeal_GlobalConfigWritesRequireRoot (root_seal_test.go) POSTs to a
// fixed list of sealed routes, including /api/models/sync_channels
// (SyncAllChannelsNow), whose bare `go syncAllChannelModels(...)` used to
// outlive the request and could race a later test's repo.DB swap/close via
// repo.GetAllChannels once the swapped-out client is closed. Forcing the
// seam inline here removes that race (cycle-8 L10 repair, ruling A-F3).
func TestMain(m *testing.M) {
	repo.AsyncGo = func(f func()) { f() }
	handler.AsyncGo = func(f func()) { f() }
	os.Exit(m.Run())
}
