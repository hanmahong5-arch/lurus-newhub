package repo

// gorm_logger_test.go — cycle-12 L8. Two neighbouring properties of how this
// process opens a database, both previously left at gorm's defaults:
//
//   - the GORM logger. chooseDB passed a bare &gorm.Config{PrepareStmt:true},
//     so gorm fell back to logger.Default: ANSI colour codes into a stream the
//     deployment configures as JSON (LOG_FORMAT=json), record-not-found
//     printed as an error, and bound parameter values interpolated into the
//     logged SQL — which on this codebase means channel keys and token keys
//     in a log line.
//   - the connection pool. SQL_MAX_LIFETIME defaulted to 60 seconds with
//     PrepareStmt:true, i.e. the whole prepared-statement cache was thrown
//     away every minute, and ConnMaxIdleTime was never set at all.
//
// The pool tests live in this file rather than a third one because
// applyPoolSettings and newGormConfig are the two halves of the same "how a
// pool is opened" change; the lane owns this file and main.go.
//
// Note on timing: the slow-branch test drives gormLogger.Trace directly with
// a synthetic begin rather than trying to make a real SQLite query exceed a
// tiny threshold. Measured on this machine 2026-09-19, consecutive
// time.Now() readings around a sub-millisecond query frequently differ by 0,
// so "threshold = 1ns + run a real query" reported slow for some statements
// and not others in the same run. A wall-clock race is not what this oracle
// is about.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/glebarez/sqlite"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// captureWriter is a gorm logger.Writer that keeps what it was asked to
// print. It formats exactly the way the production writer does
// (sprintfForGorm) so assertions see the same text an operator would.
type captureWriter struct {
	mu    sync.Mutex
	lines []string
}

func (c *captureWriter) Printf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, sprintfForGorm(format, args...))
}

func (c *captureWriter) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

// openSQLiteWithLogger opens a throwaway in-memory database wired to lg. It
// deliberately does NOT touch the package-level DB/LOG_DB globals, so it can
// run alongside the setupSQLiteDB-based tests in this package.
func openSQLiteWithLogger(t *testing.T, lg gormlogger.Interface) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:gormloggertest"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{Logger: lg})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Option{}); err != nil {
		t.Fatalf("auto migrate options: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, derr := db.DB(); derr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// TestGormLoggerConfig_ExplicitFields pins the four fields the lane changed
// away from gorm's logger.Default, plus the log level that decides whether
// the slow-query branch runs at all.
func TestGormLoggerConfig_ExplicitFields(t *testing.T) {
	t.Setenv("DB_SLOW_QUERY_MS", "")

	cfg := gormLoggerConfig()
	if cfg.Colorful {
		t.Error("Colorful = true: ANSI escapes end up inside the JSON log stream the deployment configures (LOG_FORMAT=json)")
	}
	if !cfg.IgnoreRecordNotFoundError {
		t.Error("IgnoreRecordNotFoundError = false: a miss on First() is logged as an error, which is not one")
	}
	if !cfg.ParameterizedQueries {
		t.Error("ParameterizedQueries = false: bound values are interpolated into the logged SQL, including channel and token keys")
	}
	if want := 200 * time.Millisecond; cfg.SlowThreshold != want {
		t.Errorf("SlowThreshold = %v, want %v with DB_SLOW_QUERY_MS unset", cfg.SlowThreshold, want)
	}
	if cfg.LogLevel != gormlogger.Warn {
		t.Errorf("LogLevel = %v, want %v (the slow-query branch needs at least Warn)", cfg.LogLevel, gormlogger.Warn)
	}

	t.Setenv("DB_SLOW_QUERY_MS", "50")
	if got, want := gormLoggerConfig().SlowThreshold, 50*time.Millisecond; got != want {
		t.Errorf("SlowThreshold with DB_SLOW_QUERY_MS=50 = %v, want %v", got, want)
	}
}

// TestGormLogger_RecordNotFoundIsNotLoggedAsError drives a real miss through
// a real *gorm.DB, then a real failure, and checks that only the second one
// reaches the error sink.
func TestGormLogger_RecordNotFoundIsNotLoggedAsError(t *testing.T) {
	infoW, errW := &captureWriter{}, &captureWriter{}
	db := openSQLiteWithLogger(t, newGormLogger("newhub_test", infoW, errW, gormLoggerConfig()))

	var opt Option
	err := db.Where("key = ?", "no-such-option-key").First(&opt).Error
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("precondition: First() on an empty table returned %v, want ErrRecordNotFound", err)
	}
	if got := errW.text(); got != "" {
		t.Errorf("record-not-found reached the error sink:\n%s", got)
	}

	// A genuine failure must still reach it. Without this half, the assertion
	// above would also pass for a logger that never writes to errW at all.
	if derr := db.Exec("SELECT * FROM a_table_that_does_not_exist").Error; derr == nil {
		t.Fatal("precondition: querying a missing table returned no error")
	}
	if errW.text() == "" {
		t.Error("a real query error did not reach the error sink, so the empty-error-sink assertion above proves nothing")
	}
	if combined := infoW.text() + errW.text(); strings.Contains(combined, "\x1b[") {
		t.Errorf("logger emitted ANSI escapes:\n%q", combined)
	}
}

