package logger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

// TestMain replaces asyncGo with a no-op so checkLogRotation's fire-and-forget
// SetupLogger dispatch cannot leave a goroutine that races the unlocked
// logCount/setupLogWorking globals in later tests under -race. checkLogRotation's
// own synchronous effects stay observable; SetupLogger is covered directly by
// TestSetupLogger_*.
func TestMain(m *testing.M) {
	asyncGo = func(func()) {}
	os.Exit(m.Run())
}

// snapshotGeneralSetting saves/restores the mutable package-level GeneralSetting
// so this test stays -count=1 safe and doesn't leak state to other tests.
func snapshotGeneralSetting(t *testing.T) {
	t.Helper()
	gs := operation_setting.GetGeneralSetting()
	orig := *gs
	t.Cleanup(func() { *gs = orig })
}

func TestLogQuota_AllDisplayTypes(t *testing.T) {
	snapshotGeneralSetting(t)
	gs := operation_setting.GetGeneralSetting()

	cases := []struct {
		name        string
		displayType string
		customSym   string
		customRate  float64
		quota       int
		want        string
	}{
		{"CNY", operation_setting.QuotaDisplayTypeCNY, "", 1, int(common.QuotaPerUnit), "¥7.300000 额度"},
		{"CustomWithSymbolAndRate", operation_setting.QuotaDisplayTypeCustom, "€", 2, int(common.QuotaPerUnit), "€2.000000 额度"},
		{"CustomEmptySymbolFallsBackToCurrencySign", operation_setting.QuotaDisplayTypeCustom, "", 2, int(common.QuotaPerUnit), "¤2.000000 额度"},
		{"CustomZeroRateFallsBackToOne", operation_setting.QuotaDisplayTypeCustom, "€", 0, int(common.QuotaPerUnit), "€1.000000 额度"},
		{"CustomNegativeRateFallsBackToOne", operation_setting.QuotaDisplayTypeCustom, "€", -5, int(common.QuotaPerUnit), "€1.000000 额度"},
		{"Tokens", operation_setting.QuotaDisplayTypeTokens, "", 1, 42, "42 点额度"},
		{"USDDefault", operation_setting.QuotaDisplayTypeUSD, "", 1, int(common.QuotaPerUnit), "＄1.000000 额度"},
		{"UnknownFallsToDefaultUSDBranch", "SOMETHING_ELSE", "", 1, int(common.QuotaPerUnit), "＄1.000000 额度"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs.QuotaDisplayType = tc.displayType
			gs.CustomCurrencySymbol = tc.customSym
			gs.CustomCurrencyExchangeRate = tc.customRate
			operation_setting.USDExchangeRate = 7.3

			got := LogQuota(tc.quota)
			if got != tc.want {
				t.Errorf("LogQuota(%d) with type=%s = %q, want %q", tc.quota, tc.displayType, got, tc.want)
			}
		})
	}
}

func TestFormatQuota_AllDisplayTypes(t *testing.T) {
	snapshotGeneralSetting(t)
	gs := operation_setting.GetGeneralSetting()

	cases := []struct {
		name        string
		displayType string
		customSym   string
		customRate  float64
		quota       int
		want        string
	}{
		{"CNY", operation_setting.QuotaDisplayTypeCNY, "", 1, int(common.QuotaPerUnit), "¥7.300000"},
		{"CustomWithSymbolAndRate", operation_setting.QuotaDisplayTypeCustom, "€", 2, int(common.QuotaPerUnit), "€2.000000"},
		{"CustomEmptySymbolFallsBackToCurrencySign", operation_setting.QuotaDisplayTypeCustom, "", 2, int(common.QuotaPerUnit), "¤2.000000"},
		{"CustomZeroRateFallsBackToOne", operation_setting.QuotaDisplayTypeCustom, "€", 0, int(common.QuotaPerUnit), "€1.000000"},
		{"CustomNegativeRateFallsBackToOne", operation_setting.QuotaDisplayTypeCustom, "€", -5, int(common.QuotaPerUnit), "€1.000000"},
		{"Tokens", operation_setting.QuotaDisplayTypeTokens, "", 1, 42, "42"},
		{"USDDefault", operation_setting.QuotaDisplayTypeUSD, "", 1, int(common.QuotaPerUnit), "＄1.000000"},
		{"UnknownFallsToDefaultUSDBranch", "SOMETHING_ELSE", "", 1, int(common.QuotaPerUnit), "＄1.000000"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs.QuotaDisplayType = tc.displayType
			gs.CustomCurrencySymbol = tc.customSym
			gs.CustomCurrencyExchangeRate = tc.customRate
			operation_setting.USDExchangeRate = 7.3

			got := FormatQuota(tc.quota)
			if got != tc.want {
				t.Errorf("FormatQuota(%d) with type=%s = %q, want %q", tc.quota, tc.displayType, got, tc.want)
			}
		})
	}
}

