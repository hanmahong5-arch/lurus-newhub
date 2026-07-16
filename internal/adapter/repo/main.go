package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/migration"
	"github.com/LurusTech/lurus-hub/migrations"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// migrationBaselineThrough is the highest migrations/*.sql version that the
// embedded Runner records as applied WITHOUT executing: 001–004 are MySQL
// dialect (cannot run on PostgreSQL) and 005–020 were applied to STAGE by
// hand before the Runner existed; on a fresh database their schema comes
// from AutoMigrate. Only versions above this ever execute — they must be
// PostgreSQL-only and idempotent (see internal/pkg/migration package doc).
const migrationBaselineThrough = "020_create_privacy_erasure_requests"

var commonGroupCol string
var commonKeyCol string
var commonTrueVal string
var commonFalseVal string

var logKeyCol string
var logGroupCol string

// InitCol initializes DB-dialect-specific column name quoting.
// Called automatically by chooseDB; exported for test setup with direct DB injection.
func InitCol() {
	initCol()
}

func initCol() {
	// Runtime is PostgreSQL-only (2026-06). The hermetic glebarez SQLite
	// unit-test tier also accepts double-quoted identifiers and true/false
	// boolean literals, so these constants are unconditional.
	// UPSTREAM-MERGE NOTE: new-api branches on MySQL/SQLite dialect here;
	// newhub is PG-only, keep the PG constants on conflicts.
	commonGroupCol = `"group"`
	commonKeyCol = `"key"`
	commonTrueVal = "true"
	commonFalseVal = "false"
	logGroupCol = commonGroupCol
	logKeyCol = commonKeyCol
}

var DB *gorm.DB

var LOG_DB *gorm.DB

func createRootAccountIfNeed() error {
	var user User
	if err := DB.First(&user).Error; err != nil {
		common.SysLog("no user exists; use /api/setup to create the initial admin account")
	}
	return nil
}

func CheckSetup() {
	setup := GetSetup()
	if setup == nil {
		// No setup record exists, check if we have a root user
		if RootUserExists() {
			common.SysLog("system is not initialized, but root user exists")
			// Create setup record
			newSetup := Setup{
				Version:       common.Version,
				InitializedAt: time.Now().Unix(),
			}
			err := DB.Create(&newSetup).Error
			if err != nil {
				common.SysLog("failed to create setup record: " + err.Error())
			}
			constant.SetSetup(true)
		} else {
			common.SysLog("system is not initialized and no root user exists")
			constant.SetSetup(false)
		}
	} else {
		// Setup record exists, system is initialized
		common.SysLog("system is already initialized at: " + time.Unix(setup.InitializedAt, 0).String())
		constant.SetSetup(true)
	}
}

// chooseDB opens the PostgreSQL database named by envName.
// UPSTREAM-MERGE NOTE: new-api supports MySQL/SQLite here; newhub is PG-only
// (2026-06), keep the PG arm on conflicts.
func chooseDB(envName string) (*gorm.DB, error) {
	defer initCol()
	dsn := withStatementTimeout(os.Getenv(envName))
	common.SysLog("using PostgreSQL as database")
	common.UsingPostgreSQL = true

	// Bounded boot connect-retry (A2). gorm.Open with the pgx driver connects
	// LAZILY, so it succeeds even when PG is unreachable — the failure only
	// surfaces on first query. We therefore ping inside each attempt (load-
	// bearing) so retry actually observes "PG not ready" and a DB pod that is
	// not yet Ready when this pod boots is retried instead of crash-looping the
	// process. RetryConnect never FatalLogs; InitDB/InitLogDB keep the terminal
	// FatalLog after the budget is exhausted. The all-retryable predicate makes
	// a misconfigured DSN merely delay that FatalLog by the bounded budget
	// (validateSQLDSN already rejected bad schemes upstream).
	var db *gorm.DB
	err := common.RetryConnect("postgres "+envName, common.RetryConfig{
		MaxAttempts: common.DBConnectRetries,
		BaseDelay:   common.DBConnectRetryBaseDelay,
		MaxDelay:    common.DBConnectRetryMaxDelay,
	}, func(error) bool { return true }, func() error {
		opened, oerr := gorm.Open(postgres.New(postgres.Config{
			DSN:                  dsn,
			PreferSimpleProtocol: true, // disables implicit prepared statement usage
		}), &gorm.Config{
			PrepareStmt: true, // precompile SQL
		})
		if oerr != nil {
			return oerr
		}
		sqlDB, derr := opened.DB()
		if derr != nil {
			return derr
		}
		ctx, cancel := context.WithTimeout(context.Background(), common.DBConnectPingTimeout)
		defer cancel()
		if perr := sqlDB.PingContext(ctx); perr != nil {
			_ = sqlDB.Close() // don't leak the pool on a failed attempt
			return perr
		}
		db = opened
		return nil
	})
	if err != nil {
		return nil, err
	}
	return db, nil
}

