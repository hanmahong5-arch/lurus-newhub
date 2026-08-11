package common

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// covInitSnapshot captures every global InitEnv/initConstantEnv can mutate so
// tests can restore boot-time state exactly, keeping this test hermetic with
// respect to every other test in the package (many of which read these globals).
type covInitSnapshot struct {
	version                                string
	sessionSecret                          string
	cryptoSecret                           string
	logDir                                 string
	debugEnabled                           bool
	memoryCacheEnabled                     bool
	isMasterNode                           bool
	requestInterval                        time.Duration
	syncFrequency                          int
	batchUpdateInterval                    int
	relayTimeout                           int
	relayMaxIdleConns                      int
	relayMaxIdleConnsPerHost               int
	relayResponseHeaderTimeout             time.Duration
	relayDialTimeout                       time.Duration
	healthDBPingTimeout                    time.Duration
	dbConnectRetries                       int
	dbConnectRetryBaseDelay                time.Duration
	dbConnectRetryMaxDelay                 time.Duration
	dbConnectPingTimeout                   time.Duration
	costSpikeProtectionEnabled             bool
	costSpikeHardLimitPer5Min              int
	geminiSafetySetting                    string
	cohereSafetySetting                    string
	globalApiRateLimitEnable               bool
	globalApiRateLimitNum                  int
	globalApiRateLimitDuration             int64
	globalWebRateLimitEnable               bool
	globalWebRateLimitNum                  int
	globalWebRateLimitDuration             int64
	criticalRateLimitEnable                bool
	criticalRateLimitNum                   int
	criticalRateLimitDuration              int64
	internalApiRateLimitEnable             bool
	internalApiReadRateLimitNum            int
	internalApiReadRateLimitDuration       int64
	internalApiWriteRateLimitNum           int
	internalApiWriteRateLimitDuration      int64
	internalApiProvisionRateLimitNum       int
	internalApiProvisionRateLimitDuration  int64
	billingDegradedSpendCapLB              float64
	billingDegradedWindowSec               int
	constStreamingTimeout                  int
	constDifyDebug                         bool
	constMaxFileDownloadMB                 int
	constStreamScannerMaxBufferMB          int
	constMaxRequestBodyMB                  int
	constForceStreamOption                 bool
	constCountToken                        bool
	constGetMediaToken                     bool
	constGetMediaTokenNotStream            bool
	constUpdateTask                        bool
	constAzureDefaultAPIVersion            string
	constGeminiVisionMaxImageNum           int
	constNotifyLimitCount                  int
	constNotificationLimitDurationMinute   int
	constGenerateDefaultToken              bool
	constErrorLogEnabled                   bool
	constTaskQueryLimit                    int
	constTaskPricePatches                  []string
}