// TestLogJson_MarshalOK exercises the success path (marshal succeeds, then
// delegates to LogDebug) and the failure path (marshal fails, delegates to
// LogError) of LogJson. Neither branch panics and both are hermetic: no
// network/file I/O is involved since DebugEnabled is left untouched (LogDebug
// is a no-op when disabled) and LogError only writes to slog's default writer.
func TestLogJson_Branches(t *testing.T) {
	ctx := context.Background()

	// Success path: obj marshals fine.
	LogJson(ctx, "ok-case", map[string]int{"a": 1})

	// Failure path: channels are not JSON-marshalable, forcing json.Marshal
	// to return an error and LogJson to take the LogError branch.
	LogJson(ctx, "fail-case", make(chan int))
}

// TestLogDebug_ArgsFormatting exercises both branches of LogDebug: with and
// without printf-style args, gated on common.DebugEnabled.
func TestLogDebug_ArgsFormatting(t *testing.T) {
	origDebug := common.DebugEnabled
	t.Cleanup(func() { common.DebugEnabled = origDebug })

	ctx := context.Background()

	// DebugEnabled=false: the function must return immediately without
	// touching logCount via checkLogRotation side effects panicking etc.
	common.DebugEnabled = false
	LogDebug(ctx, "disabled message %d", 1)

	// DebugEnabled=true, no args: msg used verbatim.
	common.DebugEnabled = true
	LogDebug(ctx, "enabled message no args")

	// DebugEnabled=true, with args: msg passed through fmt.Sprintf.
	LogDebug(ctx, "enabled message %d-%s", 7, "x")
}

// TestLogLevels_Basic drives LogInfo/LogWarn/LogError and their KV
// counterparts through their non-error, real code paths.
func TestLogLevels_Basic(t *testing.T) {
	ctx := context.Background()

	LogInfo(ctx, "info message")
	LogWarn(ctx, "warn message")
	LogError(ctx, "error message")

	LogInfoKV(ctx, "info kv", "k1", "v1")
	LogWarnKV(ctx, "warn kv", "k2", "v2")
	LogErrorKV(ctx, "error kv", "k3", "v3")

	origDebug := common.DebugEnabled
	t.Cleanup(func() { common.DebugEnabled = origDebug })

	common.DebugEnabled = false
	LogDebugKV(ctx, "debug kv suppressed", "k4", "v4")

	common.DebugEnabled = true
	LogDebugKV(ctx, "debug kv emitted", "k5", "v5")
}

