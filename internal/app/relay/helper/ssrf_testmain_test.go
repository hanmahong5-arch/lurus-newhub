package helper

import (
	"os"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

// TestMain allows private egress for this test binary: the relay helper tests
// dispatch through the real relay client to loopback httptest upstreams, which
// the transport-layer SSRF dial guard (app/relay_dial_guard.go) would otherwise
// block. The guard's own behavior is unit-tested in package app; here we exercise
// dispatch mechanics, so the local mock upstream is a legitimately-allowed target
// (equivalent to an operator setting AllowPrivateIp for a self-hosted LLM).
func TestMain(m *testing.M) {
	system_setting.GetFetchSetting().AllowPrivateIp = true
	os.Exit(m.Run())
}