func covSnapshotInitState() covInitSnapshot {
	return covInitSnapshot{
		version:                               Version,
		sessionSecret:                         SessionSecret,
		cryptoSecret:                          CryptoSecret,
		logDir:                                *LogDir,
		debugEnabled:                          DebugEnabled,
		memoryCacheEnabled:                    MemoryCacheEnabled,
		isMasterNode:                          IsMasterNode,
		requestInterval:                       RequestInterval,
		syncFrequency:                         SyncFrequency,
		batchUpdateInterval:                   BatchUpdateInterval,
		relayTimeout:                          RelayTimeout,
		relayMaxIdleConns:                     RelayMaxIdleConns,
		relayMaxIdleConnsPerHost:              RelayMaxIdleConnsPerHost,
		relayResponseHeaderTimeout:            RelayResponseHeaderTimeout,
		relayDialTimeout:                      RelayDialTimeout,
		healthDBPingTimeout:                   HealthDBPingTimeout,
		dbConnectRetries:                      DBConnectRetries,
		dbConnectRetryBaseDelay:               DBConnectRetryBaseDelay,
		dbConnectRetryMaxDelay:                DBConnectRetryMaxDelay,
		dbConnectPingTimeout:                  DBConnectPingTimeout,
		costSpikeProtectionEnabled:            CostSpikeProtectionEnabled,
		costSpikeHardLimitPer5Min:             CostSpikeHardLimitPer5Min,
		geminiSafetySetting:                   GeminiSafetySetting,
		cohereSafetySetting:                   CohereSafetySetting,
		globalApiRateLimitEnable:              GlobalApiRateLimitEnable,
		globalApiRateLimitNum:                 GlobalApiRateLimitNum,
		globalApiRateLimitDuration:            GlobalApiRateLimitDuration,
		globalWebRateLimitEnable:              GlobalWebRateLimitEnable,
		globalWebRateLimitNum:                 GlobalWebRateLimitNum,
		globalWebRateLimitDuration:            GlobalWebRateLimitDuration,
		criticalRateLimitEnable:               CriticalRateLimitEnable,
		criticalRateLimitNum:                  CriticalRateLimitNum,
		criticalRateLimitDuration:             CriticalRateLimitDuration,
		internalApiRateLimitEnable:            InternalApiRateLimitEnable,
		internalApiReadRateLimitNum:           InternalApiReadRateLimitNum,
		internalApiReadRateLimitDuration:      InternalApiReadRateLimitDuration,
		internalApiWriteRateLimitNum:          InternalApiWriteRateLimitNum,
		internalApiWriteRateLimitDuration:     InternalApiWriteRateLimitDuration,
		internalApiProvisionRateLimitNum:      InternalApiProvisionRateLimitNum,
		internalApiProvisionRateLimitDuration: InternalApiProvisionRateLimitDuration,
		billingDegradedSpendCapLB:             BillingDegradedSpendCapLB,
		billingDegradedWindowSec:              BillingDegradedWindowSec,
		constStreamingTimeout:                 constant.StreamingTimeout,
		constDifyDebug:                        constant.DifyDebug,
		constMaxFileDownloadMB:                constant.MaxFileDownloadMB,
		constStreamScannerMaxBufferMB:         constant.StreamScannerMaxBufferMB,
		constMaxRequestBodyMB:                 constant.MaxRequestBodyMB,
		constForceStreamOption:                constant.ForceStreamOption,
		constCountToken:                       constant.CountToken,
		constGetMediaToken:                    constant.GetMediaToken,
		constGetMediaTokenNotStream:           constant.GetMediaTokenNotStream,
		constUpdateTask:                       constant.UpdateTask,
		constAzureDefaultAPIVersion:           constant.AzureDefaultAPIVersion,
		constGeminiVisionMaxImageNum:          constant.GeminiVisionMaxImageNum,
		constNotifyLimitCount:                 constant.NotifyLimitCount,
		constNotificationLimitDurationMinute:  constant.NotificationLimitDurationMinute,
		constGenerateDefaultToken:             constant.GenerateDefaultToken,
		constErrorLogEnabled:                  constant.ErrorLogEnabled,
		constTaskQueryLimit:                   constant.TaskQueryLimit,
		constTaskPricePatches:                 constant.TaskPricePatches,
	}
}

