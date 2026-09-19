package repo

// gorm_logger.go — cycle-12 L8. The one place that decides how this process
// opens a database connection: the gorm.Config (including its logger) and the
// pool names the /metrics collectors use.
//
// What changed and why, against gorm's logger.Default which chooseDB used to
// fall back to (gorm.io/gorm/logger, v1.25.12):
//
//	Colorful:true                   ANSI escapes written into a stream the
//	                                deployment configures as JSON
//	                                (deploy/k8s/r6-stage/deployment.yaml sets
//	                                LOG_FORMAT=json). gorm swaps colour into
//	                                each of its six format strings under this
//	                                flag (infoStr, warnStr, errStr, traceStr,
//	                                traceWarnStr, traceErrStr), so no gorm line
//	                                was escape-free inside that JSON log.
//	IgnoreRecordNotFoundError:false a miss on First() printed as an error.
//	ParameterizedQueries:false      bound values interpolated into the logged
//	                                SQL. On this codebase that class of value
//	                                includes channels.key and tokens.key.
//	writer = os.Stdout via log.New  bypassed common.SysLog/SysError entirely,
//	                                so gorm lines carried no level and no
//	                                structure.
//
// Info/Warn/Error delegate to gorm's own logger.New (built from the same
// Config, which is what keeps Colorful load-bearing rather than decorative).
// Trace is implemented here instead of delegating, for two reasons gorm gives
// no hook for: the sink has to be chosen per branch (errors to
// common.SysError, the rest to common.SysLog — with Colorful off, gorm formats
// its error trace and its slow trace identically, so the branch cannot be
// recovered downstream of the Writer), and the slow branch has to be counted.
// Delegating Trace would also cost the caller site: gorm resolves it with
// utils.FileWithLineNum, which returns the first frame outside gorm's own
// source directory — through a wrapper that is the wrapper. Measured: with
// the Info branch delegating, the logged line named gorm_logger.go rather
// than the caller (TestGormLogger_TraceNamesTheCallerNotItself, run against
// that mutation 2026-09-19).

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// dbPoolNameMain / dbPoolNameLog are the db_name label values this process
// publishes pool telemetry under (metrics.RegisterDBStats in main.go) and the
// db label values the slow-query counter uses. Declared once so the two
// cannot drift apart.
const (
	dbPoolNameMain = "newhub"
	dbPoolNameLog  = "newhub_log"
)

// defaultSlowQueryMs is gorm's own default threshold, kept so this change does
// not also retune what counts as slow. DB_SLOW_QUERY_MS overrides it; 0
// disables the slow branch, which is gorm's own meaning for a zero
// SlowThreshold (its logger.Trace checks SlowThreshold != 0).
const defaultSlowQueryMs = 200

// sprintfForGorm renders one log call into a single line. gorm's format
// strings embed a newline between the caller site and the SQL; collapsing it
// keeps one log event to one record in the JSON stream.
func sprintfForGorm(format string, args ...any) string {
	return strings.TrimSpace(strings.ReplaceAll(fmt.Sprintf(format, args...), "\n", " "))
}

// gormWriterFunc adapts a plain log function to gorm's logger.Writer.
type gormWriterFunc func(string)

func (f gormWriterFunc) Printf(format string, args ...any) {
	f(sprintfForGorm(format, args...))
}

// gormLoggerConfig is the explicit replacement for gorm's logger.Default
// config. See this file's header for what each field fixes.
func gormLoggerConfig() gormlogger.Config {
	return gormlogger.Config{
		SlowThreshold:             time.Duration(common.GetEnvOrDefault("DB_SLOW_QUERY_MS", defaultSlowQueryMs)) * time.Millisecond,
		LogLevel:                  gormlogger.Warn,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      true,
		Colorful:                  false,
	}
}

// gormLogger is the logger newGormConfig attaches, and chooseDB is the one
// function that opens a pool (TestNewGormConfig_IsWhatChooseDBUses asserts
// chooseDB has no gorm.Config literal of its own).
type gormLogger struct {
	dbName string
	cfg    gormlogger.Config
	infoW  gormlogger.Writer
	errW   gormlogger.Writer
	info   gormlogger.Interface // gorm's logger over infoW, for Info/Warn
	errs   gormlogger.Interface // gorm's logger over errW, for Error
}

func newGormLogger(dbName string, infoW, errW gormlogger.Writer, cfg gormlogger.Config) *gormLogger {
	return &gormLogger{
		dbName: dbName,
		cfg:    cfg,
		infoW:  infoW,
		errW:   errW,
		info:   gormlogger.New(infoW, cfg),
		errs:   gormlogger.New(errW, cfg),
	}
}

