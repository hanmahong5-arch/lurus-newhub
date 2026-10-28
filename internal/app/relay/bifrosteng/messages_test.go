package bifrosteng

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func TestShouldHandleMessages_Gates(t *testing.T) {
	t.Setenv("RELAY_ENGINE", "legacy")
	ResetConfigForTest()
	if ShouldHandleMessages(constant.APITypeAnthropic, false) {
		t.Fatal("legacy engine must not handle messages")
	}

	t.Setenv("RELAY_ENGINE", "bifrost")
	t.Setenv("RELAY_BIFROST_MODES", "chat,messages")
	ResetConfigForTest()
	Shutdown()
	// Without Init, Ready() is false.
	if ShouldHandleMessages(constant.APITypeAnthropic, false) {
		t.Fatal("unready bifrost must not handle messages")
	}
	if ShouldHandleMessages(constant.APITypeAnthropic, true) {
		t.Fatal("pass-through must not handle messages")
	}
	if ModeEnabled(ModeMessages) != true {
		t.Fatal("messages mode should be enabled")
	}
}