func covRestoreInitState(s covInitSnapshot) {
	Version = s.version
	SessionSecret = s.sessionSecret
	CryptoSecret = s.cryptoSecret
	*LogDir = s.logDir
	DebugEnabled = s.debugEnabled
	MemoryCacheEnabled = s.memoryCacheEnabled
	IsMasterNode = s.isMasterNode
	RequestInterval = s.requestInterval
	SyncFrequency = s.syncFrequency
	BatchUpdateInterval = s.batchUpdateInterval
	RelayTimeout = s.relayTimeout
	RelayMaxIdleConns = s.relayMaxIdleConns
	RelayMaxIdleConnsPerHost = s.relayMaxIdleConnsPerHost
	RelayResponseHeaderTimeout = s.relayResponseHeaderTimeout
	RelayDialTimeout = s.relayDialTimeout
	HealthDBPingTimeout = s.healthDBPingTimeout
	DBConnectRetries = s.dbConnectRetries
	DBConnectRetryBaseDelay = s.dbConnectRetryBaseDelay
	DBConnectRetryMaxDelay = s.dbConnectRetryMaxDelay
	DBConnectPingTimeout = s.dbConnectPingTimeout
	CostSpikeProtectionEnabled = s.costSpikeProtectionEnabled
	CostSpikeHardLimitPer5Min = s.costSpikeHardLimitPer5Min
	GeminiSafetySetting = s.geminiSafetySetting
	CohereSafetySetting = s.cohereSafetySetting
	GlobalApiRateLimitEnable = s.globalApiRateLimitEnable
	GlobalApiRateLimitNum = s.globalApiRateLimitNum
	GlobalApiRateLimitDuration = s.globalApiRateLimitDuration
	GlobalWebRateLimitEnable = s.globalWebRateLimitEnable
	GlobalWebRateLimitNum = s.globalWebRateLimitNum
	GlobalWebRateLimitDuration = s.globalWebRateLimitDuration
	CriticalRateLimitEnable = s.criticalRateLimitEnable
	CriticalRateLimitNum = s.criticalRateLimitNum
	CriticalRateLimitDuration = s.criticalRateLimitDuration
	InternalApiRateLimitEnable = s.internalApiRateLimitEnable
	InternalApiReadRateLimitNum = s.internalApiReadRateLimitNum
	InternalApiReadRateLimitDuration = s.internalApiReadRateLimitDuration
	InternalApiWriteRateLimitNum = s.internalApiWriteRateLimitNum
	InternalApiWriteRateLimitDuration = s.internalApiWriteRateLimitDuration
	InternalApiProvisionRateLimitNum = s.internalApiProvisionRateLimitNum
	InternalApiProvisionRateLimitDuration = s.internalApiProvisionRateLimitDuration
	BillingDegradedSpendCapLB = s.billingDegradedSpendCapLB
	BillingDegradedWindowSec = s.billingDegradedWindowSec
	constant.StreamingTimeout = s.constStreamingTimeout
	constant.DifyDebug = s.constDifyDebug
	constant.MaxFileDownloadMB = s.constMaxFileDownloadMB
	constant.StreamScannerMaxBufferMB = s.constStreamScannerMaxBufferMB
	constant.MaxRequestBodyMB = s.constMaxRequestBodyMB
	constant.ForceStreamOption = s.constForceStreamOption
	constant.CountToken = s.constCountToken
	constant.GetMediaToken = s.constGetMediaToken
	constant.GetMediaTokenNotStream = s.constGetMediaTokenNotStream
	constant.UpdateTask = s.constUpdateTask
	constant.AzureDefaultAPIVersion = s.constAzureDefaultAPIVersion
	constant.GeminiVisionMaxImageNum = s.constGeminiVisionMaxImageNum
	constant.NotifyLimitCount = s.constNotifyLimitCount
	constant.NotificationLimitDurationMinute = s.constNotificationLimitDurationMinute
	constant.GenerateDefaultToken = s.constGenerateDefaultToken
	constant.ErrorLogEnabled = s.constErrorLogEnabled
	constant.TaskQueryLimit = s.constTaskQueryLimit
	constant.TaskPricePatches = s.constTaskPricePatches
}

// covClearInitEnvVars unsets every env var InitEnv/initConstantEnv reads, so
// each subtest starts from a deterministic "nothing set" baseline regardless
// of what the host shell exports.
func covClearInitEnvVars(t *testing.T) {
	t.Helper()
	vars := []string{
		"VERSION", "SESSION_SECRET", "CRYPTO_SECRET", "DEBUG", "MEMORY_CACHE_ENABLED",
		"NODE_TYPE", "POLLING_INTERVAL", "SYNC_FREQUENCY", "BATCH_UPDATE_INTERVAL",
		"RELAY_TIMEOUT", "RELAY_MAX_IDLE_CONNS", "RELAY_MAX_IDLE_CONNS_PER_HOST",
		"RELAY_RESPONSE_HEADER_TIMEOUT", "RELAY_DIAL_TIMEOUT", "HEALTH_DB_PING_TIMEOUT_MS",
		"DB_CONNECT_RETRIES", "DB_CONNECT_RETRY_DELAY_MS", "DB_CONNECT_RETRY_MAX_DELAY_MS",
		"DB_CONNECT_PING_TIMEOUT_MS", "COST_SPIKE_PROTECTION_ENABLED", "COST_SPIKE_HARD_LIMIT_PER_5MIN",
		"GEMINI_SAFETY_SETTING", "COHERE_SAFETY_SETTING", "GLOBAL_API_RATE_LIMIT_ENABLE",
		"GLOBAL_API_RATE_LIMIT", "GLOBAL_API_RATE_LIMIT_DURATION", "GLOBAL_WEB_RATE_LIMIT_ENABLE",
		"GLOBAL_WEB_RATE_LIMIT", "GLOBAL_WEB_RATE_LIMIT_DURATION", "CRITICAL_RATE_LIMIT_ENABLE",
		"CRITICAL_RATE_LIMIT", "CRITICAL_RATE_LIMIT_DURATION", "INTERNAL_API_RATE_LIMIT_ENABLE",
		"INTERNAL_API_READ_RATE_LIMIT", "INTERNAL_API_READ_RATE_LIMIT_DURATION",
		"INTERNAL_API_WRITE_RATE_LIMIT", "INTERNAL_API_WRITE_RATE_LIMIT_DURATION",
		"INTERNAL_API_PROVISION_RATE_LIMIT", "INTERNAL_API_PROVISION_RATE_LIMIT_DURATION",
		"BILLING_DEGRADED_SPEND_CAP_LB", "BILLING_DEGRADED_WINDOW_SEC",
		"STREAMING_TIMEOUT", "DIFY_DEBUG", "MAX_FILE_DOWNLOAD_MB", "STREAM_SCANNER_MAX_BUFFER_MB",
		"MAX_REQUEST_BODY_MB", "FORCE_STREAM_OPTION", "CountToken", "GET_MEDIA_TOKEN",
		"GET_MEDIA_TOKEN_NOT_STREAM", "UPDATE_TASK", "AZURE_DEFAULT_API_VERSION",
		"GEMINI_VISION_MAX_IMAGE_NUM", "NOTIFY_LIMIT_COUNT", "NOTIFICATION_LIMIT_DURATION_MINUTE",
		"GENERATE_DEFAULT_TOKEN", "ERROR_LOG_ENABLED", "TASK_QUERY_LIMIT", "TASK_PRICE_PATCH",
	}
	for _, v := range vars {
		name := v
		orig, had := os.LookupEnv(name)
		if had {
			t.Cleanup(func() { _ = os.Setenv(name, orig) })
		} else {
			t.Cleanup(func() { _ = os.Unsetenv(name) })
		}
		_ = os.Unsetenv(name)
	}
}

