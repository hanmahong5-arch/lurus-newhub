package limiter

import (
	"context"
	_ "embed"
	"fmt"
	"sync"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/redis/go-redis/v9"
)

//go:embed lua/rate_limit.lua
var rateLimitScript string

// rateLimit runs by SHA and falls back to sending the source when Redis
// answers NOSCRIPT. The limiter used to EvalSha a SHA loaded once at startup:
// after a Redis restart or SCRIPT FLUSH (or a failed load at boot, SHA "")
// every call errored, and the model rate limiter fails open on error, so
// limits were off until the pod restarted.
var rateLimit = redis.NewScript(rateLimitScript)

type RedisLimiter struct {
	client *redis.Client
}

var (
	instance *RedisLimiter
	once     sync.Once
)

func New(ctx context.Context, r *redis.Client) *RedisLimiter {
	once.Do(func() {
		// Preload is only an optimisation now: Run reloads on NOSCRIPT.
		if err := rateLimit.Load(ctx, r).Err(); err != nil {
			common.SysLog(fmt.Sprintf("Failed to load rate limit script: %v", err))
		}
		instance = &RedisLimiter{client: r}
	})

	return instance
}

func (rl *RedisLimiter) Allow(ctx context.Context, key string, opts ...Option) (bool, error) {
	// 默认配置
	config := &Config{
		Capacity:  10,
		Rate:      1,
		Requested: 1,
	}

	// 应用选项模式
	for _, opt := range opts {
		opt(config)
	}

	// 执行限流
	result, err := rateLimit.Run(
		ctx,
		rl.client,
		[]string{key},
		config.Requested,
		config.Rate,
		config.Capacity,
	).Int()

	if err != nil {
		return false, fmt.Errorf("rate limit failed: %w", err)
	}
	return result == 1, nil
}

// Config 配置选项模式
type Config struct {
	Capacity  int64
	Rate      int64
	Requested int64
}

type Option func(*Config)

func WithCapacity(c int64) Option {
	return func(cfg *Config) { cfg.Capacity = c }
}

func WithRate(r int64) Option {
	return func(cfg *Config) { cfg.Rate = r }
}

func WithRequested(n int64) Option {
	return func(cfg *Config) { cfg.Requested = n }
}
