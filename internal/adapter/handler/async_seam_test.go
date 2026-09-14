package handler

import (
	"os"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
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
// app.AsyncGo is the same kind of seam one layer down: PostConsumeQuota
// spawns the cost-spike window writer through it, and that writer reads
// common.RedisEnabled, which the relay fixtures restore in t.Cleanup — the
// race the -race gate caught once the fixtures started settling real rows.
// AsyncGo (this package) is the same seam again for SyncAllChannelsNow's
// syncAllChannelModels spawn — cycle-8 L10 repair, ruling A-F3: without
// this, a test that swaps repo.DB and restores it in t.Cleanup can race a
// still-running syncAllChannelModels goroutine from an earlier
// POST /api/models/sync_channels test, panicking on repo.DB==nil.
func TestMain(m *testing.M) {
	repo.AsyncGo = func(f func()) { f() }
	app.AsyncGo = func(f func()) { f() }
	AsyncGo = func(f func()) { f() }
	os.Exit(m.Run())
}