// TestInitEnv_DefaultsWhenUnset locks in the boot-time defaults InitEnv
// computes when no env vars are configured — the "fresh deploy, no
// overrides" path every other env var branch is diffed against.
func TestInitEnv_DefaultsWhenUnset(t *testing.T) {
	snap := covSnapshotInitState()
	t.Cleanup(func() { covRestoreInitState(snap) })
	covClearInitEnvVars(t)

	tmpLogDir := filepath.Join(t.TempDir(), "logs-default")
	*LogDir = tmpLogDir

	InitEnv()

	if RequestInterval != 0 {
		t.Errorf("RequestInterval default = %v, want 0 (POLLING_INTERVAL unset -> Atoi error -> 0)", RequestInterval)
	}
	if DebugEnabled {
		t.Error("DebugEnabled should default false")
	}
	if !IsMasterNode {
		t.Error("IsMasterNode should default true when NODE_TYPE unset")
	}
	if SyncFrequency != 60 {
		t.Errorf("SyncFrequency default = %d, want 60", SyncFrequency)
	}
	if GeminiSafetySetting != "BLOCK_NONE" {
		t.Errorf("GeminiSafetySetting default = %q, want BLOCK_NONE", GeminiSafetySetting)
	}
	if !GlobalApiRateLimitEnable || GlobalApiRateLimitNum != 180 {
		t.Errorf("global api rate limit defaults wrong: enable=%v num=%d", GlobalApiRateLimitEnable, GlobalApiRateLimitNum)
	}
	if !CostSpikeProtectionEnabled || CostSpikeHardLimitPer5Min != 50000 {
		t.Errorf("cost spike defaults wrong: enabled=%v limit=%d", CostSpikeProtectionEnabled, CostSpikeHardLimitPer5Min)
	}
	if BillingDegradedSpendCapLB != 50.0 || BillingDegradedWindowSec != 3600 {
		t.Errorf("billing degraded defaults wrong: cap=%v window=%d", BillingDegradedSpendCapLB, BillingDegradedWindowSec)
	}
	// LogDir must have been created since it did not exist.
	if info, err := os.Stat(tmpLogDir); err != nil || !info.IsDir() {
		t.Errorf("expected InitEnv to create LogDir %s: %v", tmpLogDir, err)
	}
	// initConstantEnv defaults.
	if constant.StreamingTimeout != 300 {
		t.Errorf("constant.StreamingTimeout default = %d, want 300", constant.StreamingTimeout)
	}
	if constant.AzureDefaultAPIVersion != "2025-04-01-preview" {
		t.Errorf("constant.AzureDefaultAPIVersion default = %q", constant.AzureDefaultAPIVersion)
	}
	if constant.GenerateDefaultToken {
		t.Error("constant.GenerateDefaultToken should default false")
	}
}