// TestGormLogger_SlowBranchCountsAndRoutes is the counter oracle: only the
// slow branch increments lurus_gateway_db_slow_query_total, it writes to the
// info sink, and an errored call takes the error branch instead — counted as
// nothing, written to the error sink.
func TestGormLogger_SlowBranchCountsAndRoutes(t *testing.T) {
	const pool = "newhub_slowbranch"
	cfg := gormLoggerConfig()
	infoW, errW := &captureWriter{}, &captureWriter{}
	lg := newGormLogger(pool, infoW, errW, cfg)
	fc := func() (string, int64) { return "SELECT * FROM `options` WHERE `key` = ?", 1 }
	count := func() float64 {
		return testutil.ToFloat64(metrics.DBSlowQueryTotal.WithLabelValues(pool))
	}

	under := count()
	lg.Trace(context.Background(), time.Now(), fc, nil)
	if got := count(); got != under {
		t.Errorf("a query inside the threshold was counted as slow: %f -> %f", under, got)
	}
	if infoW.text() != "" || errW.text() != "" {
		t.Errorf("a query inside the threshold was logged at all:\ninfo=%s\nerr=%s", infoW.text(), errW.text())
	}

	before := count()
	lg.Trace(context.Background(), time.Now().Add(-2*cfg.SlowThreshold), fc, nil)
	if got := count(); got-before != 1 {
		t.Errorf("DBSlowQueryTotal{db=%s} did not increment by 1 for a query over the threshold: %f -> %f", pool, before, got)
	}
	if !strings.Contains(infoW.text(), "SLOW SQL") {
		t.Errorf("the slow query was not logged to the info sink:\n%s", infoW.text())
	}
	if errW.text() != "" {
		t.Errorf("the slow query also reached the error sink:\n%s", errW.text())
	}

	// Slow AND failing: gorm's own branch order makes that one error line,
	// not a slow line, so it must not be counted as a slow query either.
	before = count()
	lg.Trace(context.Background(), time.Now().Add(-2*cfg.SlowThreshold), fc, errors.New("connection reset"))
	if got := count(); got != before {
		t.Errorf("a failing query was counted as slow: %f -> %f", before, got)
	}
	if !strings.Contains(errW.text(), "connection reset") {
		t.Errorf("the failing query did not reach the error sink:\n%s", errW.text())
	}
}

