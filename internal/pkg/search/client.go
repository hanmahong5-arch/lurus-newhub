package search

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/meilisearch/meilisearch-go"
)

// Global Meilisearch client instance
// Meilisearch 客户端全局实例
var Client meilisearch.ServiceManager

// Configuration variables
// 配置变量
var (
	Enabled         bool   // Whether Meilisearch integration is enabled / 是否启用 Meilisearch 集成
	Host            string // Meilisearch host URL / Meilisearch 主机地址
	APIKey          string // Meilisearch API key / Meilisearch API 密钥
	SyncEnabled     bool   // Whether automatic sync is enabled / 是否启用自动同步
	SyncBatchSize   int    // Batch size for bulk operations / 批量操作的批次大小
	SyncInterval    int    // Sync interval in seconds / 同步间隔(秒)
	MaxResults      int    // Maximum search results / 最大搜索结果数
	SearchTimeout   int64  // Search timeout in milliseconds / 搜索超时时间(毫秒)
	Debug           bool   // Enable debug logging / 启用调试日志
	WorkerCount     int    // Number of worker goroutines / 工作协程数
	RetryCount      int    // Number of retries for failed operations / 失败操作重试次数
	RetryDelay      int    // Retry delay in milliseconds / 重试延迟(毫秒)
	AutoCreateIndex bool   // Auto create index if not exists / 自动创建索引
	IndexPrefix     string // Index name prefix / 索引名称前缀
)

// InitMeilisearch initializes the Meilisearch client and loads configuration
// 初始化 Meilisearch 客户端并加载配置
func InitMeilisearch() error {
	// Load configuration from environment variables
	// 从环境变量加载配置
	loadConfig()

	// Check if Meilisearch is enabled
	// 检查是否启用 Meilisearch
	if !Enabled {
		common.SysLog("Meilisearch integration is disabled")
		return nil
	}

	// Validate required configuration
	// 验证必需的配置
	if Host == "" {
		return fmt.Errorf("MEILISEARCH_HOST is required when MEILISEARCH_ENABLED=true")
	}
	if APIKey == "" {
		return fmt.Errorf("MEILISEARCH_API_KEY is required when MEILISEARCH_ENABLED=true")
	}

	// Create Meilisearch client with a bounded HTTP timeout (P1-1). Meilisearch
	// is off the relay/billing hot path (log full-text search only), but without
	// a client timeout a hung or slow instance would block the admin log handler
	// indefinitely. SearchTimeout caps every Meilisearch call so a down search
	// backend fails fast instead of pinning request goroutines. A 0/negative
	// value falls back to a sane 5s default rather than "no timeout".
	timeout := time.Duration(SearchTimeout) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	Client = meilisearch.New(Host,
		meilisearch.WithAPIKey(APIKey),
		meilisearch.WithCustomClient(&http.Client{Timeout: timeout}))

	// Test connection and get health status
	// 测试连接并获取健康状态
	health, err := Client.Health()
	if err != nil {
		return fmt.Errorf("failed to connect to Meilisearch: %w", err)
	}

	common.SysLog(fmt.Sprintf("Connected to Meilisearch at %s, status: %s", Host, health.Status))

	// Get Meilisearch version
	// 获取 Meilisearch 版本
	version, err := Client.Version()
	if err != nil {
		common.SysLog(fmt.Sprintf("Warning: could not get Meilisearch version: %v", err))
	} else {
		common.SysLog(fmt.Sprintf("Meilisearch version: %s", version.PkgVersion))
	}

	// Initialize indexes with configuration
	// 使用配置初始化索引
	if AutoCreateIndex {
		if err := InitializeIndexes(); err != nil {
			return fmt.Errorf("failed to initialize indexes: %w", err)
		}
	}

	common.SysLog("Meilisearch client initialized successfully")
	return nil
}

// loadConfig loads configuration from environment variables
// 从环境变量加载配置
func loadConfig() {
	// Basic settings
	// 基本设置
	Enabled = getBoolEnv("MEILISEARCH_ENABLED", false)
	Host = getEnv("MEILISEARCH_HOST", "http://localhost:7700")
	APIKey = getEnv("MEILISEARCH_API_KEY", "")

	// Sync settings
	// 同步设置
	SyncEnabled = getBoolEnv("MEILISEARCH_SYNC_ENABLED", true)
	SyncBatchSize = getIntEnv("MEILISEARCH_SYNC_BATCH_SIZE", 1000)
	SyncInterval = getIntEnv("MEILISEARCH_SYNC_INTERVAL", 60)

	// Search settings
	// 搜索设置
	MaxResults = getIntEnv("MEILISEARCH_MAX_SEARCH_RESULTS", 1000)
	SearchTimeout = int64(getIntEnv("MEILISEARCH_SEARCH_TIMEOUT", 5000))

	// Advanced settings
	// 高级设置
	Debug = getBoolEnv("MEILISEARCH_DEBUG", false)
	WorkerCount = getIntEnv("MEILISEARCH_WORKER_COUNT", 10)
	RetryCount = getIntEnv("MEILISEARCH_RETRY_COUNT", 3)
	RetryDelay = getIntEnv("MEILISEARCH_RETRY_DELAY", 1000)
	AutoCreateIndex = getBoolEnv("MEILISEARCH_AUTO_CREATE_INDEX", true)
	IndexPrefix = getEnv("MEILISEARCH_INDEX_PREFIX", "")

	if Debug {
		logConfig()
	}
}