// TestInitEnv_EnvOverrides exercises every "env var set" branch in one pass:
// secrets, numeric/bool/string overrides, and the TASK_PRICE_PATCH CSV parse
// with embedded blanks and whitespace that must be trimmed/dropped.
func TestInitEnv_EnvOverrides(t *testing.T) {
	snap := covSnapshotInitState()
	t.Cleanup(func() { covRestoreInitState(snap) })
	covClearInitEnvVars(t)

	tmpLogDir := filepath.Join(t.TempDir(), "logs-override")
	*LogDir = tmpLogDir

	t.Setenv("VERSION", "test-v9.9.9")
	t.Setenv("SESSION_SECRET", "a-non-default-secret-value")
	t.Setenv("CRYPTO_SECRET", "a-distinct-crypto-secret")
	t.Setenv("DEBUG", "true")
	t.Setenv("MEMORY_CACHE_ENABLED", "true")
	t.Setenv("NODE_TYPE", "slave")
	t.Setenv("POLLING_INTERVAL", "7")
	t.Setenv("SYNC_FREQUENCY", "45")
	t.Setenv("GEMINI_SAFETY_SETTING", "BLOCK_ONLY_HIGH")
	t.Setenv("COHERE_SAFETY_SETTING", "STRICT")
	t.Setenv("GLOBAL_API_RATE_LIMIT_ENABLE", "false")
	t.Setenv("GLOBAL_API_RATE_LIMIT", "5")
	t.Setenv("BILLING_DEGRADED_SPEND_CAP_LB", "12.5")
	t.Setenv("TASK_PRICE_PATCH", " gpt-4:1.5 , ,claude-3:2.0,")

	InitEnv()

	if Version != "test-v9.9.9" {
		t.Errorf("Version = %q, want overridden value", Version)
	}
	if SessionSecret != "a-non-default-secret-value" {
		t.Errorf("SessionSecret = %q, want overridden value", SessionSecret)
	}
	if CryptoSecret != "a-distinct-crypto-secret" {
		t.Errorf("CryptoSecret = %q, want its own override (not falling back to SessionSecret)", CryptoSecret)
	}
	if !DebugEnabled {
		t.Error("DebugEnabled should be true when DEBUG=true")
	}
	if !MemoryCacheEnabled {
		t.Error("MemoryCacheEnabled should be true")
	}
	if IsMasterNode {
		t.Error("IsMasterNode should be false when NODE_TYPE=slave")
	}
	if RequestInterval != 7*time.Second {
		t.Errorf("RequestInterval = %v, want 7s", RequestInterval)
	}
	if SyncFrequency != 45 {
		t.Errorf("SyncFrequency = %d, want 45", SyncFrequency)
	}
	if GeminiSafetySetting != "BLOCK_ONLY_HIGH" {
		t.Errorf("GeminiSafetySetting = %q", GeminiSafetySetting)
	}
	if CohereSafetySetting != "STRICT" {
		t.Errorf("CohereSafetySetting = %q", CohereSafetySetting)
	}
	if GlobalApiRateLimitEnable {
		t.Error("GlobalApiRateLimitEnable should be false when overridden to false")
	}
	if GlobalApiRateLimitNum != 5 {
		t.Errorf("GlobalApiRateLimitNum = %d, want 5", GlobalApiRateLimitNum)
	}
	if BillingDegradedSpendCapLB != 12.5 {
		t.Errorf("BillingDegradedSpendCapLB = %v, want 12.5", BillingDegradedSpendCapLB)
	}
	wantPatches := []string{"gpt-4:1.5", "claude-3:2.0"}
	if len(constant.TaskPricePatches) != len(wantPatches) {
		t.Fatalf("TaskPricePatches = %#v, want %#v", constant.TaskPricePatches, wantPatches)
	}
	for i, p := range wantPatches {
		if constant.TaskPricePatches[i] != p {
			t.Errorf("TaskPricePatches[%d] = %q, want %q (blank entries must be dropped, whitespace trimmed)", i, constant.TaskPricePatches[i], p)
		}
	}
}

// TestInitEnv_CryptoSecretFallsBackToSessionSecret covers the branch where
// CRYPTO_SECRET is unset: CryptoSecret must fall back to whatever
// SessionSecret resolved to, not keep its old value.
func TestInitEnv_CryptoSecretFallsBackToSessionSecret(t *testing.T) {
	snap := covSnapshotInitState()
	t.Cleanup(func() { covRestoreInitState(snap) })
	covClearInitEnvVars(t)

	tmpLogDir := filepath.Join(t.TempDir(), "logs-fallback")
	*LogDir = tmpLogDir

	t.Setenv("SESSION_SECRET", "fallback-source-secret")
	// CRYPTO_SECRET intentionally left unset.

	InitEnv()

	if CryptoSecret != "fallback-source-secret" {
		t.Errorf("CryptoSecret = %q, want fallback to SessionSecret %q", CryptoSecret, SessionSecret)
	}
}

