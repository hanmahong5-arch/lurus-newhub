package logger

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// boot_settings_captureSlog redirects the global slog output to an in-memory
// buffer for the duration of the test and restores the previous logger on
// cleanup, so assertions can inspect actual emitted log lines rather than
// merely asserting "no panic".
func boot_settings_captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	common.InitSlog(&common.SlogConfig{
		Level:      -10, // effectively "all levels" (below slog.LevelDebug)
		Writer:     buf,
		ErrWriter:  buf,
		JSONFormat: false,
		TimeFormat: "2006/01/02 - 15:04:05",
	})
	t.Cleanup(func() {
		common.InitSlog(common.DefaultSlogConfig())
	})
	return buf
}

// --- LogQuota / FormatQuota ---

func boot_settings_resetGeneralSetting(t *testing.T) {
	t.Helper()
	orig := *operation_setting.GetGeneralSetting()
	t.Cleanup(func() { *operation_setting.GetGeneralSetting() = orig })
}

func TestLogQuota_USDDefault(t *testing.T) {
	boot_settings_resetGeneralSetting(t)
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD

	// 500000 quota units = $1.00 at QuotaPerUnit=500000.
	got := LogQuota(int(common.QuotaPerUnit))
	if !strings.Contains(got, "＄1.000000") {
		t.Fatalf("expected USD-formatted quota containing ＄1.000000, got %q", got)
	}
}

func TestLogQuota_CNYAppliesExchangeRate(t *testing.T) {
	boot_settings_resetGeneralSetting(t)
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeCNY
	origRate := operation_setting.USDExchangeRate
	operation_setting.USDExchangeRate = 7.0
	t.Cleanup(func() { operation_setting.USDExchangeRate = origRate })

	got := LogQuota(int(common.QuotaPerUnit)) // 1 USD worth
	if !strings.Contains(got, "¥7.000000") {
		t.Fatalf("expected CNY quota of ¥7.000000 (1 USD * 7.0 rate), got %q", got)
	}
}

func TestLogQuota_CustomCurrency_SymbolAndRateFallbacks(t *testing.T) {
	boot_settings_resetGeneralSetting(t)
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeCustom
	operation_setting.GetGeneralSetting().CustomCurrencySymbol = ""
	operation_setting.GetGeneralSetting().CustomCurrencyExchangeRate = 0 // must fall back to 1

	got := LogQuota(int(common.QuotaPerUnit))
	// Empty symbol falls back to the generic currency glyph, and a
	// non-positive rate falls back to 1 (par with USD) rather than
	// dividing/multiplying by zero.
	if !strings.Contains(got, "¤1.000000") {
		t.Fatalf("expected ¤1.000000 with empty-symbol/zero-rate fallbacks, got %q", got)
	}
}

func TestLogQuota_Tokens(t *testing.T) {
	boot_settings_resetGeneralSetting(t)
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens

	got := LogQuota(12345)
	if got != "12345 点额度" {
		t.Fatalf("expected raw token count '12345 点额度', got %q", got)
	}
}

func TestFormatQuota_AllBranches(t *testing.T) {
	boot_settings_resetGeneralSetting(t)

	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	if got := FormatQuota(int(common.QuotaPerUnit)); got != "＄1.000000" {
		t.Fatalf("expected ＄1.000000, got %q", got)
	}

	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	if got := FormatQuota(42); got != "42" {
		t.Fatalf("expected raw '42', got %q", got)
	}

	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeCustom
	operation_setting.GetGeneralSetting().CustomCurrencySymbol = "€"
	operation_setting.GetGeneralSetting().CustomCurrencyExchangeRate = 2
	if got := FormatQuota(int(common.QuotaPerUnit)); got != "€2.000000" {
		t.Fatalf("expected €2.000000 (1 USD * rate 2), got %q", got)
	}
}

// --- LogInfo / LogWarn / LogError / LogDebug ---

