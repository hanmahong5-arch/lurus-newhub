package handler

import (
	"os"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
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
// AsyncGo (this package) is the same seam again for the fire-and-forget
// spawns listed in async_seam_structural_test.go's scan — originally
// SyncAllChannelsNow's syncAllChannelModels spawn (cycle-8 L10 repair, ruling
// A-F3: without this, a test that swaps repo.DB and restores it in t.Cleanup
// can race a still-running syncAllChannelModels goroutine from an earlier
// POST /api/models/sync_channels test, panicking on repo.DB==nil), and since
// cycle 12 also the channel-cache refreshes in v2_channel.go, the
// disable-channel / OpenRouter-cooldown spawns in processChannelError
// (relay.go) and the download bookkeeping in release.go.
// governance.AsyncGo is the fourth seam: governance.RecordAuditEvent hands
// the event to whichever AuditWriter was current at call time, and the
// writers these tests install are bound to a per-test *gorm.DB their cleanup
// closes (see pinAuditWriter in audit_writer_pin_test.go). Detached, that
// write and that close have no ordering.
func TestMain(m *testing.M) {
	repo.AsyncGo = func(f func()) { f() }
	app.AsyncGo = func(f func()) { f() }
	AsyncGo = func(f func()) { f() }
	governance.AsyncGo = func(f func()) { f() }
	os.Exit(m.Run())
}
