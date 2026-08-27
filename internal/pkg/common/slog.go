package common

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// Global slog logger instance
var (
	// slogLogger is an atomic.Pointer so every read (each LogXxx and the
	// SysError/SysLog path, including detached goroutines) is race-free with a
	// test that resets it. Replaces the old `*slog.Logger` + `sync.Once`, whose
	// raw reassignment in tests raced ensureSlogInit's read (a -race finding).
	slogLogger    atomic.Pointer[slog.Logger]
	slogLevel     = new(slog.LevelVar) // Dynamic log level
	slogWriter    io.Writer
	slogErrWriter io.Writer
	slogMu        sync.RWMutex
	// slogJSONFormat records the format InitSlog last applied, so callers that
	// need to branch on it (e.g. the access-log middleware choosing between the
	// legacy "[GIN] ..." text line and a structured record) read the format
	// InitSlog actually built instead of re-reading LOG_FORMAT themselves,
	// which would drift from the GIN_MODE=release auto-select in
	// SlogConfigFromEnv (below, around the os.Getenv("GIN_MODE") check) the
	// moment LOG_FORMAT is left unset.
	slogJSONFormat atomic.Bool
)

// SlogConfig holds configuration for the slog logger
type SlogConfig struct {
	// Level is the minimum log level (default: Info)
	Level slog.Level
	// Writer is the output writer for info/debug logs (default: os.Stdout)
	Writer io.Writer
	// ErrWriter is the output writer for warn/error logs (default: os.Stderr)
	ErrWriter io.Writer
	// AddSource adds source file information to logs
	AddSource bool
	// JSONFormat uses JSON output format instead of text
	JSONFormat bool
	// TimeFormat is the time format string (default: "2006/01/02 - 15:04:05")
	TimeFormat string
}

// DefaultSlogConfig returns the default configuration
func DefaultSlogConfig() *SlogConfig {
	return &SlogConfig{
		Level:      slog.LevelInfo,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
		AddSource:  false,
		JSONFormat: false,
		TimeFormat: "2006/01/02 - 15:04:05",
	}
}

// SlogConfigFromEnv creates a SlogConfig from environment variables.
// Supported env vars:
//   - LOG_FORMAT: "json" or "text" (default: "text"; auto-selects "json" when GIN_MODE=release)
//   - LOG_LEVEL: "debug", "info", "warn", "error" (default: "info")
func SlogConfigFromEnv() *SlogConfig {
	cfg := DefaultSlogConfig()

	// Determine log format
	format := strings.ToLower(os.Getenv("LOG_FORMAT"))
	switch format {
	case "json":
		cfg.JSONFormat = true
	case "text":
		cfg.JSONFormat = false
	default:
		// Auto-select JSON in release mode
		if os.Getenv("GIN_MODE") == "release" {
			cfg.JSONFormat = true
		}
	}

	// Determine log level
	level := strings.ToLower(os.Getenv("LOG_LEVEL"))
	switch level {
	case "debug":
		cfg.Level = slog.LevelDebug
	case "warn", "warning":
		cfg.Level = slog.LevelWarn
	case "error":
		cfg.Level = slog.LevelError
	default:
		cfg.Level = slog.LevelInfo
	}

	return cfg
}

// logContextKey is used to store user_id in context.Context for slog extraction.
type logContextKey string

const logKeyUserID logContextKey = "log_user_id"

// WithUserID returns a context with user_id attached for structured log extraction.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, logKeyUserID, userID)
}

// contextHandler wraps any slog.Handler to inject trace context fields (trace_id, span_id,
// request_id, user_id) from context.Context into every log record.
// Used for JSON mode where the underlying handler has no context-extraction logic.
type contextHandler struct {
	inner slog.Handler
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if ctx != nil {
		// Inject trace_id and span_id from OTel span context.
		sc := trace.SpanContextFromContext(ctx)
		if sc.HasTraceID() {
			r.AddAttrs(slog.String("trace_id", sc.TraceID().String()))
		}
		if sc.HasSpanID() {
			r.AddAttrs(slog.String("span_id", sc.SpanID().String()))
		}
		// Inject request_id.
		if id := ctx.Value(RequestIdKey); id != nil {
			r.AddAttrs(slog.String("request_id", fmt.Sprintf("%v", id)))
		}
		// Inject user_id.
		if uid := ctx.Value(logKeyUserID); uid != nil {
			r.AddAttrs(slog.String("user_id", fmt.Sprintf("%v", uid)))
		}
	}
	return h.inner.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{inner: h.inner.WithGroup(name)}
}

