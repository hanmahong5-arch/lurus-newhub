package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config is the centralized application configuration.
// All values are loaded from environment variables with sensible defaults.
type Config struct {
	Server   ServerConfig
	Relay    RelayConfig
	Search   SearchConfig
	Cron     CronConfig
	Security SecurityConfig
	Storage  StorageConfig
	CORS     CORSConfig
}

// StorageConfig holds object storage related settings.
type StorageConfig struct {
	// MinIOBucket is the MinIO bucket name for release artifacts.
	MinIOBucket string
	// MinIOEndpoint is the MinIO server address (host:port), used for internal cluster connections.
	MinIOEndpoint string
	// MinIOAccessKey is the MinIO access key.
	MinIOAccessKey string
	// MinIOSecretKey is the MinIO secret key.
	MinIOSecretKey string
	// MinIOSecure enables TLS for the MinIO connection.
	MinIOSecure bool
	// MinIOPublicEndpoint is the externally accessible base URL for presigned download URLs
	// (e.g., "https://minio-api.lurus.cn"). If empty, the internal endpoint is used.
	MinIOPublicEndpoint string
}

// CORSConfig holds CORS middleware settings.
type CORSConfig struct {
	// AllowedOrigins is the list of allowed CORS origins.
	AllowedOrigins []string
}

// ServerConfig holds HTTP server related settings.
type ServerConfig struct {
	// GracefulShutdownTimeout is the max wait time for in-flight requests on shutdown.
	GracefulShutdownTimeout time.Duration
}

// RelayConfig holds relay/streaming related settings.
type RelayConfig struct {
	// StreamScannerInitialBuffer is the initial buffer size for the SSE scanner (bytes).
	StreamScannerInitialBuffer int
	// StreamScannerMaxBuffer is the max buffer size for the SSE scanner (bytes).
	// Can also be set via STREAM_SCANNER_MAX_BUFFER_MB env var (in MB).
	StreamScannerMaxBuffer int
	// PingInterval is the default interval between SSE keep-alive pings.
	PingInterval time.Duration
	// WriteTimeout is the timeout for individual write operations during streaming.
	WriteTimeout time.Duration
	// MaxPingDuration is the maximum total duration a ping goroutine will run.
	MaxPingDuration time.Duration
	// GoroutineShutdownTimeout is the max wait time for streaming goroutines to exit.
	GoroutineShutdownTimeout time.Duration
	// StopChannelBuffer is the buffer size for the streaming stop channel.
	StopChannelBuffer int
}

// SearchConfig holds Meilisearch related settings.
type SearchConfig struct {
	// SyncWorkerCount is the number of concurrent workers for Meilisearch sync.
	SyncWorkerCount int
}

// CronConfig holds background cron job settings.
type CronConfig struct {
	// QuotaResetCheckInterval is the interval between daily quota reset checks.
	QuotaResetCheckInterval time.Duration
}

// SecurityConfig holds security related settings.
type SecurityConfig struct {
	// SMSCodeExpiration is the TTL for SMS verification codes.
	SMSCodeExpiration time.Duration
	// TrustedProxies is the list of CIDRs gin trusts to have set X-Forwarded-For
	// honestly. Default covers the RFC1918 + loopback + link-local ranges the
	// in-cluster host nginx reverse proxy connects from; anything outside these
	// ranges has its X-Forwarded-For ignored and ClientIP() falls back to the
	// TCP peer address.
	TrustedProxies []string
}

var (
	globalConfig *Config
	configOnce   sync.Once
)

// Get returns the global Config instance, initializing it from env vars on first call.
func Get() *Config {
	configOnce.Do(func() {
		globalConfig = loadFromEnv()
	})
	return globalConfig
}

// loadFromEnv loads all configuration from environment variables with defaults.
func loadFromEnv() *Config {
	cfg := &Config{
		Server: ServerConfig{
			GracefulShutdownTimeout: envDuration("GRACEFUL_SHUTDOWN_TIMEOUT", 30*time.Second),
		},
		Relay: RelayConfig{
			StreamScannerInitialBuffer: envInt("STREAM_SCANNER_INITIAL_BUFFER", 64<<10),   // 64KB
			StreamScannerMaxBuffer:     envInt("STREAM_SCANNER_MAX_BUFFER", 64<<20),        // 64MB
			PingInterval:               envDuration("RELAY_PING_INTERVAL", 10*time.Second),
			WriteTimeout:               envDuration("RELAY_WRITE_TIMEOUT", 10*time.Second),
			MaxPingDuration:            envDuration("RELAY_MAX_PING_DURATION", 30*time.Minute),
			GoroutineShutdownTimeout:   envDuration("RELAY_GOROUTINE_SHUTDOWN_TIMEOUT", 5*time.Second),
			StopChannelBuffer:          envInt("RELAY_STOP_CHANNEL_BUFFER", 3),
		},
		Search: SearchConfig{
			SyncWorkerCount: envInt("MEILISEARCH_SYNC_WORKERS", 32),
		},
		Cron: CronConfig{
			QuotaResetCheckInterval: envDuration("QUOTA_RESET_CHECK_INTERVAL", 1*time.Minute),
		},
		Security: SecurityConfig{
			SMSCodeExpiration: envDuration("SMS_CODE_EXPIRATION", 5*time.Minute),
			TrustedProxies: envStringSlice("TRUSTED_PROXIES", []string{
				"10.0.0.0/8",
				"172.16.0.0/12",
				"192.168.0.0/16",
				"127.0.0.0/8",
				"::1/128",
				"fe80::/10",
			}),
		},
		Storage: StorageConfig{
			MinIOBucket:         envString("MINIO_RELEASES_BUCKET", "lurus-releases"),
			MinIOEndpoint:       envString("MINIO_ENDPOINT", ""),
			MinIOAccessKey:      envString("MINIO_ACCESS_KEY", ""),
			MinIOSecretKey:      envString("MINIO_SECRET_KEY", ""),
			MinIOSecure:         envString("MINIO_SECURE", "false") == "true",
			MinIOPublicEndpoint: envString("MINIO_PUBLIC_ENDPOINT", ""),
		},
		CORS: CORSConfig{
			AllowedOrigins: envStringSlice("ALLOWED_ORIGINS", []string{
				"https://lurus.cn",
				"https://www.lurus.cn",
				"https://gushen.lurus.cn",
				"https://webmail.lurus.cn",
				"http://localhost:5173",
				"http://localhost:3000",
			}),
		},
	}
	return cfg
}

// PrintEffective logs the effective configuration at startup.
func (c *Config) PrintEffective() string {
	return fmt.Sprintf(
		"Config: relay.ping_interval=%s relay.write_timeout=%s relay.max_ping_duration=%s relay.scanner_buffer=%dKB/%dMB",
		c.Relay.PingInterval,
		c.Relay.WriteTimeout,
		c.Relay.MaxPingDuration,
		c.Relay.StreamScannerInitialBuffer/1024,
		c.Relay.StreamScannerMaxBuffer/(1024*1024),
	)
}

// --- env helpers ---

func envString(key string, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func envStringSlice(key string, defaultVal []string) []string {
	if v := os.Getenv(key); v != "" {
		parts := strings.Split(v, ",")
		result := make([]string, 0, len(parts))
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
		if len(result) > 0 {
			return result
		}
	}
	return defaultVal
}

func envInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultVal
}

func envDuration(key string, defaultVal time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		// Also try as seconds (integer)
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return defaultVal
}