func TestLogInfo_WritesMessageToOutput(t *testing.T) {
	buf := boot_settings_captureSlog(t)
	LogInfo(context.Background(), "hub started successfully")
	if !strings.Contains(buf.String(), "hub started successfully") {
		t.Fatalf("expected LogInfo message in output, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "[INFO]") {
		t.Fatalf("expected INFO level tag, got %q", buf.String())
	}
}

func TestLogWarn_WritesMessageToOutput(t *testing.T) {
	buf := boot_settings_captureSlog(t)
	LogWarn(context.Background(), "quota nearing threshold")
	if !strings.Contains(buf.String(), "quota nearing threshold") || !strings.Contains(buf.String(), "[WARN]") {
		t.Fatalf("expected WARN-tagged message, got %q", buf.String())
	}
}

func TestLogError_WritesMessageToOutput(t *testing.T) {
	buf := boot_settings_captureSlog(t)
	LogError(context.Background(), "channel dial failed")
	if !strings.Contains(buf.String(), "channel dial failed") || !strings.Contains(buf.String(), "[ERR]") {
		t.Fatalf("expected ERR-tagged message, got %q", buf.String())
	}
}

func TestLogDebug_GatedByDebugEnabled(t *testing.T) {
	buf := boot_settings_captureSlog(t)
	origDebug := common.DebugEnabled
	t.Cleanup(func() { common.DebugEnabled = origDebug })

	common.DebugEnabled = false
	LogDebug(context.Background(), "debug detail %s", "suppressed")
	if strings.Contains(buf.String(), "suppressed") {
		t.Fatalf("expected debug message suppressed when DebugEnabled=false, got %q", buf.String())
	}

	common.DebugEnabled = true
	LogDebug(context.Background(), "debug detail %s", "emitted")
	if !strings.Contains(buf.String(), "debug detail emitted") {
		t.Fatalf("expected formatted debug message when DebugEnabled=true, got %q", buf.String())
	}
}

func TestLogDebug_NoArgsSkipsFormatting(t *testing.T) {
	buf := boot_settings_captureSlog(t)
	origDebug := common.DebugEnabled
	common.DebugEnabled = true
	t.Cleanup(func() { common.DebugEnabled = origDebug })

	// A message containing a literal '%s' with zero args must be passed
	// through verbatim, not fed to fmt.Sprintf (which would leave a stray
	// "%!s(MISSING)").
	LogDebug(context.Background(), "raw percent %s literal")
	if !strings.Contains(buf.String(), "raw percent %s literal") {
		t.Fatalf("expected literal message preserved with no args, got %q", buf.String())
	}
}

// --- LogInfoKV / LogWarnKV / LogErrorKV / LogDebugKV ---

func TestLogInfoKV_IncludesKeyValuePairs(t *testing.T) {
	buf := boot_settings_captureSlog(t)
	LogInfoKV(context.Background(), "channel test complete", "channel_id", 42, "status", "ok")
	out := buf.String()
	if !strings.Contains(out, "channel_id=42") || !strings.Contains(out, "status=ok") {
		t.Fatalf("expected structured key-value pairs in output, got %q", out)
	}
}

func TestLogWarnKV_IncludesKeyValuePairs(t *testing.T) {
	buf := boot_settings_captureSlog(t)
	LogWarnKV(context.Background(), "rate limit approaching", "group", "vip")
	if !strings.Contains(buf.String(), "group=vip") {
		t.Fatalf("expected group=vip in output, got %q", buf.String())
	}
}

func TestLogErrorKV_IncludesKeyValuePairs(t *testing.T) {
	buf := boot_settings_captureSlog(t)
	LogErrorKV(context.Background(), "billing outbox failed", "attempt", 3)
	if !strings.Contains(buf.String(), "attempt=3") {
		t.Fatalf("expected attempt=3 in output, got %q", buf.String())
	}
}

func TestLogDebugKV_GatedByDebugEnabled(t *testing.T) {
	buf := boot_settings_captureSlog(t)
	origDebug := common.DebugEnabled
	t.Cleanup(func() { common.DebugEnabled = origDebug })

	common.DebugEnabled = false
	LogDebugKV(context.Background(), "should not appear", "k", "v")
	if strings.Contains(buf.String(), "should not appear") {
		t.Fatalf("expected debug KV message suppressed when DebugEnabled=false")
	}

	common.DebugEnabled = true
	LogDebugKV(context.Background(), "should appear", "k", "v")
	if !strings.Contains(buf.String(), "should appear") || !strings.Contains(buf.String(), "k=v") {
		t.Fatalf("expected debug KV message with k=v when DebugEnabled=true, got %q", buf.String())
	}
}

// --- LogJson ---

func TestLogJson_MarshalableObjectLogsJSON(t *testing.T) {
	buf := boot_settings_captureSlog(t)
	origDebug := common.DebugEnabled
	common.DebugEnabled = true
	t.Cleanup(func() { common.DebugEnabled = origDebug })

	LogJson(context.Background(), "request payload", map[string]int{"tokens": 128})
	out := buf.String()
	if !strings.Contains(out, "request payload") || !strings.Contains(out, `"tokens":128`) {
		t.Fatalf("expected marshaled JSON payload in debug output, got %q", out)
	}
}

func TestLogJson_UnmarshalableObjectLogsError(t *testing.T) {
	buf := boot_settings_captureSlog(t)

	// A Go channel cannot be JSON-marshaled — this must route to LogError
	// with a "json marshal failed" message rather than panicking or silently
	// dropping the log line.
	LogJson(context.Background(), "bad payload", make(chan int))
	out := buf.String()
	if !strings.Contains(out, "json marshal failed") {
		t.Fatalf("expected marshal-failure error message, got %q", out)
	}
	if !strings.Contains(out, "[ERR]") {
		t.Fatalf("expected the marshal failure logged at ERROR level, got %q", out)
	}
}

// --- SetupLogger ---

func TestSetupLogger_EmptyLogDirIsNoOp(t *testing.T) {
	origDir := *common.LogDir
	*common.LogDir = ""
	t.Cleanup(func() { *common.LogDir = origDir })

	origWriter := gin.DefaultWriter
	SetupLogger() // must return without touching gin's writers
	if gin.DefaultWriter != origWriter {
		t.Fatalf("expected no writer change when LogDir is empty")
	}
}

func TestSetupLogger_CreatesLogFileWhenDirSet(t *testing.T) {
	origDir := *common.LogDir
	// SetupLogger opens the log file and wires it into gin.DefaultWriter for
	// the rest of the process; the fd is never explicitly closed by this
	// package, so on Windows t.TempDir()'s auto-cleanup would fail with
	// "file in use". Use a manually managed temp dir and best-effort cleanup.
	tmpDir, err := os.MkdirTemp("", "logger-setup-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	*common.LogDir = tmpDir
	t.Cleanup(func() {
		*common.LogDir = origDir
		_ = os.RemoveAll(tmpDir) // best-effort; fd may still be held on Windows
	})

	SetupLogger()

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read temp log dir: %v", err)
	}
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "oneapi-") && strings.HasSuffix(e.Name(), ".log") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a oneapi-*.log file created in %s, entries: %v", tmpDir, entries)
	}
}

