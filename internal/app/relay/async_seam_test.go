package relay

import (
	"os"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

// TestMain forces app.PostConsumeQuota's async side effects to run inline for
// the relay test binary too. Several relay tests call app.PostConsumeQuota,
// whose fire-and-forget gopool goroutines read package globals (e.g.
// common.RedisEnabled) via the cost-spike and quota-notify paths. Running them
// synchronously keeps those reads from outliving the spawning test and racing a
// later test's global-state teardown under the -race gate. See app.AsyncGo.
//
// It also allows private egress for the whole relay test binary: these tests
// dispatch through the real relay client to loopback httptest upstreams, which
// the transport-layer SSRF dial guard (app/relay_dial_guard.go) would otherwise
// block. The guard's own behavior is unit-tested in package app; here we exercise
// dispatch mechanics, so the local mock upstream is a legitimately-allowed target
// (equivalent to an operator setting AllowPrivateIp for a self-hosted LLM).
func TestMain(m *testing.M) {
	app.AsyncGo = func(f func()) { f() }
	// app.AsyncGo covers app's own spawns; the repo functions app calls spawn
	// their own cache writers. Missing this is what produced a DATA RACE in
	// internal/app on an unrelated pull request. See repo.AsyncGo.
	repo.AsyncGo = func(f func()) { f() }
	system_setting.GetFetchSetting().AllowPrivateIp = true
	os.Exit(m.Run())
}
