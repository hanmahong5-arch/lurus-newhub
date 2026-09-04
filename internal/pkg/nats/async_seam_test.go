package nats

import (
	"os"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// See repo.AsyncGo (internal/adapter/repo/async.go): tests here swap
// common.RDB / common.RedisEnabled and call into repo, whose fire-and-forget
// cache writers would otherwise outlive the test that spawned them and race
// its teardown. Production is unaffected — AsyncGo's value there is gopool.Go.
func TestMain(m *testing.M) {
	repo.AsyncGo = func(f func()) { f() }
	os.Exit(m.Run())
}