// TestGormLogger_BoundValuesStayOutOfTheLoggedSQL drives a real query through
// a real *gorm.DB and checks the logged statement carries the placeholder,
// not the value bound to it. It runs at Info level so every statement is
// logged — the property under test is ParamsFilter, not which branch fires.
func TestGormLogger_BoundValuesStayOutOfTheLoggedSQL(t *testing.T) {
	const secret = "sk-probe-bound-value-must-not-be-logged"

	cfg := gormLoggerConfig()
	cfg.LogLevel = gormlogger.Info
	infoW, errW := &captureWriter{}, &captureWriter{}
	db := openSQLiteWithLogger(t, newGormLogger("newhub_params", infoW, errW, cfg))

	if err := db.Create(&Option{Key: secret, Value: "v"}).Error; err != nil {
		t.Fatalf("seed option: %v", err)
	}
	var opt Option
	if err := db.Where("key = ?", secret).First(&opt).Error; err != nil {
		t.Fatalf("select option: %v", err)
	}

	combined := infoW.text() + "\n" + errW.text()
	if !strings.Contains(combined, "options") {
		t.Fatalf("no statement touching the options table was logged, so the assertions below prove nothing:\n%s", combined)
	}
	if strings.Contains(combined, secret) {
		t.Errorf("the value bound to the query appears in the log line — on this codebase that class of value includes channel and token keys:\n%s", combined)
	}
	if !strings.Contains(combined, "?") {
		t.Errorf("no placeholder survived into the logged SQL; the statement was rewritten rather than left parameterized:\n%s", combined)
	}
	if strings.Contains(combined, "\x1b[") {
		t.Errorf("logger emitted ANSI escapes:\n%q", combined)
	}
}

// TestGormLogger_TraceNamesTheCallerNotItself guards the reason Trace is
// implemented here instead of delegating to gorm's logger: gorm resolves the
// caller as the first frame outside its own source tree, so a wrapper that
// delegates makes every line name gorm_logger.go.
func TestGormLogger_TraceNamesTheCallerNotItself(t *testing.T) {
	cfg := gormLoggerConfig()
	cfg.LogLevel = gormlogger.Info
	infoW, errW := &captureWriter{}, &captureWriter{}
	db := openSQLiteWithLogger(t, newGormLogger("newhub_caller", infoW, errW, cfg))

	var opt Option
	_ = db.Where("key = ?", "anything").First(&opt).Error

	got := infoW.text()
	if !strings.Contains(got, "gorm_logger_test.go:") {
		t.Errorf("logged lines do not name the code that issued the query:\n%s", got)
	}
	if strings.Contains(got, "gorm_logger.go:") {
		t.Errorf("logged lines name the logger itself instead of the caller:\n%s", got)
	}
}

// TestNewGormConfig_IsWhatChooseDBUses checks the production constructor and
// that chooseDB actually goes through it. Without the second half the field
// assertions above would be a proxy oracle: they would keep passing while
// chooseDB carried on handing gorm a bare config.
func TestNewGormConfig_IsWhatChooseDBUses(t *testing.T) {
	cfg := newGormConfig(dbPoolNameMain)
	if !cfg.PrepareStmt {
		t.Error("PrepareStmt = false; the existing behaviour must survive the logger change")
	}
	if _, ok := cfg.Logger.(*gormLogger); !ok {
		t.Errorf("Logger is %T, want *gormLogger", cfg.Logger)
	}
	if got := poolNameForEnv("LOG_SQL_DSN"); got != dbPoolNameLog {
		t.Errorf("poolNameForEnv(LOG_SQL_DSN) = %q, want %q", got, dbPoolNameLog)
	}
	if got := poolNameForEnv("SQL_DSN"); got != dbPoolNameMain {
		t.Errorf("poolNameForEnv(SQL_DSN) = %q, want %q", got, dbPoolNameMain)
	}

	src := readRepoSource(t, "main.go")
	chooseDBBody := funcBodyOrFail(t, src, "func chooseDB(")
	if !strings.Contains(chooseDBBody, "newGormConfig(") {
		t.Error("chooseDB does not build its gorm.Config through newGormConfig — the logger settings above never reach a real connection")
	}
	if strings.Contains(chooseDBBody, "&gorm.Config{") {
		t.Error("chooseDB still has an inline &gorm.Config{...} literal; there must be one place that decides how a pool is opened")
	}
}