// TestInitEnv_LogDirAlreadyExists covers the branch where the target log
// directory already exists: InitEnv must not error or attempt to recreate it.
func TestInitEnv_LogDirAlreadyExists(t *testing.T) {
	snap := covSnapshotInitState()
	t.Cleanup(func() { covRestoreInitState(snap) })
	covClearInitEnvVars(t)

	preexisting := filepath.Join(t.TempDir(), "already-here")
	if err := os.MkdirAll(preexisting, 0777); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}
	*LogDir = preexisting

	InitEnv() // must not panic/Fatal even though the dir is already present

	if info, err := os.Stat(preexisting); err != nil || !info.IsDir() {
		t.Errorf("preexisting LogDir should remain a directory: %v", err)
	}
}

// TestInitEnv_PollingIntervalNonNumeric covers the strconv.Atoi error branch:
// a malformed POLLING_INTERVAL is silently treated as 0 rather than crashing.
func TestInitEnv_PollingIntervalNonNumeric(t *testing.T) {
	snap := covSnapshotInitState()
	t.Cleanup(func() { covRestoreInitState(snap) })
	covClearInitEnvVars(t)

	*LogDir = filepath.Join(t.TempDir(), "logs-badpoll")
	t.Setenv("POLLING_INTERVAL", "not-a-number")

	InitEnv()

	if RequestInterval != 0 {
		t.Errorf("RequestInterval = %v, want 0 for a non-numeric POLLING_INTERVAL", RequestInterval)
	}
}

// TestInitConstantEnv_Direct calls the unexported initConstantEnv directly to
// pin down every constant.* field it sets from env, independent of the wider
// InitEnv blast radius.
func TestInitConstantEnv_Direct(t *testing.T) {
	snap := covSnapshotInitState()
	t.Cleanup(func() { covRestoreInitState(snap) })
	covClearInitEnvVars(t)

	t.Setenv("STREAMING_TIMEOUT", "123")
	t.Setenv("DIFY_DEBUG", "false")
	t.Setenv("MAX_FILE_DOWNLOAD_MB", "999")
	t.Setenv("GET_MEDIA_TOKEN_NOT_STREAM", "true")
	t.Setenv("GENERATE_DEFAULT_TOKEN", "true")
	t.Setenv("TASK_QUERY_LIMIT", "42")

	initConstantEnv()

	if constant.StreamingTimeout != 123 {
		t.Errorf("StreamingTimeout = %d, want 123", constant.StreamingTimeout)
	}
	if constant.DifyDebug {
		t.Error("DifyDebug should be false when overridden")
	}
	if constant.MaxFileDownloadMB != 999 {
		t.Errorf("MaxFileDownloadMB = %d, want 999", constant.MaxFileDownloadMB)
	}
	if !constant.GetMediaTokenNotStream {
		t.Error("GetMediaTokenNotStream should be true when overridden")
	}
	if !constant.GenerateDefaultToken {
		t.Error("GenerateDefaultToken should be true when overridden")
	}
	if constant.TaskQueryLimit != 42 {
		t.Errorf("TaskQueryLimit = %d, want 42", constant.TaskQueryLimit)
	}
}

// TestInitConstantEnv_EmptyTaskPricePatchLeavesFieldUntouched documents the
// current (not obviously correct) behavior: since the TASK_PRICE_PATCH branch
// is only entered when the env var is non-empty, calling initConstantEnv with
// it unset does NOT clear a previously-configured constant.TaskPricePatches.
// FINDING: this is a latent stale-config bug if initConstantEnv is ever called
// more than once with a shrinking config (e.g. hot-reload), but InitEnv is
// only ever called once at boot today, so it's not exploitable in production.
func TestInitConstantEnv_EmptyTaskPricePatchLeavesFieldUntouched(t *testing.T) {
	snap := covSnapshotInitState()
	t.Cleanup(func() { covRestoreInitState(snap) })
	covClearInitEnvVars(t)

	constant.TaskPricePatches = []string{"stale-patch:9.9"}

	initConstantEnv() // TASK_PRICE_PATCH unset

	if len(constant.TaskPricePatches) != 1 || constant.TaskPricePatches[0] != "stale-patch:9.9" {
		t.Errorf("TaskPricePatches = %#v, want untouched stale value (documents current behavior)", constant.TaskPricePatches)
	}
}
