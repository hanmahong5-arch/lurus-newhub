package search

import (
	"context"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/bytedance/gopkg/util/gopool"
)

// Worker pool for async indexing
// 用于异步索引的工作池
var asyncPool gopool.Pool

// AsyncGo is this package's fire-and-forget spawn seam, the same convention as
// repo.AsyncGo (internal/adapter/repo/async.go), app.AsyncGo
// (internal/app/quota.go) and handler.AsyncGo (handler/model_meta.go). Its
// default submits to asyncPool, so production keeps the behaviour the four
// Sync*Async functions below had before this seam existed; a package's
// TestMain can assign
// an inline implementation so a submission finishes before the test that
// caused it returns.
//
// Why this package needs one: the submissions below read the package globals
// Client / RetryCount / RetryDelay / Debug / IndexPrefix from a pool worker,
// while cov_helpers_test.go's withFakeClient restores those same globals in
// t.Cleanup. There is no join point between the two, which is the shape the
// cycle-12 plan §1.2 records as the 2026-09-09 CI `-race` failure at
// sync.go's batch submission. (That attribution is quoted from the plan, not
// re-measured here — `-race` needs cgo and does not run on the dev host.)
//
// This seam does not change the panic semantics of the calls it replaces:
// they were already asyncPool.Go, and a gopool worker recovers a panic in the
// submitted func and logs it (gopool@v0.1.3 util/gopool/worker.go run())
// rather than letting it crash the process.
var AsyncGo = func(f func()) { asyncPool.Go(f) }

// syncCtx and syncCancel for graceful shutdown
var (
	syncCtx    context.Context
	syncCancel context.CancelFunc
)

// InitSync initializes the sync mechanism
// 初始化同步机制
func InitSync() error {
	return InitSyncWithContext(context.Background())
}

// InitSyncWithContext initializes the sync mechanism with context support.
// The context should be cancelled on application shutdown.
// 使用 context 支持初始化同步机制。应用关闭时应取消 context。
func InitSyncWithContext(ctx context.Context) error {
	if !IsEnabled() || !SyncEnabled {
		return nil
	}

	// Initialize worker pool for async operations
	// 初始化工作池用于异步操作
	asyncPool = gopool.NewPool("meilisearch-sync", int32(WorkerCount), gopool.NewConfig()) // #nosec G115 — WorkerCount is config-bounded to ≤256; fits in int32.

	common.SysLog(fmt.Sprintf("Meilisearch sync initialized with %d workers", WorkerCount))

	// Start scheduled sync if interval > 0
	// 如果间隔 > 0 则启动定时同步
	if SyncInterval > 0 {
		syncCtx, syncCancel = context.WithCancel(ctx)
		go ScheduledSyncWithContext(syncCtx, SyncInterval)
		common.SysLog(fmt.Sprintf("Scheduled sync started with interval %d seconds", SyncInterval))
	}

	return nil
}

// StopSync stops the scheduled sync task gracefully.
// 优雅停止定时同步任务。
func StopSync() {
	if syncCancel != nil {
		syncCancel()
	}
}

// SyncLogAsync asynchronously syncs a single log to Meilisearch
// 异步将单个日志同步到 Meilisearch
func SyncLogAsync(log *Log) {
	if !IsEnabled() || !SyncEnabled {
		return
	}

	if asyncPool == nil {
		// If pool not initialized, index synchronously
		// 如果池未初始化,则同步索引
		_ = IndexLog(log)
		return
	}

	// Submit to worker pool
	// 提交到工作池
	AsyncGo(func() {
		err := RetryWithBackoff(func() error {
			return IndexLog(log)
		})
		if err != nil && Debug {
			common.SysLog(fmt.Sprintf("Failed to sync log %d after retries: %v", log.Id, err))
		}
	})
}

// SyncLogsBatchAsync asynchronously syncs multiple logs in batch
// 异步批量同步多个日志
func SyncLogsBatchAsync(logs []*Log) {
	if !IsEnabled() || !SyncEnabled || len(logs) == 0 {
		return
	}

	if asyncPool == nil {
		// If pool not initialized, index synchronously
		// 如果池未初始化,则同步索引
		_ = IndexLogsBatch(logs)
		return
	}

	// Submit to worker pool
	// 提交到工作池
	AsyncGo(func() {
		err := RetryWithBackoff(func() error {
			return IndexLogsBatch(logs)
		})
		if err != nil && Debug {
			common.SysLog(fmt.Sprintf("Failed to sync log batch after retries: %v", err))
		}
	})
}

// SyncUserAsync asynchronously syncs a user to Meilisearch
// 异步将用户同步到 Meilisearch
func SyncUserAsync(user *User) {
	if !IsEnabled() || !SyncEnabled {
		return
	}

	if asyncPool == nil {
		_ = IndexUser(user)
		return
	}

	AsyncGo(func() {
		err := RetryWithBackoff(func() error {
			return IndexUser(user)
		})
		if err != nil && Debug {
			common.SysLog(fmt.Sprintf("Failed to sync user %d: %v", user.Id, err))
		}
	})
}