// withStatementTimeout injects a server-side statement_timeout (ms) as a pgx
// connection runtime parameter so EVERY pooled connection caps query duration at
// the driver level (P1-1). This is the cheapest, broadest DB-hang guard — far
// better than threading a context deadline through each repo call: a wedged
// query (lock wait, runaway plan) is killed by Postgres instead of pinning a
// goroutine + connection until the pool drains. Default 8s sits well above
// normal cluster-local latency yet below the relay/readiness timeouts, so a
// genuinely stuck query surfaces as a fast error that the breaker/readiness path
// can act on. SQL_STATEMENT_TIMEOUT_MS=0 disables it. A DSN that already sets
// statement_timeout is left untouched (operator override wins).
func withStatementTimeout(dsn string) string {
	timeoutMs := common.GetEnvOrDefault("SQL_STATEMENT_TIMEOUT_MS", 8000)
	if timeoutMs <= 0 || dsn == "" {
		return dsn
	}
	u, err := url.Parse(dsn)
	if err != nil {
		// Not a URL-form DSN we can safely edit — leave it as-is rather than risk
		// corrupting a key=value DSN. PG-only boot gate already validated scheme.
		return dsn
	}
	q := u.Query()
	if q.Get("statement_timeout") != "" {
		return dsn // operator already set it; don't override
	}
	q.Set("statement_timeout", strconv.Itoa(timeoutMs))
	u.RawQuery = q.Encode()
	return u.String()
}

// validateSQLDSN is the PG-only boot gate: newhub refuses to start on anything
// but a PostgreSQL DSN. The historical SQLite dev fallback (empty DSN /
// "local") and MySQL support were removed 2026-06 — production has been pure
// PostgreSQL since launch, and a silent SQLite fallback on a misconfigured pod
// passes health checks while every write fails on the read-only root
// filesystem. The hermetic SQLite unit-test tier injects its DB directly and
// never goes through this gate.
func validateSQLDSN(dsn string) error {
	if dsn == "" {
		return errors.New("SQL_DSN is required (PostgreSQL-only since 2026-06); set a postgres:// or postgresql:// DSN")
	}
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		scheme, _, _ := strings.Cut(dsn, "://")
		return fmt.Errorf("unsupported SQL DSN scheme %q: newhub is PostgreSQL-only since 2026-06 (MySQL and the SQLite dev fallback were removed); set a postgres:// or postgresql:// DSN", scheme)
	}
	return nil
}