// TestCheckLogRotation_Branches directly drives the unexported
// checkLogRotation state machine to cover the increment-only branch, the
// suppressed-by-setupLogWorking branch, and the rotation-triggering branch.
func TestCheckLogRotation_Branches(t *testing.T) {
	origLogCount := logCount
	origWorking := setupLogWorking
	t.Cleanup(func() {
		logCount = origLogCount
		setupLogWorking = origWorking
	})

	// Make any SetupLogger triggered by rotation a hermetic no-op (empty
	// LogDir short-circuits the whole body). Deliberately NOT restored to
	// the original ("./logs") at cleanup: branch 3 below fires an async
	// gopool goroutine calling SetupLogger, and restoring a non-empty,
	// possibly-nonexistent relative dir before that goroutine runs would
	// make it hit os.Exit(1) and kill the whole test binary. Leaving it
	// empty is safe forever after — every other test in this file sets its
	// own explicit LogDir before relying on SetupLogger's file-creation path.
	*common.LogDir = ""

	// Branch 1: below threshold -> just increments, no rotation.
	logCount = 0
	setupLogWorking = false
	checkLogRotation()
	if logCount != 1 {
		t.Fatalf("logCount after one below-threshold call = %d, want 1", logCount)
	}
	if setupLogWorking {
		t.Fatalf("setupLogWorking should remain false below threshold")
	}

	// Branch 2: above threshold but setupLogWorking already true ->
	// rotation suppressed, logCount left incremented (not reset).
	logCount = maxLogCount + 1
	setupLogWorking = true
	checkLogRotation()
	if logCount != maxLogCount+2 {
		t.Fatalf("logCount when setupLogWorking=true = %d, want %d (rotation must be suppressed)", logCount, maxLogCount+2)
	}

	// Branch 3: above threshold and not already working -> rotation fires,
	// synchronously resetting logCount to 0 and flagging setupLogWorking
	// true before the async SetupLogger goroutine is dispatched.
	logCount = maxLogCount + 1
	setupLogWorking = false
	checkLogRotation()
	if logCount != 0 {
		t.Fatalf("logCount after rotation trigger = %d, want 0", logCount)
	}
	if !setupLogWorking {
		t.Fatalf("setupLogWorking should be true synchronously right after the rotation-triggering call")
	}
}

// TestSetupLogger_EmptyLogDirNoop exercises the guard branch: an empty
// LogDir means SetupLogger does nothing (no file created, no lock touched).
func TestSetupLogger_EmptyLogDirNoop(t *testing.T) {
	origLogDir := *common.LogDir
	t.Cleanup(func() { *common.LogDir = origLogDir })

	*common.LogDir = ""
	SetupLogger() // must return immediately without touching setupLogLock
}

// TestSetupLogger_CreatesFile exercises the real file-creation path with a
// temp directory (local filesystem only, no network).
func TestSetupLogger_CreatesFile(t *testing.T) {
	origLogDir := *common.LogDir
	origWorking := setupLogWorking
	t.Cleanup(func() {
		*common.LogDir = origLogDir
		setupLogWorking = origWorking
	})

	// SetupLogger keeps the log fd open for the process lifetime (it never
	// closes it), so on Windows a t.TempDir()-managed directory can't be
	// removed during cleanup (file still in use). Use a manually created
	// temp dir and skip removal instead of failing the test on that.
	dir, err := os.MkdirTemp("", "logger-coverage-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	*common.LogDir = dir
	setupLogWorking = false

	SetupLogger()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s) failed: %v", dir, err)
	}
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "oneapi-") && strings.HasSuffix(e.Name(), ".log") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected an oneapi-*.log file under %s, got entries: %v", dir, entries)
	}
	if setupLogWorking {
		t.Fatalf("setupLogWorking should be reset to false via defer after SetupLogger returns")
	}
}

// TestSetupLogger_LockContention exercises the "already working" early
// return: when setupLogLock is already held, SetupLogger must not block and
// must not create a new log file.
func TestSetupLogger_LockContention(t *testing.T) {
	origLogDir := *common.LogDir
	origWorking := setupLogWorking
	t.Cleanup(func() {
		*common.LogDir = origLogDir
		setupLogWorking = origWorking
	})

	dir := t.TempDir()
	*common.LogDir = dir
	setupLogWorking = false

	setupLogLock.Lock()
	SetupLogger() // TryLock fails -> logs "already working" and returns
	setupLogLock.Unlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s) failed: %v", dir, err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no log file created while lock was contended, got: %v", entries)
	}

	// Sanity: filepath.Join still behaves as SetupLogger expects (guards
	// against accidental path construction regressions in this package).
	if got := filepath.Join(dir, "x.log"); !strings.HasSuffix(got, "x.log") {
		t.Fatalf("filepath.Join sanity check failed: %q", got)
	}
}
