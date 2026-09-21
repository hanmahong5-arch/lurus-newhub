package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

const maxLogCount = 1000000

// defaultLogFileRetain is how many rotated oneapi-*.log files pruneLogFiles
// keeps when LOG_FILE_RETAIN isn't set.
const defaultLogFileRetain = 3

var logCount atomic.Int64
var setupLogLock sync.Mutex
var setupLogWorking atomic.Bool

// currentLogFd is the *os.File SetupLogger last swapped into
// gin.DefaultWriter/DefaultErrorWriter. Tracked so the previous fd can be
// closed instead of leaked on every rotation.
var currentLogFd atomic.Pointer[os.File]

// logFdCloseGrace is how long SetupLogger waits before closing the fd it
// just replaced. A concurrent goroutine may have already read the old
// gin.DefaultWriter/ErrorWriter value before this rotation's swap landed
// (there is no lock around those package vars on the read side), so closing
// immediately could turn an in-flight write into an error; a short grace
// period lets any such write finish first. Var, not const, so tests can
// shrink it instead of sleeping the production value.
var logFdCloseGrace = 2 * time.Second

// SetupLogger configures file-based logging with rotation
func SetupLogger() {
	defer func() {
		setupLogWorking.Store(false)
	}()
	if *common.LogDir != "" {
		ok := setupLogLock.TryLock()
		if !ok {
			slog.Info("setup log is already working")
			return
		}
		defer func() {
			setupLogLock.Unlock()
		}()
		logPath := filepath.Join(*common.LogDir, fmt.Sprintf("oneapi-%s.log", time.Now().Format("20060102150405")))
		fd, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			slog.Error("failed to open log file", "error", err)
			os.Exit(1)
		}
		gin.DefaultWriter = io.MultiWriter(os.Stdout, fd)
		gin.DefaultErrorWriter = io.MultiWriter(os.Stderr, fd)

		// Update slog writers as well
		common.SetSlogWriter(gin.DefaultWriter)
		common.SetSlogErrWriter(gin.DefaultErrorWriter)

		if prev := currentLogFd.Swap(fd); prev != nil {
			// Read the grace period here, on the caller's goroutine, so the
			// closer never touches the package variable (a test shrinks it
			// between SetupLogger calls; reading it inside the goroutine
			// raced with that write under -race).
			grace := logFdCloseGrace
			go func(f *os.File) {
				time.Sleep(grace)
				_ = f.Close()
			}(prev)
		}

		pruneLogFiles(*common.LogDir, common.GetEnvOrDefault("LOG_FILE_RETAIN", defaultLogFileRetain))
	}
}

// pruneLogFiles keeps only the newest keep rotated oneapi-*.log files in
// dir, deleting the rest. Rotation (above) creates a new file every time
// checkLogRotation trips; nothing removed the old ones, so a long-lived pod
// (deploy/k8s: data is an emptyDir, no persistent disk, 30G root on R6) grew
// one file per rotation forever. oneapi-<YYYYMMDDHHMMSS>.log's name is
// itself lexicographically sortable by creation time, so a plain string
// sort is enough — no need to stat mtimes.
func pruneLogFiles(dir string, keep int) {
	// keep<=0 (LOG_FILE_RETAIN unset to 0, or set to a negative value) falls
	// back to defaultLogFileRetain rather than disabling pruning: a value of
	// 0 has no "never prune, grow the emptyDir forever" meaning here.
	if keep <= 0 {
		keep = defaultLogFileRetain
	}
	matches, err := filepath.Glob(filepath.Join(dir, "oneapi-*.log"))
	if err != nil || len(matches) <= keep {
		return
	}
	sort.Strings(matches)
	for _, old := range matches[:len(matches)-keep] {
		if rmErr := os.Remove(old); rmErr != nil {
			slog.Warn("failed to prune rotated log file", "file", old, "error", rmErr)
		}
	}
}

// LogInfo logs an info message with request context
func LogInfo(ctx context.Context, msg string) {
	common.LogInfo(ctx, msg)
	checkLogRotation()
}

// LogWarn logs a warning message with request context
func LogWarn(ctx context.Context, msg string) {
	common.LogWarn(ctx, msg)
	checkLogRotation()
}

// LogError logs an error message with request context
func LogError(ctx context.Context, msg string) {
	common.LogError(ctx, msg)
	checkLogRotation()
}

// LogDebug logs a debug message with request context
// Only logs when DebugEnabled is true
func LogDebug(ctx context.Context, msg string, args ...any) {
	if common.DebugEnabled {
		if len(args) > 0 {
			msg = fmt.Sprintf(msg, args...)
		}
		common.LogDebug(ctx, msg)
		checkLogRotation()
	}
}