func InitDB() (err error) {
	if err := validateSQLDSN(os.Getenv("SQL_DSN")); err != nil {
		common.FatalLog("SQL_DSN: " + err.Error())
	}
	db, err := chooseDB("SQL_DSN")
	if err == nil {
		if common.DebugEnabled {
			db = db.Debug()
		}
		DB = db
		sqlDB, err := DB.DB()
		if err != nil {
			return err
		}
		sqlDB.SetMaxIdleConns(common.GetEnvOrDefault("SQL_MAX_IDLE_CONNS", 100))
		sqlDB.SetMaxOpenConns(common.GetEnvOrDefault("SQL_MAX_OPEN_CONNS", 1000))
		sqlDB.SetConnMaxLifetime(time.Second * time.Duration(common.GetEnvOrDefault("SQL_MAX_LIFETIME", 60)))

		// Expose main-pool saturation (wait_count/wait_duration) on /metrics so
		// pool exhaustion is alertable instead of surfacing only as latency.
		metrics.RegisterDBStats("newhub", sqlDB)

		if !common.IsMasterNode {
			return nil
		}
		// HA boot: multiple master-capable replicas contend for the long-lived
		// leadership lease here, but — unlike the original design — the lease
		// no longer gates boot migrations. The surviving leader holds the lease
		// for its whole lifetime, so lease-gated migrations were silently
		// skipped on every multi-replica rolling update (2026-07-15 incident:
		// prod schema stuck at 022 while shipped code expected 026). Boot
		// migrations now run on EVERY master-capable replica, serialized by a
		// dedicated pg_advisory_lock (AutoMigrate) plus the runner's own lock;
		// both phases are idempotent no-ops when nothing is pending.
		if e := DB.AutoMigrate(&entity.LeaderElection{}); e != nil {
			// A concurrent cold-start replica may be creating the same table.
			// Tolerate the race only if the table actually exists afterward —
			// robust across SQLite/PostgreSQL vs matching driver error strings.
			if !DB.Migrator().HasTable(&entity.LeaderElection{}) {
				return fmt.Errorf("bootstrap leader_elections table: %w", e)
			}
		}
		gotLease, lerr := TryAcquireOrRenew(entity.LeaderElectionName, common.NodeHolderID(), entity.LeaderBootLeaseTTLSeconds, common.GetTimestamp())
		if lerr != nil {
			return fmt.Errorf("acquire boot migration lease: %w", lerr)
		}
		if gotLease {
			// We hold leadership from boot. Reflect it immediately so leader-gated
			// startup catch-up runs (reaper / aggregator / audit cleanup) fire on
			// this node now, instead of being skipped until the LeaderManager's
			// first asynchronous renew flips the flag.
			common.SetLeader(true)
		} else {
			common.SysLog("boot lease held by another replica — leadership deferred; boot migrations still run under advisory lock")
		}
		return runBootMigrations(sqlDB)
	} else {
		common.FatalLog(err)
	}
	return err
}

// bootAutoMigrateLockID serializes GORM AutoMigrate across concurrently
// booting replicas: the ASCII bytes of "LurusHbA" packed big-endian —
// deliberately distinct from the SQL runner's AdvisoryLockID so the two
// phases cannot self-deadlock across pooled connections.
const bootAutoMigrateLockID int64 = 0x4C75727573486241

// runBootMigrations applies GORM AutoMigrate and the embedded SQL runner on
// every master-capable boot. AutoMigrate is serialized with a session-scoped
// advisory lock (acquired and released on the SAME connection); the runner
// serializes itself. Running both on every replica — not just the lease
// winner — is what guarantees a rolling update actually applies new DDL.
func runBootMigrations(sqlDB *sql.DB) error {
	common.SysLog("database migration started")
	if err := withPGAdvisoryLock(context.Background(), sqlDB, bootAutoMigrateLockID, migrateDB); err != nil {
		return err
	}
	if !migrationsAutoRunEnabled() {
		common.SysLog("embedded SQL migrations skipped: MIGRATIONS_AUTO_RUN is disabled")
		return nil
	}
	runner := &migration.Runner{
		DB:              sqlDB,
		FS:              migrations.FS,
		BaselineThrough: migrationBaselineThrough,
	}
	if err := runner.Run(context.Background()); err != nil {
		return fmt.Errorf("run embedded SQL migrations: %w", err)
	}
	return nil
}