// SyncChannelAsync asynchronously syncs a channel to Meilisearch
// 异步将通道同步到 Meilisearch
func SyncChannelAsync(channel *Channel) {
	if !IsEnabled() || !SyncEnabled {
		return
	}

	if asyncPool == nil {
		_ = IndexChannel(channel)
		return
	}

	AsyncGo(func() {
		err := RetryWithBackoff(func() error {
			return IndexChannel(channel)
		})
		if err != nil && Debug {
			common.SysLog(fmt.Sprintf("Failed to sync channel %d: %v", channel.Id, err))
		}
	})
}

// ScheduledSync performs scheduled background sync
// Deprecated: Use ScheduledSyncWithContext instead.
// 执行定时后台同步
func ScheduledSync(intervalSeconds int) {
	ScheduledSyncWithContext(context.Background(), intervalSeconds)
}

// ScheduledSyncWithContext performs scheduled background sync with context support.
// 使用 context 支持执行定时后台同步
func ScheduledSyncWithContext(ctx context.Context, intervalSeconds int) {
	if !IsEnabled() || !SyncEnabled {
		return
	}

	ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			common.SysLog("scheduled sync stopped")
			return
		case <-ticker.C:
			if Debug {
				common.SysLog("Starting scheduled sync...")
			}

			// Sync recent logs (last hour)
			// 同步最近的日志(最后一小时)
			err := SyncRecentLogs(3600)
			if err != nil {
				common.SysLog(fmt.Sprintf("Scheduled log sync failed: %v", err))
			}

			if Debug {
				common.SysLog("Scheduled sync completed")
			}
		}
	}
}

// SyncRecentLogs syncs logs created within the last N seconds
// 同步在最近 N 秒内创建的日志
func SyncRecentLogs(lastSeconds int64) error {
	if !IsEnabled() {
		return fmt.Errorf("Meilisearch is not enabled")
	}

	// NOTE: This function needs to be called from model package to access LOG_DB
	// 注意: 这个函数需要从 model 包调用以访问 LOG_DB
	// For now, just log a message
	// 目前只记录一条消息
	common.SysLog("SyncRecentLogs: This function should be called from model package")
	return nil
}

// SyncAllLogs performs a full sync of all logs (use with caution for large datasets)
// 执行所有日志的完整同步(大数据集需谨慎使用)
func SyncAllLogs() error {
	if !IsEnabled() {
		return fmt.Errorf("Meilisearch is not enabled")
	}

	// NOTE: This function needs to be called from model package to access LOG_DB
	// 注意: 这个函数需要从 model 包调用以访问 LOG_DB
	common.SysLog("SyncAllLogs: This function should be called from model package")
	return nil
}

// SyncAllUsers performs a full sync of all users
// 执行所有用户的完整同步
func SyncAllUsers() error {
	if !IsEnabled() {
		return fmt.Errorf("Meilisearch is not enabled")
	}

	// NOTE: This function needs to be called from model package to access DB
	// 注意: 这个函数需要从 model 包调用以访问 DB
	common.SysLog("SyncAllUsers: This function should be called from model package")
	return nil
}

// SyncAllChannels performs a full sync of all channels
// 执行所有通道的完整同步
func SyncAllChannels() error {
	if !IsEnabled() {
		return fmt.Errorf("Meilisearch is not enabled")
	}

	// NOTE: This function needs to be called from model package to access DB
	// 注意: 这个函数需要从 model 包调用以访问 DB
	common.SysLog("SyncAllChannels: This function should be called from model package")
	return nil
}

// RebuildAllIndexes deletes and rebuilds all indexes with full data sync
// 删除并重建所有索引,并进行完整数据同步
func RebuildAllIndexes() error {
	if !IsEnabled() {
		return fmt.Errorf("Meilisearch is not enabled")
	}

	common.SysLog("Starting full index rebuild...")

	// Rebuild logs index
	// 重建日志索引
	if err := RebuildIndex(IndexLogs); err != nil {
		return fmt.Errorf("failed to rebuild logs index: %w", err)
	}
	if err := SyncAllLogs(); err != nil {
		return fmt.Errorf("failed to sync logs: %w", err)
	}

	// Rebuild users index
	// 重建用户索引
	if err := RebuildIndex(IndexUsers); err != nil {
		return fmt.Errorf("failed to rebuild users index: %w", err)
	}
	if err := SyncAllUsers(); err != nil {
		return fmt.Errorf("failed to sync users: %w", err)
	}

	// Rebuild channels index
	// 重建通道索引
	if err := RebuildIndex(IndexChannels); err != nil {
		return fmt.Errorf("failed to rebuild channels index: %w", err)
	}
	if err := SyncAllChannels(); err != nil {
		return fmt.Errorf("failed to sync channels: %w", err)
	}

	common.SysLog("Full index rebuild completed")
	return nil
}