// logConfig logs the current configuration
// 记录当前配置
func logConfig() {
	common.SysLog("=== Meilisearch Configuration ===")
	common.SysLog(fmt.Sprintf("Enabled: %v", Enabled))
	common.SysLog(fmt.Sprintf("Host: %s", Host))
	common.SysLog(fmt.Sprintf("API Key: %s", maskAPIKey(APIKey)))
	common.SysLog(fmt.Sprintf("Sync Enabled: %v", SyncEnabled))
	common.SysLog(fmt.Sprintf("Sync Batch Size: %d", SyncBatchSize))
	common.SysLog(fmt.Sprintf("Sync Interval: %d seconds", SyncInterval))
	common.SysLog(fmt.Sprintf("Max Results: %d", MaxResults))
	common.SysLog(fmt.Sprintf("Search Timeout: %d ms", SearchTimeout))
	common.SysLog(fmt.Sprintf("Worker Count: %d", WorkerCount))
	common.SysLog(fmt.Sprintf("Retry Count: %d", RetryCount))
	common.SysLog(fmt.Sprintf("Retry Delay: %d ms", RetryDelay))
	common.SysLog(fmt.Sprintf("Auto Create Index: %v", AutoCreateIndex))
	common.SysLog(fmt.Sprintf("Index Prefix: %s", IndexPrefix))
	common.SysLog("================================")
}

// maskAPIKey masks the API key for logging
// 为日志记录屏蔽 API 密钥
func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// IsEnabled returns whether Meilisearch integration is enabled
// 返回 Meilisearch 集成是否启用
func IsEnabled() bool {
	return Enabled && Client != nil
}

// IsHealthy checks if Meilisearch is healthy
// 检查 Meilisearch 是否健康
func IsHealthy() bool {
	if !IsEnabled() {
		return false
	}

	health, err := Client.Health()
	if err != nil {
		return false
	}

	return health.Status == "available"
}

// GetStats returns statistics about indexes
// 返回索引的统计信息
func GetStats() (map[string]interface{}, error) {
	if !IsEnabled() {
		return nil, fmt.Errorf("Meilisearch is not enabled")
	}

	stats, err := Client.GetStats()
	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %w", err)
	}

	result := make(map[string]interface{})
	result["database_size"] = stats.DatabaseSize
	result["indexes"] = len(stats.Indexes)

	indexStats := make(map[string]interface{})
	for name, indexStat := range stats.Indexes {
		indexStats[name] = map[string]interface{}{
			"documents": indexStat.NumberOfDocuments,
			"indexing":  indexStat.IsIndexing,
		}
	}
	result["index_stats"] = indexStats

	return result, nil
}

// Helper functions for environment variables
// 环境变量辅助函数

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getIntEnv(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getBoolEnv(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}

// RetryWithBackoff executes a function with exponential backoff retry logic
// 使用指数退避重试逻辑执行函数
func RetryWithBackoff(fn func() error) error {
	// RetryCount 由运维配置（MEILISEARCH_RETRY_COUNT），<1 时归一化为 1，
	// 保证 fn 至少执行一次，否则操作会被整个跳过且返回无意义的错误
	attempts := RetryCount
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for i := 0; i < attempts; i++ {
		err = fn()
		if err == nil {
			return nil
		}

		if i < attempts-1 {
			delay := time.Duration(RetryDelay*(i+1)) * time.Millisecond
			if Debug {
				common.SysLog(fmt.Sprintf("Retry %d/%d failed, waiting %v: %v", i+1, attempts, delay, err))
			}
			time.Sleep(delay)
		}
	}
	return fmt.Errorf("failed after %d retries: %w", attempts, err)
}

// GetIndexName returns the full index name with prefix
// 返回带前缀的完整索引名称
func GetIndexName(baseName string) string {
	if IndexPrefix == "" {
		return baseName
	}
	return IndexPrefix + "_" + baseName
}