// checkLogRotation checks if log rotation is needed
func checkLogRotation() {
	if logCount.Add(1) <= maxLogCount {
		return
	}
	// 越过阈值就归零：轮转已在进行时若不归零，计数会随每条日志无界增长。
	logCount.Store(0)
	// 只有抢到标志位的调用方才启动轮转，避免并发重复触发。
	if setupLogWorking.CompareAndSwap(false, true) {
		gopool.Go(func() {
			SetupLogger()
		})
	}
}

// LogQuota formats quota for display based on display type setting
func LogQuota(quota int) string {
	// New logic: output based on quota display type
	q := float64(quota)
	switch operation_setting.GetQuotaDisplayType() {
	case operation_setting.QuotaDisplayTypeCNY:
		usd := q / common.QuotaPerUnit
		cny := usd * operation_setting.USDExchangeRate
		return fmt.Sprintf("¥%.6f 额度", cny)
	case operation_setting.QuotaDisplayTypeCustom:
		usd := q / common.QuotaPerUnit
		rate := operation_setting.GetGeneralSetting().CustomCurrencyExchangeRate
		symbol := operation_setting.GetGeneralSetting().CustomCurrencySymbol
		if symbol == "" {
			symbol = "¤"
		}
		if rate <= 0 {
			rate = 1
		}
		v := usd * rate
		return fmt.Sprintf("%s%.6f 额度", symbol, v)
	case operation_setting.QuotaDisplayTypeTokens:
		return fmt.Sprintf("%d 点额度", quota)
	default: // USD
		return fmt.Sprintf("＄%.6f 额度", q/common.QuotaPerUnit)
	}
}

// FormatQuotaASCII renders a quota as a plain-ASCII USD amount, whatever the
// operator has set QuotaDisplayType to. It exists for messages that go out on
// the API wire.
//
// FormatQuota below is display-type dependent by design — the operator picks
// the currency their logs and console speak. That is the wrong dependency for
// an error.message: a machine-facing 402 became "＄0.000002" on a default
// deployment (the fullwidth U+FF04 this package has carried from upstream)
// and "¥0.0000142" on a CNY one. The cycle-13 AST language gate did not see
// either, because it scans string LITERALS under middleware/repo/relay and
// this text is interpolated in from internal/pkg/logger — found instead by a
// live UAT probe on 2026-09-21, after the claim "the 402 is ASCII on all
// three wires" had already been written down for the middleware path that
// was true of.
//
// The unit is the API's own: quota / common.QuotaPerUnit, the baseline-USD
// number every other wire field is denominated in.
func FormatQuotaASCII(quota int) string {
	return fmt.Sprintf("$%.6f", float64(quota)/common.QuotaPerUnit)
}

// FormatQuota formats quota value based on display type setting.
// NOT for wire messages — see FormatQuotaASCII above.
func FormatQuota(quota int) string {
	q := float64(quota)
	switch operation_setting.GetQuotaDisplayType() {
	case operation_setting.QuotaDisplayTypeCNY:
		usd := q / common.QuotaPerUnit
		cny := usd * operation_setting.USDExchangeRate
		return fmt.Sprintf("¥%.6f", cny)
	case operation_setting.QuotaDisplayTypeCustom:
		usd := q / common.QuotaPerUnit
		rate := operation_setting.GetGeneralSetting().CustomCurrencyExchangeRate
		symbol := operation_setting.GetGeneralSetting().CustomCurrencySymbol
		if symbol == "" {
			symbol = "¤"
		}
		if rate <= 0 {
			rate = 1
		}
		v := usd * rate
		return fmt.Sprintf("%s%.6f", symbol, v)
	case operation_setting.QuotaDisplayTypeTokens:
		return fmt.Sprintf("%d", quota)
	default:
		return fmt.Sprintf("＄%.6f", q/common.QuotaPerUnit)
	}
}

// LogJson logs an object as JSON (for testing only)
func LogJson(ctx context.Context, msg string, obj any) {
	jsonStr, err := json.Marshal(obj)
	if err != nil {
		LogError(ctx, fmt.Sprintf("json marshal failed: %s", err.Error()))
		return
	}
	LogDebug(ctx, fmt.Sprintf("%s | %s", msg, string(jsonStr)))
}

// Structured logging helpers with key-value pairs

// LogInfoKV logs an info message with key-value pairs
func LogInfoKV(ctx context.Context, msg string, args ...any) {
	common.LogInfo(ctx, msg, args...)
	checkLogRotation()
}

// LogWarnKV logs a warning message with key-value pairs
func LogWarnKV(ctx context.Context, msg string, args ...any) {
	common.LogWarn(ctx, msg, args...)
	checkLogRotation()
}

// LogErrorKV logs an error message with key-value pairs
func LogErrorKV(ctx context.Context, msg string, args ...any) {
	common.LogError(ctx, msg, args...)
	checkLogRotation()
}

// LogDebugKV logs a debug message with key-value pairs
func LogDebugKV(ctx context.Context, msg string, args ...any) {
	if common.DebugEnabled {
		common.LogDebug(ctx, msg, args...)
		checkLogRotation()
	}
}