// withPGAdvisoryLock runs fn while holding pg_advisory_lock(key) on one
// dedicated connection, so the unlock is guaranteed to hit the session that
// owns the lock (a pool-level Exec could unlock on a different session and
// leave the lock stranded until the owning connection is recycled).
func withPGAdvisoryLock(ctx context.Context, sqlDB *sql.DB, key int64, fn func() error) error {
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("advisory lock conn: %w", err)
	}
	defer func() { _ = conn.Close() }()
	// The lock wait must outlive the DSN-injected statement_timeout (P1-1):
	// replicas booting together wait here for as long as the holder's
	// AutoMigrate takes, and Postgres cancelling that wait fatals the pod
	// (STAGE crash-loop 2026-07-15). SET LOCAL lifts the caps for the wait
	// only — the session-scoped lock survives commit, and the connection
	// returns to the pool with its DSN defaults intact.
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("advisory lock tx: %w", err)
	}
	for _, q := range []string{`SET LOCAL statement_timeout = 0`, `SET LOCAL lock_timeout = 0`} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("acquire advisory lock %d: %w", key, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit advisory lock acquire %d: %w", key, err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, key)
	}()
	return fn()
}

// migrationsAutoRunEnabled gates the embedded SQL migration runner.
// Unset defaults to ON — AutoMigrate has always run unconditionally on the
// lease winner, so default-on matches the existing boot posture.
// MIGRATIONS_AUTO_RUN=false|0|no|off is the escape hatch for hand-applied
// migrations (e.g. an ALTER the app's PG role does not own).
func migrationsAutoRunEnabled() bool {
	switch strings.ToLower(os.Getenv("MIGRATIONS_AUTO_RUN")) {
	case "false", "0", "no", "off":
		return false
	}
	return true
}

func InitLogDB() (err error) {
	if os.Getenv("LOG_SQL_DSN") == "" {
		LOG_DB = DB
		return
	}
	if err := validateSQLDSN(os.Getenv("LOG_SQL_DSN")); err != nil {
		common.FatalLog("LOG_SQL_DSN: " + err.Error())
	}
	db, err := chooseDB("LOG_SQL_DSN")
	if err == nil {
		if common.DebugEnabled {
			db = db.Debug()
		}
		LOG_DB = db
		sqlDB, err := LOG_DB.DB()
		if err != nil {
			return err
		}
		sqlDB.SetMaxIdleConns(common.GetEnvOrDefault("SQL_MAX_IDLE_CONNS", 100))
		sqlDB.SetMaxOpenConns(common.GetEnvOrDefault("SQL_MAX_OPEN_CONNS", 1000))
		sqlDB.SetConnMaxLifetime(time.Second * time.Duration(common.GetEnvOrDefault("SQL_MAX_LIFETIME", 60)))

		// Log-DB pool telemetry under a distinct db_name so it is separable from
		// the main pool on /metrics.
		metrics.RegisterDBStats("newhub_log", sqlDB)

		if !common.IsMasterNode {
			return nil
		}
		common.SysLog("database migration started")
		err = migrateLOGDB()
		return err
	} else {
		common.FatalLog(err)
	}
	return err
}

func migrateDB() error {
	err := DB.AutoMigrate(
		&Channel{},
		&Token{},
		&User{},
		&Option{},
		&Redemption{},
		&Ability{},
		&Log{},
		&Midjourney{},
		&QuotaData{},
		&Task{},
		&Model{},
		&Vendor{},
		&PrefillGroup{},
		&Setup{},
		&InternalApiKey{},
		// Multi-tenant models
		&Tenant{},
		&UserIdentityMapping{},
		&TenantConfig{},
		// Release/download management
		&entity.Release{},
		&entity.ReleaseArtifact{},
		&entity.DownloadLog{},
		// Switch config presets
		&SwitchConfigPresetRow{},
		// Currency exchange ledger
		&entity.CurrencyExchange{},
		// Governance audit trail
		&entity.AuditEvent{},
		// OpenRouter free-model sync
		&entity.OpenRouterSyncJob{},
		&entity.ModelUsageStat{},
		// Tenant credit pools (Reseller mode, ADR 2026-05-18)
		&entity.TenantCreditPool{},
		&entity.TenantCreditPoolDraw{},
		// Credit-pool fund events (BillingOutbox idempotency, migration 019) —
		// was missing here, so a fresh PG never created the table; migration 021
		// also creates it IF NOT EXISTS for DR restores that skip AutoMigrate.
		&entity.CreditPoolFundEvent{},
		// Distributor batch redemption issuance ledger (idempotency, migration 027)
		&entity.ProvisionedRedemptionBatch{},
		// Playground named presets (Wave 3 Phase 1)
		&PlaygroundPreset{},
		// HA leader-election lease (H1.3)
		&entity.LeaderElection{},
		// PIPL §47 erasure intent / progress / evidence (migration 020)
		&entity.PrivacyErasureRequest{},
		// Per-model rate limits (migration 026) — model dimension of the
		// business rate limiter (middleware.BusinessModelRateLimit)
		&entity.ModelRateLimit{},
		// v2 checkout order ownership (migration 028) — order_no -> account_id
		// binding that gates GET .../checkout/:order_no/status against IDOR
		&entity.BillingCheckoutOrder{},
	)
	if err != nil {
		return err
	}

	// Initialize tenant context manager after DB migration
	err = InitTenantContextManager(DB)
	if err != nil {
		common.SysLog("Failed to initialize tenant context manager: " + err.Error())
		return err
	}
	common.SysLog("Tenant context manager initialized successfully")

	return nil
}

