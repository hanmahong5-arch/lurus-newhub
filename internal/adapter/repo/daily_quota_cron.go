package repo

import (
	"context"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

const (
	// DailyQuotaResetInterval is the interval between reset checks
	DailyQuotaResetInterval = 60 // seconds
	// DailyQuotaResetBatchSize is the number of users to process per batch
	DailyQuotaResetBatchSize = 100
)

// StartDailyQuotaResetCron starts the daily quota reset cron job.
// Deprecated: Use StartDailyQuotaResetCronWithContext instead.
func StartDailyQuotaResetCron() {
	StartDailyQuotaResetCronWithContext(context.Background())
}

// StartDailyQuotaResetCronWithContext starts the daily quota reset cron job with context support.
// The goroutine exits when ctx is cancelled.
func StartDailyQuotaResetCronWithContext(ctx context.Context) {
	common.SysLog("Starting daily quota reset cron job")

	ticker := time.NewTicker(time.Duration(DailyQuotaResetInterval) * time.Second)

	common.SafeGoWithContext(ctx, func(c context.Context) {
		defer ticker.Stop()
		// Run immediately on startup. Each pass is panic-isolated: under the
		// single outer SafeGoWithContext one panic would end the cron for the
		// pod's lifetime.
		common.RunTickSafe("daily-quota-reset", processDailyQuotaResets)

		for {
			select {
			case <-c.Done():
				common.SysLog("Daily quota reset cron stopped")
				return
			case <-ticker.C:
				common.RunTickSafe("daily-quota-reset", processDailyQuotaResets)
			}
		}
	})
}

// processDailyQuotaResets processes daily quota resets for all users that need it
func processDailyQuotaResets() {
	users, err := GetUsersNeedingDailyReset(DailyQuotaResetBatchSize)
	if err != nil {
		common.SysError("Failed to get users needing daily reset: " + err.Error())
		return
	}

	if len(users) == 0 {
		return
	}

	common.SysLog("Processing daily quota reset for " + strconv.Itoa(len(users)) + " users")

	for _, user := range users {
		err := ProcessDailyQuotaReset(user.Id)
		if err != nil {
			common.SysError("Failed to reset daily quota for user " + strconv.Itoa(user.Id) + ": " + err.Error())
			continue
		}
	}

	common.SysLog("Completed daily quota reset batch")
}

// CheckAndHandleDailyQuotaExhaustion checks if user's daily quota is exhausted
// and switches to fallback group if needed
// Returns true if quota is exhausted (user should be limited)
func CheckAndHandleDailyQuotaExhaustion(userId int, quotaToConsume int) (bool, error) {
	info, err := GetUserDailyQuotaInfo(userId)
	if err != nil {
		return false, err
	}

	// If daily quota is not set (0), no limit
	if info.DailyQuota <= 0 {
		return false, nil
	}

	// Check if needs reset first
	if info.NeedsReset {
		if err := ProcessDailyQuotaReset(userId); err != nil {
			common.SysError("Failed to reset daily quota during check: " + err.Error())
		}
		// Refresh info after reset
		info, err = GetUserDailyQuotaInfo(userId)
		if err != nil {
			return false, err
		}
	}

	// Check if daily quota would be exceeded
	if info.DailyUsed+quotaToConsume > info.DailyQuota {
		// Switch to fallback group
		if !info.IsUsingFallback && info.FallbackGroup != "" {
			if err := SwitchToFallbackGroup(userId); err != nil {
				common.SysError("Failed to switch to fallback group: " + err.Error())
			}
		}
		return true, nil
	}

	return false, nil
}

// PostConsumeDailyQuota updates daily used quota after consumption
// This should be called after successful API request
func PostConsumeDailyQuota(userId int, quotaConsumed int) error {
	if quotaConsumed <= 0 {
		return nil
	}

	// Increase daily used
	if err := IncreaseDailyUsed(userId, quotaConsumed); err != nil {
		return err
	}

	// Check if quota exhausted after consumption
	info, err := GetUserDailyQuotaInfo(userId)
	if err != nil {
		return err
	}

	// If daily quota is not set, no action needed
	if info.DailyQuota <= 0 {
		return nil
	}

	// Check if quota is now exhausted
	if info.DailyUsed >= info.DailyQuota && !info.IsUsingFallback && info.FallbackGroup != "" {
		return SwitchToFallbackGroup(userId)
	}

	return nil
}