// lockedBuffer is the sink the test below installs as the process-wide slog
// writer. It is mutex-guarded because that writer is global: a background
// goroutine left running by another test in this package can log into it
// while this test is reading, and an unguarded bytes.Buffer would be a data
// race the -race job reports as this test's fault.
type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// slogLevelOfRecordContaining finds the single JSON log record carrying
// marker and returns its "level" field. It fatals on zero or several matches
// so a marker that stopped being unique cannot silently weaken the assertion.
func slogLevelOfRecordContaining(t *testing.T, body, marker string) string {
	t.Helper()
	var levels []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, marker) {
			continue
		}
		var rec struct {
			Level string `json:"level"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("the log record carrying %q is not JSON (%v):\n%s", marker, err, line)
		}
		levels = append(levels, rec.Level)
	}
	if len(levels) != 1 {
		t.Fatalf("want exactly one log record carrying %q, found %d — the whole capture was:\n%s",
			marker, len(levels), body)
	}
	return levels[0]
}

// TestNewGormConfig_ErrorsAndSlowQueriesLandOnTheirOwnSinks is the oracle for
// the one property the hand-written Trace exists for: WHICH sink each branch
// writes to. Everything else in this file constructs its own newGormLogger
// with capture writers, so the production wiring — which of
// common.SysLog/common.SysError newGormConfig hands to which field — was
// unguarded: the two could be swapped and the package stayed green, shipping
// database errors at slog level INFO and slow queries at ERROR.
//
// This drives the logger newGormConfig actually builds and reads the level
// off the real log record, rather than asserting on struct fields: in
// production LOG_FORMAT=json (deploy/k8s/r6-stage/deployment.yaml), so
// common.SysLog and common.SysError differ only by the "level" field of the
// JSON record they emit, and that field is exactly what an operator filters
// on.
//
// Mutation target: swap the two gormWriterFunc arguments in newGormConfig —
// both assertions go red.
func TestNewGormConfig_ErrorsAndSlowQueriesLandOnTheirOwnSinks(t *testing.T) {
	t.Setenv("DB_SLOW_QUERY_MS", "")

	captured := &lockedBuffer{}
	common.InitSlog(&common.SlogConfig{JSONFormat: true, Writer: captured, ErrWriter: captured})
	t.Cleanup(func() { common.InitSlog(nil) })

	built := newGormConfig(dbPoolNameMain)
	lg, ok := built.Logger.(*gormLogger)
	if !ok {
		t.Fatalf("newGormConfig's Logger is %T, want *gormLogger", built.Logger)
	}
	if lg.cfg.SlowThreshold <= 0 {
		t.Fatalf("SlowThreshold = %v; the slow branch below would never run and this test would "+
			"prove nothing", lg.cfg.SlowThreshold)
	}

	const failingMarker = "gormsinkprobe-failing-statement"
	const slowMarker = "gormsinkprobe-slow-statement"

	lg.Trace(context.Background(), time.Now(),
		func() (string, int64) { return "SELECT '" + failingMarker + "'", 0 },
		errors.New("connection reset by peer"))
	lg.Trace(context.Background(), time.Now().Add(-2*lg.cfg.SlowThreshold),
		func() (string, int64) { return "SELECT '" + slowMarker + "'", 1 },
		nil)

	body := captured.String()
	if got := slogLevelOfRecordContaining(t, body, failingMarker); got != "ERROR" {
		t.Errorf("a failed database statement was logged at level %s, want ERROR — newGormConfig has "+
			"the error branch wired to the info sink, so database errors are invisible to any "+
			"level-based filter", got)
	}
	if got := slogLevelOfRecordContaining(t, body, slowMarker); got != "INFO" {
		t.Errorf("a slow database statement was logged at level %s, want INFO — newGormConfig has "+
			"the slow branch wired to the error sink, so every slow query reads as a failure", got)
	}
}

// TestSQLPoolDefaults pins the pool configuration. The values are asserted on
// currentPoolSettings (database/sql exposes no getter for ConnMaxLifetime or
// ConnMaxIdleTime), with applyPoolSettings checked against the one field
// sql.DBStats does expose, plus a structural check that both InitDB and
// InitLogDB go through it.
func TestSQLPoolDefaults(t *testing.T) {
	for _, k := range []string{"SQL_MAX_IDLE_CONNS", "SQL_MAX_OPEN_CONNS", "SQL_MAX_LIFETIME", "SQL_MAX_IDLE_TIME"} {
		t.Setenv(k, "")
	}

	s := currentPoolSettings()
	if want := 30 * time.Minute; s.MaxLifetime < want {
		t.Errorf("MaxLifetime = %v, want at least %v: with PrepareStmt:true a short lifetime throws the whole prepared-statement cache away on every recycle", s.MaxLifetime, want)
	}
	if s.MaxIdleTime <= 0 {
		t.Errorf("MaxIdleTime = %v; idle connections are never retired, so a burst leaves the pool holding sockets against the database for the rest of the lifetime window", s.MaxIdleTime)
	}
	if s.MaxIdleConns <= 0 || s.MaxOpenConns <= 0 {
		t.Errorf("MaxIdleConns=%d MaxOpenConns=%d; both must stay positive", s.MaxIdleConns, s.MaxOpenConns)
	}

	t.Setenv("SQL_MAX_LIFETIME", "90")
	t.Setenv("SQL_MAX_IDLE_TIME", "45")
	t.Setenv("SQL_MAX_OPEN_CONNS", "7")
	over := currentPoolSettings()
	if over.MaxLifetime != 90*time.Second || over.MaxIdleTime != 45*time.Second || over.MaxOpenConns != 7 {
		t.Errorf("env override not honoured: %+v", over)
	}

	db := openSQLiteWithLogger(t, gormlogger.Discard)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB(): %v", err)
	}
	applied := applyPoolSettings(sqlDB)
	if got := sqlDB.Stats().MaxOpenConnections; got != applied.MaxOpenConns {
		t.Errorf("applyPoolSettings did not reach the pool: MaxOpenConnections=%d, settings said %d", got, applied.MaxOpenConns)
	}

	src := readRepoSource(t, "main.go")
	for _, fn := range []string{"func InitDB(", "func InitLogDB("} {
		if !strings.Contains(funcBodyOrFail(t, src, fn), "applyPoolSettings(sqlDB)") {
			t.Errorf("%s does not call applyPoolSettings(sqlDB)", strings.TrimPrefix(fn, "func "))
		}
	}
	// The setter count is taken over EVERY non-test file in this package, not
	// over main.go alone. A second production call site anywhere in package
	// repo splits pool configuration in exactly the way applyPoolSettings
	// exists to prevent, and a main.go-only scan cannot see it — which is what
	// this check used to be.
	//
	// Scope limit it still cannot close: a different package holding its own
	// *sql.DB can call the setters and nothing here notices. Every such hit in
	// the tree today is a _test.go file pinning SetMaxOpenConns(1) to
	// serialise a shared-cache SQLite handle, which is deliberate.
	sources := packageProductionSources(t)
	if len(sources) < 2 || sources["main.go"] == "" {
		t.Fatalf("scanned %d non-test files and main.go %s among them — the setter scan below is "+
			"measuring nothing", len(sources), map[bool]string{true: "is", false: "is NOT"}[sources["main.go"] != ""])
	}
	for _, setter := range []string{"SetMaxIdleConns(", "SetMaxOpenConns(", "SetConnMaxLifetime(", "SetConnMaxIdleTime("} {
		total := 0
		var where []string
		for name, body := range sources {
			if n := strings.Count(body, setter); n > 0 {
				total += n
				where = append(where, fmt.Sprintf("%s x%d", name, n))
			}
		}
		if total != 1 {
			sort.Strings(where)
			t.Errorf("package repo calls %s %d times (%s), want exactly 1 — the one inside "+
				"applyPoolSettings in main.go. A second call site is how the two pools drift apart.",
				setter, total, strings.Join(where, ", "))
		}
	}
}

// packageProductionSources returns the body of every non-test .go file in
// this package, keyed by file name. The working directory of a Go test is its
// own package directory, which is what makes the bare ReadDir(".") correct
// here (readRepoSource below relies on the same fact).
func packageProductionSources(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, rerr := os.ReadFile(name)
		if rerr != nil {
			t.Fatalf("read %s: %v", name, rerr)
		}
		out[name] = string(body)
	}
	return out
}

// readRepoSource reads one of this package's own source files.
func readRepoSource(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

// funcBodyOrFail returns the source text from the given func header up to the
// next line that is a lone "}" at column 0 — gofmt guarantees that is the
// function's closing brace and that nothing inside the body looks like it.
func funcBodyOrFail(t *testing.T, src, header string) string {
	t.Helper()
	idx := strings.Index(src, header)
	if idx < 0 {
		t.Fatalf("source does not contain %q — this structural check is measuring nothing", header)
	}
	rest := src[idx:]
	if end := strings.Index(rest, "\n}\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}
