package app

import (
	"errors"
	"fmt"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	hubnats "github.com/LurusTech/lurus-hub/internal/pkg/nats"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/bytedance/gopkg/util/gopool"
)

// AsyncGo dispatches PostConsumeQuota's fire-and-forget side effects.
var AsyncGo = gopool.Go

var debitWalletGRPC = common.DebitWalletGRPC

var settleWithBreaker = common.SettleWithBreaker

var decreaseTokenQuotaSeam = repo.DecreaseTokenQuota

// ErrTokenQuotaInsufficient marks the per-key spending cap rejection inside PreConsumeTokenQuota.
var ErrTokenQuotaInsufficient = errors.New("token quota is not enough")

func PreConsumeTokenQuota(relayInfo *relaycommon.RelayInfo, quota int) error {
	if quota < 0 {
		return errors.New("quota must not be negative")
	}
	if relayInfo.IsPlayground {
		return nil
	}
	if relayInfo.TokenUnlimited {
		return repo.DecreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, quota)
	}
	ok, err := repo.DecreaseTokenQuotaIfEnough(relayInfo.TokenId, relayInfo.TokenKey, quota)
	if err != nil {
		return err
	}
	if !ok {
		remain := 0
		if tok, gErr := repo.GetTokenByKey(relayInfo.TokenKey, false); gErr == nil && tok != nil {
			remain = tok.RemainQuota
		}
		return fmt.Errorf("%w, token remain quota: %s, need quota: %s",
			ErrTokenQuotaInsufficient, logger.FormatQuotaASCII(remain), logger.FormatQuotaASCII(quota))
	}
	return nil
}

// sourceProductOf returns the resolved cross-product attribution tag for a relay.
func sourceProductOf(relayInfo *relaycommon.RelayInfo) string {
	if relayInfo != nil && relayInfo.SourceProduct != "" {
		return relayInfo.SourceProduct
	}
	return ratio_setting.DefaultSourceProduct
}

const creditPoolAlertHookTimeout = 2 * time.Second

var publishPoolThresholdSeam = hubnats.PublishPoolThreshold

func PostConsumeQuota(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int, sendEmail bool) (err error) {
	advisory := relayInfo.PlatformGoverned && (common.LocalLedgerAdvisory() || relayInfo.WalletAuthoritative)
	userLedger := relayInfo.UserId > 0

	if userLedger {
		if quota > 0 {
			err = repo.DecreaseUserQuota(relayInfo.UserId, quota)
		} else {
			err = repo.IncreaseUserQuota(relayInfo.UserId, -quota, false)
		}
		if err != nil {
			if !advisory {
				return err
			}
			metrics.BillingAdvisoryMeterLost.WithLabelValues("user_quota").Inc()
			common.SysLog(fmt.Sprintf("advisory: user quota shadow write lost (settle proceeds): userId=%d, quota=%d, err=%s",
				relayInfo.UserId, quota, err.Error()))
			err = nil
		}
	}

	dailyConsumed := quota + preConsumedQuota
	if userLedger && dailyConsumed > 0 {
		if dailyErr := repo.PostConsumeDailyQuota(relayInfo.UserId, dailyConsumed); dailyErr != nil {
			common.SysLog("failed to update daily quota: " + dailyErr.Error())
		}
	}

	poolDebit := quota + preConsumedQuota
	poolDebited, poolDebitLost := false, false
	if poolDebit > 0 && relayInfo.TokenId > 0 {
		poolDebited, poolDebitLost = debitTenantPool(relayInfo, poolDebit)
	}

	localQuotaConsistent := true
	if !relayInfo.IsPlayground && relayInfo.TokenId > 0 {
		if quota > 0 {
			err = decreaseTokenQuotaSeam(relayInfo.TokenId, relayInfo.TokenKey, quota)
		} else {
			err = repo.IncreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, -quota)
		}
		if err != nil {
			localQuotaConsistent = false
			if advisory {
				metrics.BillingAdvisoryMeterLost.WithLabelValues("token_quota").Inc()
			}
			common.SysLog(fmt.Sprintf("token quota update failed, compensating: userId=%d, quota=%d, err=%s",
				relayInfo.UserId, quota, err.Error()))
			if userLedger {
				compensateUserQuotaOnTokenFailure(relayInfo.UserId, quota)
			}
			if poolDebited {
				creditTenantPool(relayInfo, poolDebit, "token_write")
			}
		}
	}

	if sendEmail && userLedger && (quota+preConsumedQuota) != 0 {
		checkAndSendQuotaNotify(relayInfo, quota, preConsumedQuota)
	}

	totalQuota := quota + preConsumedQuota
	if totalQuota > 0 && relayInfo.TokenId > 0 {
		tpmTokenID := relayInfo.TokenId
		tpmTenantID := bizTPMTenantOf(tpmTokenID)
		tpmModel := relayInfo.OriginModelName
		AsyncGo(func() {
			RecordBusinessTPMUsage(tpmTokenID, tpmTenantID, totalQuota)
			RecordBusinessTPMModelUsage(tpmTenantID, tpmModel, totalQuota)
		})
	}

	if totalQuota > 0 && userLedger {
		costSpikeUserID := relayInfo.UserId
		AsyncGo(func() {
			RecordCostSpikeWindow(costSpikeUserID, totalQuota)
		})
	}

	settlePlatformWallet(relayInfo, totalQuota, preConsumedQuota, localQuotaConsistent, advisory, poolDebitLost)

	if !localQuotaConsistent && !advisory {
		return err
	}
	return nil
}

func compensateUserQuotaOnTokenFailure(userId int, quota int) {
	if quota > 0 {
		if compErr := repo.IncreaseUserQuota(userId, quota, false); compErr != nil {
			common.SysError(fmt.Sprintf("CRITICAL: compensation failed: userId=%d, quota=%d, err=%s",
				userId, quota, compErr.Error()))
		}
		return
	}
	if compErr := repo.DecreaseUserQuota(userId, -quota); compErr != nil {
		common.SysError(fmt.Sprintf("CRITICAL: compensation failed: userId=%d, quota=%d, err=%s",
			userId, -quota, compErr.Error()))
	}
}
