package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

const maxLogCount = 1000000

var logCount atomic.Int64
var setupLogLock sync.Mutex
var setupLogWorking atomic.Bool

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

// FormatQuota formats quota value based on display type setting
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
