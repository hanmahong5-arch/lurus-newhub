package operation_setting

import (
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/setting/config"
)

// monitor_setting_lock_test.go — GetMonitorSetting is a read path that used to
// write.
//
// It is called from the automatic channel-test loop (handler/channel-test.go
// reads it four times per cycle) and it assigned the registered monitor_setting
// struct on EVERY call, with no lock, while the option-sync tick republishes
// that same struct from the options table. Two fixes, both pinned here:
// the write happens only when the environment asks for something the struct
// does not already hold, and when it does happen it is under the configuration
// write lock.
//
// Both assertions use the lock itself as the instrument, which is what makes
// them deterministic rather than a race that has to be lost on demand: while
// this test holds the configuration READ lock,
//
//   - a call that does not need to write returns (it only takes the read lock,
//     which is shared), and
//   - a call that does need to write blocks until the read lock is released
//     (a writer cannot proceed while a reader holds it).
//
// So "returns promptly" proves no write was attempted, and "blocks, then
// completes" proves the write went through the lock.
//
// The behaviour contract itself — 0 and negative disable, positive enables and
// sets the period, unparsable and unset leave the memory state alone — is
// pinned by fix_monitor_setting_test.go and by the TestGetMonitorSetting_*
// cases in cov_boot-settings_operation_setting_test.go, all of which still
// pass; this file only adds the two properties they cannot see.

func snapshotMonitorSetting(t *testing.T) {
	t.Helper()
	previous := monitorSetting
	t.Cleanup(func() { monitorSetting = previous })
}

func TestGetMonitorSetting_DoesNotWriteWhenTheEnvironmentAgrees(t *testing.T) {
	snapshotMonitorSetting(t)

	monitorSetting = MonitorSetting{AutoTestChannelEnabled: true, AutoTestChannelMinutes: 45}
	t.Setenv("CHANNEL_TEST_FREQUENCY", "45")

	done := make(chan struct{})
	config.RLock()
	go func() {
		GetMonitorSetting()
		close(done)
	}()

	select {
	case <-done:
		config.RUnlock()
	case <-time.After(2 * time.Second):
		config.RUnlock()
		<-done
		t.Fatal("GetMonitorSetting blocked on the configuration write lock even though the environment matches what is already published; it is writing the registered struct on every call")
	}

	if !monitorSetting.AutoTestChannelEnabled || monitorSetting.AutoTestChannelMinutes != 45 {
		t.Errorf("monitor setting = %+v, want the state it started in", monitorSetting)
	}
}

func TestGetMonitorSetting_TakesTheWriteLockWhenItHasToChangeSomething(t *testing.T) {
	snapshotMonitorSetting(t)

	monitorSetting = MonitorSetting{AutoTestChannelEnabled: false, AutoTestChannelMinutes: 10}
	t.Setenv("CHANNEL_TEST_FREQUENCY", "30")

	done := make(chan struct{})
	config.RLock()
	go func() {
		GetMonitorSetting()
		close(done)
	}()

	select {
	case <-done:
		config.RUnlock()
		t.Fatal("GetMonitorSetting published a new value while this goroutine held the configuration read lock; the write is not taking the write lock")
	case <-time.After(150 * time.Millisecond):
	}

	config.RUnlock()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("GetMonitorSetting never completed after the read lock was released")
	}

	if !monitorSetting.AutoTestChannelEnabled || monitorSetting.AutoTestChannelMinutes != 30 {
		t.Errorf("monitor setting = %+v, want the environment override to have been applied (enabled, 30 minutes)", monitorSetting)
	}
}