func TestSetupLogger_ConcurrentCallSkipsWhenLocked(t *testing.T) {
	// Business guarantee: rotation must never run two file-open cycles at
	// once (would corrupt gin.DefaultWriter mid-write). Hold the lock
	// ourselves and verify a concurrent SetupLogger call returns immediately
	// without creating a second file / blocking.
	origDir := *common.LogDir
	tmpDir := t.TempDir()
	*common.LogDir = tmpDir
	t.Cleanup(func() { *common.LogDir = origDir })

	setupLogLock.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		SetupLogger() // should hit TryLock() == false and return promptly
	}()

	select {
	case <-done:
		// expected: returns without blocking on the held lock
	case <-time.After(2 * time.Second):
		setupLogLock.Unlock()
		t.Fatalf("SetupLogger blocked instead of skipping under contention")
	}
	setupLogLock.Unlock()

	entries, _ := os.ReadDir(tmpDir)
	if len(entries) != 0 {
		t.Fatalf("expected no log file created while lock was held, got %v", entries)
	}
}

// --- checkLogRotation (unexported, internal test) ---

// TestCheckLogRotation_TriggersResetAtThreshold is the only test covering the
// dispatch branch of checkLogRotation (logger.go:93-99): counter reset, the
// CompareAndSwap that elects the single rotating caller, the gopool.Go
// dispatch, and SetupLogger's deferred flag release. The fix_log_rotation
// tests all preset setupLogWorking=true (or stay below the threshold), so they
// deliberately never reach it.
func TestCheckLogRotation_TriggersResetAtThreshold(t *testing.T) {
	origCount := logCount.Load()
	origWorking := setupLogWorking.Load()
	origDir := *common.LogDir
	*common.LogDir = "" // keep the async SetupLogger cheap/no-op
	t.Cleanup(func() {
		logCount.Store(origCount)
		setupLogWorking.Store(origWorking)
		*common.LogDir = origDir
	})

	logCount.Store(maxLogCount) // one increment away from tripping the threshold
	setupLogWorking.Store(false)

	checkLogRotation() // logCount becomes maxLogCount+1 > maxLogCount -> resets

	// The reset happens synchronously before the async SetupLogger goroutine
	// is dispatched, so logCount must already be 0 here.
	if got := logCount.Load(); got != 0 {
		t.Fatalf("expected logCount reset to 0 after crossing threshold, got %d", got)
	}

	// setupLogWorking is flipped true synchronously by the CompareAndSwap, then
	// the dispatched goroutine's SetupLogger defer flips it back to false; poll
	// briefly. The poll only exits after SetupLogger has finished reading
	// *common.LogDir, so the Cleanup restore above cannot race it.
	deadline := time.Now().Add(2 * time.Second)
	for setupLogWorking.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if setupLogWorking.Load() {
		t.Fatalf("expected setupLogWorking to settle back to false once the async rotation completes")
	}
}

// NOTE: the below-threshold and rotation-in-flight cases that used to live here
// are covered line-for-line by TestFixCheckLogRotation_BelowThresholdJustCounts
// and TestFixCheckLogRotation_CounterBoundedWhileRotating in
// fix_log_rotation_test.go — the latter of which asserts the counter now stays
// bounded, which is the opposite of what the old test locked in.

// --- concurrent writers (business requirement: logging must be goroutine-safe) ---

func TestLogInfo_ConcurrentWritesDoNotRace(t *testing.T) {
	buf := &syncBuffer{}
	common.InitSlog(&common.SlogConfig{
		Level:      -10,
		Writer:     buf,
		ErrWriter:  buf,
		JSONFormat: false,
		TimeFormat: "2006/01/02 - 15:04:05",
	})
	t.Cleanup(func() { common.InitSlog(common.DefaultSlogConfig()) })

	var wg sync.WaitGroup
	const n = 50
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			LogInfo(context.Background(), "concurrent log line")
		}(i)
	}
	wg.Wait()

	out := buf.String()
	if strings.Count(out, "concurrent log line") != n {
		t.Fatalf("expected %d log lines written without interleaving corruption, got %d in output of length %d",
			n, strings.Count(out, "concurrent log line"), len(out))
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