// customHandler wraps slog.Handler to provide custom formatting
type customHandler struct {
	slog.Handler
	writer     io.Writer
	errWriter  io.Writer
	timeFormat string
	mu         *sync.Mutex
}

func (h *customHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Determine output writer based on level
	w := h.writer
	if r.Level >= slog.LevelWarn {
		w = h.errWriter
	}

	// Format level tag
	var levelTag string
	switch r.Level {
	case slog.LevelDebug:
		levelTag = "DEBUG"
	case slog.LevelInfo:
		levelTag = "INFO"
	case slog.LevelWarn:
		levelTag = "WARN"
	case slog.LevelError:
		levelTag = "ERR"
	default:
		levelTag = r.Level.String()
	}

	// Build the log line
	timeStr := r.Time.Format(h.timeFormat)

	// Extract context values for structured log correlation.
	requestID := "SYSTEM"
	traceID := ""
	if ctx != nil {
		if id := ctx.Value(RequestIdKey); id != nil {
			requestID = fmt.Sprintf("%v", id)
		}
		sc := trace.SpanContextFromContext(ctx)
		if sc.HasTraceID() {
			traceID = sc.TraceID().String()
		}
	}

	// Collect attributes
	var attrs []string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key != "" && a.Key != "msg" {
			attrs = append(attrs, fmt.Sprintf("%s=%v", a.Key, a.Value.Any()))
		}
		return true
	})

	// Format message
	msg := r.Message
	if len(attrs) > 0 {
		msg = fmt.Sprintf("%s | %s", msg, strings.Join(attrs, " "))
	}

	// Write formatted log line with optional trace_id.
	if traceID != "" {
		_, err := fmt.Fprintf(w, "[%s] %s | %s | trace=%s | %s\n", levelTag, timeStr, requestID, traceID, msg)
		return err
	}
	_, err := fmt.Fprintf(w, "[%s] %s | %s | %s\n", levelTag, timeStr, requestID, msg)
	return err
}

func (h *customHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &customHandler{
		Handler:    h.Handler.WithAttrs(attrs),
		writer:     h.writer,
		errWriter:  h.errWriter,
		timeFormat: h.timeFormat,
		mu:         h.mu,
	}
}

func (h *customHandler) WithGroup(name string) slog.Handler {
	return &customHandler{
		Handler:    h.Handler.WithGroup(name),
		writer:     h.writer,
		errWriter:  h.errWriter,
		timeFormat: h.timeFormat,
		mu:         h.mu,
	}
}

// InitSlog initializes the global slog logger with the given config and
// returns it, so lazy callers can use the freshly-created (local, non-nil)
// pointer without a second atomic load that a concurrent reset could nil.
func InitSlog(cfg *SlogConfig) *slog.Logger {
	if cfg == nil {
		cfg = DefaultSlogConfig()
	}

	slogMu.Lock()
	defer slogMu.Unlock()

	slogLevel.Set(cfg.Level)
	slogWriter = cfg.Writer
	slogErrWriter = cfg.ErrWriter
	slogJSONFormat.Store(cfg.JSONFormat)

	var handler slog.Handler
	opts := &slog.HandlerOptions{
		Level:     slogLevel,
		AddSource: cfg.AddSource,
	}

	if cfg.JSONFormat {
		// Wrap JSON handler with contextHandler to inject trace_id, span_id,
		// request_id, user_id from context into every JSON log entry.
		handler = &contextHandler{inner: slog.NewJSONHandler(cfg.Writer, opts)}
	} else {
		// Use custom handler for text format
		baseHandler := slog.NewTextHandler(cfg.Writer, opts)
		handler = &customHandler{
			Handler:    baseHandler,
			writer:     cfg.Writer,
			errWriter:  cfg.ErrWriter,
			timeFormat: cfg.TimeFormat,
			mu:         &sync.Mutex{},
		}
	}

	l := slog.New(handler)
	slogLogger.Store(l)
	slog.SetDefault(l)
	return l
}