func (l *gormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	next := *l
	next.cfg.LogLevel = level
	next.info = l.info.LogMode(level)
	next.errs = l.errs.LogMode(level)
	return &next
}

func (l *gormLogger) Info(ctx context.Context, msg string, data ...any) {
	l.info.Info(ctx, msg, data...)
}

func (l *gormLogger) Warn(ctx context.Context, msg string, data ...any) {
	l.info.Warn(ctx, msg, data...)
}

func (l *gormLogger) Error(ctx context.Context, msg string, data ...any) {
	l.errs.Error(ctx, msg, data...)
}

// Trace keeps the branch order of gorm's own logger.Trace: the error branch
// wins over the slow branch, so a query that is both slow and failing is
// logged once, as an error, and is not counted as slow.
func (l *gormLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.cfg.LogLevel <= gormlogger.Silent {
		return
	}
	elapsed := time.Since(begin)
	switch {
	case l.logsAsError(err):
		sql, rows := fc()
		l.errW.Printf("%s db error: %v [%.3fms] [rows:%s] %s",
			gormCallerSite(), err, msOf(elapsed), rowsLabel(rows), sql)
	case l.isSlow(elapsed):
		metrics.RecordDBSlowQuery(l.dbName)
		sql, rows := fc()
		l.infoW.Printf("%s SLOW SQL >= %v [%.3fms] [rows:%s] %s",
			gormCallerSite(), l.cfg.SlowThreshold, msOf(elapsed), rowsLabel(rows), sql)
	case l.cfg.LogLevel == gormlogger.Info:
		sql, rows := fc()
		l.infoW.Printf("%s [%.3fms] [rows:%s] %s",
			gormCallerSite(), msOf(elapsed), rowsLabel(rows), sql)
	}
}

// logsAsError reports whether gorm's Trace would take its error branch for
// this call, under this Config.
func (l *gormLogger) logsAsError(err error) bool {
	return err != nil && l.cfg.LogLevel >= gormlogger.Error &&
		(!errors.Is(err, gormlogger.ErrRecordNotFound) || !l.cfg.IgnoreRecordNotFoundError)
}

// isSlow reports whether gorm's Trace would take its SLOW SQL branch.
func (l *gormLogger) isSlow(elapsed time.Duration) bool {
	return l.cfg.LogLevel >= gormlogger.Warn && l.cfg.SlowThreshold != 0 && elapsed > l.cfg.SlowThreshold
}

// ParamsFilter is the hook gorm's callbacks look for before building the SQL
// they hand to Trace (gorm.io/gorm/callbacks.go, `db.Logger.(ParamsFilter)`).
// It is NOT part of logger.Interface, so a logger that does not re-implement
// it silently turns ParameterizedQueries back off.
func (l *gormLogger) ParamsFilter(_ context.Context, sql string, params ...any) (string, []any) {
	if l.cfg.ParameterizedQueries {
		return sql, nil
	}
	return sql, params
}

func msOf(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1e6 }

// rowsLabel renders gorm's rows-affected sentinel the way gorm does: -1 means
// "not applicable" and prints as a dash.
func rowsLabel(rows int64) string {
	if rows == -1 {
		return "-"
	}
	return strconv.FormatInt(rows, 10)
}

// gormCallerSite returns file:line for the code that issued the query — the
// first stack frame that is neither inside gorm nor inside this file. That is
// the rule gorm's own utils.FileWithLineNum applies, extended to skip this
// file so the logger does not report itself. A frame in a database driver
// (the Migrator paths go through one) is reported as the caller, which is
// what gorm's helper does too.
func gormCallerSite() string {
	pcs := make([]uintptr, 16)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if frame.File != "" &&
			!strings.Contains(frame.File, "gorm.io/") &&
			!strings.HasSuffix(frame.File, "gorm_logger.go") {
			return frame.File + ":" + strconv.Itoa(frame.Line)
		}
		if !more {
			return ""
		}
	}
}

// newGormConfig builds the gorm.Config chooseDB passes to gorm.Open.
func newGormConfig(dbName string) *gorm.Config {
	return &gorm.Config{
		PrepareStmt: true, // precompile SQL
		Logger: newGormLogger(dbName,
			gormWriterFunc(common.SysLog),
			gormWriterFunc(common.SysError),
			gormLoggerConfig()),
	}
}

// poolNameForEnv maps the DSN environment variable chooseDB was asked for to
// the pool name used on /metrics. Two are passed today — InitDB's "SQL_DSN"
// and InitLogDB's "LOG_SQL_DSN", the two chooseDB call sites in main.go;
// anything else is treated as the main pool.
func poolNameForEnv(envName string) string {
	if envName == "LOG_SQL_DSN" {
		return dbPoolNameLog
	}
	return dbPoolNameMain
}
