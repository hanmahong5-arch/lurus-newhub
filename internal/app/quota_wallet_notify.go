package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	hubnats "github.com/LurusTech/lurus-hub/internal/pkg/nats"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

// mirrorUsageEvent posts one charged relay into platform billing.usage_events.
func mirrorUsageEvent(ctx context.Context, relayInfo *relaycommon.RelayInfo, accountID int64, totalQuota int, amountLB float64, idemKey string, preAuthID int64) {
	meta := map[string]any{
		"amount_cny": amountLB,
		"model":      relayInfo.OriginModelName,
		"user_id":    relayInfo.UserId,
	}
	if preAuthID > 0 {
		meta["preauth_id"] = preAuthID
	}
	addMirrorCostMeta(meta, relayInfo)
	if rptErr := common.ReportUsageEvent(ctx, accountID, sourceProductOf(relayInfo), "llm_relay",
		int64(totalQuota), idemKey, meta); rptErr != nil {
		metrics.BillingUsageMirrorTotal.WithLabelValues("error").Inc()
		common.SysLog(fmt.Sprintf("usage mirror failed (drift data point lost): account=%d, key=%s, err=%s",
			accountID, idemKey, rptErr.Error()))
		return
	}
	metrics.BillingUsageMirrorTotal.WithLabelValues("success").Inc()
}

var zeroWalletWarnLast sync.Map

var zeroWalletWarnLogf = common.SysError

func resetZeroWalletWarnThrottle() {
	zeroWalletWarnLast.Range(func(key, _ any) bool {
		zeroWalletWarnLast.Delete(key)
		return true
	})
}

// warnZeroWalletAmount detects a positive local quota whose wallet charge rounds to 0.0000 LB.
func warnZeroWalletAmount(accountID int64, totalQuota int, amountLB float64) {
	if totalQuota <= 0 || amountLB >= 0.00005 {
		return
	}
	metrics.BillingZeroAmountChargeTotal.Inc()

	now := time.Now()
	if last, loaded := zeroWalletWarnLast.LoadOrStore(accountID, now); loaded {
		if now.Sub(last.(time.Time)) < time.Minute {
			return
		}
		zeroWalletWarnLast.Store(accountID, now)
	}

	zeroWalletWarnLogf(fmt.Sprintf(
		`{"event":"wallet_zero_amount_charge","who":"account:%d","what":"positive local quota %d converts to %.6f LB","result":"will round to 0.0000 under wallet numeric(14,4), charge silently lost"}`,
		accountID, totalQuota, amountLB))
}

// reportQuotaThreshold fetches the user's current quota state and publishes NATS threshold events.
func reportQuotaThreshold(ctx context.Context, relayInfo *relaycommon.RelayInfo, consumed int) {
	if !hubnats.Enabled() {
		return
	}
	pub := hubnats.Get()
	if pub == nil {
		return
	}
	if relayInfo.IdentityAccountID <= 0 || relayInfo.UserId == 0 || consumed <= 0 {
		return
	}

	user, err := repo.GetUserById(relayInfo.UserId)
	if err != nil {
		common.SysLog(fmt.Sprintf("quota threshold: failed to fetch user %d: %s", relayInfo.UserId, err.Error()))
		return
	}

	usedAfter := int64(user.UsedQuota)
	limitTokens := int64(user.Quota) + usedAfter

	var rdb redisDeduper
	if common.RedisEnabled && common.RDB != nil {
		rdb = wrapRedis(common.RDB)
	}

	checkAndPublishQuotaThresholds(ctx, quotaThresholdParams{
		UserId:            relayInfo.UserId,
		IdentityAccountID: relayInfo.IdentityAccountID,
		TenantID:          user.TenantId,
		QuotaConsumed:     int64(consumed),
		UsedTokensAfter:   usedAfter,
		LimitTokens:       limitTokens,
	}, rdb, pub)
}

const quotaNotifyBudget = 10 * time.Second

func checkAndSendQuotaNotify(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int) {
	AsyncGo(func() {
		userSetting := relayInfo.UserSetting
		threshold := common.QuotaRemindThreshold
		if userSetting.QuotaWarningThreshold != 0 {
			threshold = int(userSetting.QuotaWarningThreshold)
		}

		consumeQuota := quota + preConsumedQuota
		if relayInfo.UserQuota-consumeQuota >= threshold {
			return
		}

		prompt := "您的额度即将用尽"
		topUpLink := fmt.Sprintf("%s/wallet", system_setting.ServerAddress)

		var content string
		var values []interface{}

		notifyType := userSetting.NotifyType
		if notifyType == "" {
			notifyType = dto.NotifyTypeEmail
		}

		if notifyType == dto.NotifyTypeBark {
			content = "{{value}}，剩余额度：{{value}}，请及时充值"
			values = []interface{}{prompt, logger.FormatQuota(relayInfo.UserQuota)}
		} else if notifyType == dto.NotifyTypeGotify {
			content = "{{value}}，当前剩余额度为 {{value}}，请及时充值。"
			values = []interface{}{prompt, logger.FormatQuota(relayInfo.UserQuota)}
		} else {
			content = "{{value}}，当前剩余额度为 {{value}}，为了不影响您的使用，请及时充值。<br/>充值链接：<a href='{{value}}'>{{value}}</a>"
			values = []interface{}{prompt, logger.FormatQuota(relayInfo.UserQuota), topUpLink, topUpLink}
		}

		notifyCtx, notifyCancel := context.WithTimeout(context.Background(), quotaNotifyBudget)
		err := NotifyUser(notifyCtx, relayInfo.UserId, relayInfo.UserEmail, relayInfo.UserSetting, dto.NewNotify(dto.NotifyTypeQuotaExceed, prompt, content, values))
		notifyCancel()
		if err != nil {
			common.SysError(fmt.Sprintf("failed to send quota notify to user %d: %s", relayInfo.UserId, err.Error()))
		}
	})
}