// ensureSlogInit returns the live logger, lazily initializing it if unset.
// It returns the pointer (rather than leaving callers to Load() again) so each
// log call uses ONE captured, non-nil logger — closing the nil-deref window
// where a concurrent slogLogger.Store(nil) (tests) between an ensure and a
// separate Load() would panic, and halving the atomic loads on the hot path.
// InitSlog serializes on slogMu and is idempotent; a rare double-init from two
// concurrent first-callers is harmless (both build an identical default logger).
func ensureSlogInit() *slog.Logger {
	if l := slogLogger.Load(); l != nil {
		return l
	}
	return InitSlog(nil)
}

// GetSlogLogger returns the global slog logger
func GetSlogLogger() *slog.Logger {
	return ensureSlogInit()
}

// IsJSONLogFormat reports whether the format InitSlog last applied is JSON.
// Before the first InitSlog call it reports false (the DefaultSlogConfig
// text default), matching ensureSlogInit's lazy-init fallback.
func IsJSONLogFormat() bool {
	return slogJSONFormat.Load()
}

// SetSlogLevel dynamically sets the log level
func SetSlogLevel(level slog.Level) {
	ensureSlogInit()
	slogLevel.Set(level)
}

// SetSlogWriter sets the output writer for logs
func SetSlogWriter(w io.Writer) {
	slogMu.Lock()
	defer slogMu.Unlock()
	slogWriter = w
}

// SetSlogErrWriter sets the output writer for error logs
func SetSlogErrWriter(w io.Writer) {
	slogMu.Lock()
	defer slogMu.Unlock()
	slogErrWriter = w
}

// Structured logging functions that work with context

// LogInfo logs an info message with optional key-value pairs
func LogInfo(ctx context.Context, msg string, args ...any) {
	ensureSlogInit().InfoContext(ctx, msg, args...)
}

// LogWarn logs a warning message with optional key-value pairs
func LogWarn(ctx context.Context, msg string, args ...any) {
	ensureSlogInit().WarnContext(ctx, msg, args...)
}

// LogError logs an error message with optional key-value pairs
func LogError(ctx context.Context, msg string, args ...any) {
	ensureSlogInit().ErrorContext(ctx, msg, args...)
}

// LogDebug logs a debug message with optional key-value pairs
func LogDebug(ctx context.Context, msg string, args ...any) {
	if !DebugEnabled {
		return
	}
	ensureSlogInit().DebugContext(ctx, msg, args...)
}

// LogWithSource logs a message with source file information
func LogWithSource(ctx context.Context, level slog.Level, msg string, args ...any) {
	l := ensureSlogInit()
	// Get caller information
	_, file, line, ok := runtime.Caller(1)
	if ok {
		args = append(args, "source", fmt.Sprintf("%s:%d", file, line))
	}
	l.Log(ctx, level, msg, args...)
}

// Helper functions for common patterns

// LogErrorf logs a formatted error message
func LogErrorf(ctx context.Context, format string, args ...any) {
	LogError(ctx, fmt.Sprintf(format, args...))
}

// LogInfof logs a formatted info message
func LogInfof(ctx context.Context, format string, args ...any) {
	LogInfo(ctx, fmt.Sprintf(format, args...))
}

// LogWarnf logs a formatted warning message
func LogWarnf(ctx context.Context, format string, args ...any) {
	LogWarn(ctx, fmt.Sprintf(format, args...))
}

// LogDebugf logs a formatted debug message
func LogDebugf(ctx context.Context, format string, args ...any) {
	if DebugEnabled {
		LogDebug(ctx, fmt.Sprintf(format, args...))
	}
}

// Background returns a background context with optional request ID
func Background() context.Context {
	return context.Background()
}

// WithRequestID returns a context with request ID attached
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, RequestIdKey, requestID)
}

// Timer returns a function that logs the elapsed time when called
func Timer(ctx context.Context, name string) func() {
	start := time.Now()
	return func() {
		elapsed := time.Since(start)
		LogDebug(ctx, fmt.Sprintf("%s took %v", name, elapsed))
	}
}