func migrateDBFast() error {

	var wg sync.WaitGroup

	migrations := []struct {
		model interface{}
		name  string
	}{
		{&Channel{}, "Channel"},
		{&Token{}, "Token"},
		{&User{}, "User"},
		{&Option{}, "Option"},
		{&Redemption{}, "Redemption"},
		{&Ability{}, "Ability"},
		{&Log{}, "Log"},
		{&Midjourney{}, "Midjourney"},
		{&QuotaData{}, "QuotaData"},
		{&Task{}, "Task"},
		{&Model{}, "Model"},
		{&Vendor{}, "Vendor"},
		{&PrefillGroup{}, "PrefillGroup"},
		{&Setup{}, "Setup"},
		{&InternalApiKey{}, "InternalApiKey"},
		// Release/download management
		{&entity.Release{}, "Release"},
		{&entity.ReleaseArtifact{}, "ReleaseArtifact"},
		{&entity.DownloadLog{}, "DownloadLog"},
		// Governance audit trail
		{&entity.AuditEvent{}, "AuditEvent"},
		// OpenRouter free-model sync
		{&entity.OpenRouterSyncJob{}, "OpenRouterSyncJob"},
		{&entity.ModelUsageStat{}, "ModelUsageStat"},
		// Tenant credit pools (Reseller mode, ADR 2026-05-18)
		{&entity.TenantCreditPool{}, "TenantCreditPool"},
		{&entity.TenantCreditPoolDraw{}, "TenantCreditPoolDraw"},
	}
	// 动态计算migration数量，确保errChan缓冲区足够大
	errChan := make(chan error, len(migrations))

	for _, m := range migrations {
		wg.Add(1)
		go func(model interface{}, name string) {
			defer wg.Done()
			if err := DB.AutoMigrate(model); err != nil {
				errChan <- fmt.Errorf("failed to migrate %s: %v", name, err)
			}
		}(m.model, m.name)
	}

	// Wait for all migrations to complete
	wg.Wait()
	close(errChan)

	// Check for any errors
	for err := range errChan {
		if err != nil {
			return err
		}
	}
	common.SysLog("database migrated")
	return nil
}

func migrateLOGDB() error {
	var err error
	if err = LOG_DB.AutoMigrate(&Log{}); err != nil {
		return err
	}
	return nil
}

func closeDB(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	err = sqlDB.Close()
	return err
}

func CloseDB() error {
	if LOG_DB != DB {
		err := closeDB(LOG_DB)
		if err != nil {
			return err
		}
	}
	return closeDB(DB)
}

var (
	lastPingTime time.Time
	pingMutex    sync.Mutex
)

func PingDB() error {
	pingMutex.Lock()
	defer pingMutex.Unlock()

	if time.Since(lastPingTime) < time.Second*10 {
		return nil
	}

	sqlDB, err := DB.DB()
	if err != nil {
		log.Printf("Error getting sql.DB from GORM: %v", err)
		return err
	}

	err = sqlDB.Ping()
	if err != nil {
		log.Printf("Error pinging DB: %v", err)
		return err
	}

	lastPingTime = time.Now()
	common.SysLog("Database pinged successfully")
	return nil
}
